package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// probeSettleTimeout bounds the settlement of a probe that ended, also after
// doctor was cancelled.
const probeSettleTimeout = 10 * time.Second

// probe makes one model call, the empty-call form from the live verification
// bench, with read-only access and an explicit output file for Codex. It is a
// worker run like any other: the account gate admits it in the free worker's
// process, or in the paid one only when paid runs are allowed and the cap
// leaves room, and it never waits for a slot. A refused probe makes no model
// call. The probe runs in the process the gate built, its environment and
// settings included, and settles once with the usage and cost it reported.
func probe(ctx context.Context, agent string, cfg *config.Config, entries []catalogEntry) (err error) {
	dir, err := os.MkdirTemp("", "duolog-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var model string
	var args []string
	if agent == "codex" {
		list := make([]route.Entry, 0, len(entries))
		for _, entry := range entries {
			list = append(list, entry.Entry)
		}
		var ok bool
		if model, ok = route.Latest(cfg.Models["luna"].Family, list); !ok {
			return fmt.Errorf("no visible model for luna probe")
		}
		args = []string{"exec", "--json", "--skip-git-repo-check", "-m", model, "-c", "model_reasoning_effort=low", "-c", "project_doc_max_bytes=0", "-s", "read-only", "--ephemeral", "-o", filepath.Join(dir, "answer.json"), "Reply with the word ok."}
	} else {
		model = "haiku"
		args = []string{"-p", "--model", model, "--effort", "low", "--no-session-persistence", "--output-format", "json", "--strict-mcp-config", "--permission-mode", "plan", "--", "Reply with the word ok."}
	}
	g, err := delegate.NewGate(cfg)
	if err != nil {
		return err
	}
	permit, err := admitProbe(ctx, g, cfg, agent, route.Normalize(agent, model), dir)
	if err != nil {
		return err
	}
	started := false
	var measured probeUsage
	defer func() {
		// One settlement of what the call measured, also when it failed or
		// was cancelled; a call that never started gives its hold back.
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeSettleTimeout)
		defer cancel()
		s := paidruns.Settlement{Input: measured.input, Cached: measured.cached, Output: measured.output,
			ReportedUSD: measured.cost, Started: started, CompleteUsage: started && measured.known}
		tokens := usage.Tokens{Input: s.Input, Cached: s.Cached, Write: measured.write, WriteHour: measured.writeHour, Output: s.Output}
		if priced, ok := usage.Cost(cfg, agent, model, tokens); ok && priced >= 0 {
			s.PricedUSD = priced
		}
		err = errors.Join(err, g.Finish(settleCtx, permit, s))
	}()
	p := permit.Process
	if err := g.Store.Launch(ctx, permit.Reservation.ID); err != nil {
		return err
	}
	bin, err := workerenv.LookPath(p.Prefix[0], p.Env)
	if err != nil {
		return err
	}
	argv := p.Argv(args...)
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Dir = dir
	cmd.Env = p.Env
	cmd.Stdin = strings.NewReader("")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	started = true
	attachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeSettleTimeout)
	attachErr := g.Store.Attach(attachCtx, permit.Reservation.ID, paidruns.ProcessOwner(cmd.Process.Pid))
	cancel()
	if attachErr != nil {
		// A call that cannot be recorded does not run on unseen.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return attachErr
	}
	waitErr := cmd.Wait()
	output := stdout.Bytes()
	measured = readProbeUsage(agent, output)
	if waitErr != nil {
		return waitErr
	}
	if agent == "codex" {
		answer, err := os.ReadFile(filepath.Join(dir, "answer.json"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(answer)) != "ok" {
			return fmt.Errorf("missing expected answer")
		}
		return nil
	}
	var result struct {
		IsError bool   `json:"is_error"`
		Result  string `json:"result"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return err
	}
	if result.IsError || strings.TrimSpace(result.Result) != "ok" {
		return fmt.Errorf("missing expected answer")
	}
	return nil
}

// admitProbe admits the probe as a worker of agent that runs in dir: the
// free variant, then the paid one when paid runs allow it. Its settings
// layers are found where the probe looks: the home folder, dir, this platform
// and the probe's own environment. It never waits; a refusal is the error,
// worded as delegate words it, and nothing is run.
func admitProbe(ctx context.Context, g *paidruns.Gate, cfg *config.Config, agent, family, dir string) (paidruns.Permit, error) {
	home, _ := os.UserHomeDir()
	free, err := workerenv.Build(workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free,
		Config: cfg, Environ: os.Environ(), Role: "probe", Minimal: true})
	if err != nil {
		return paidruns.Permit{}, err
	}
	loc := workerenv.Locations{Home: home, Root: dir, GOOS: runtime.GOOS, Environ: free.Env}
	variants := []workerenv.Variant{workerenv.Free}
	for _, c := range paidruns.Candidates([]config.Target{{Agent: agent}}, true, cfg.PaidRuns) {
		if c.Variant == workerenv.Paid {
			variants = append(variants, workerenv.Paid)
		}
	}
	var refusals []string
	for _, variant := range variants {
		in := workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: variant, Config: cfg,
			Environ: os.Environ(), Role: "probe", Minimal: true, Locations: loc}
		permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: family, Role: "probe",
			Owner: paidruns.ProcessOwner(os.Getpid())})
		var refusal *paidruns.Refusal
		if errors.As(err, &refusal) {
			refusals = append(refusals, delegate.RefusalLine(refusal))
			continue
		}
		return permit, err
	}
	return paidruns.Permit{}, fmt.Errorf("refused, no model call made: %s", strings.Join(refusals, "; "))
}

// probeUsage is what a probe reported: tokens, Cached and write disjoint
// subsets of input, writeHour the part of write kept for an hour, and the
// CLI's cost when it gave one.
type probeUsage struct {
	input, cached, output int64
	write, writeHour      int64
	cost                  *float64
	known                 bool
}

// readProbeUsage reads the usage of a probe's output: Claude's result object
// with its cost, or Codex's turn.completed events.
func readProbeUsage(agent string, output []byte) probeUsage {
	var u probeUsage
	if agent == "claude" {
		var result struct {
			Usage *struct {
				Input    int64 `json:"input_tokens"`
				Cached   int64 `json:"cache_read_input_tokens"`
				Creation int64 `json:"cache_creation_input_tokens"`
				Output   int64 `json:"output_tokens"`
				Split    *struct {
					Hour int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
			Cost *float64 `json:"total_cost_usd"`
		}
		if json.Unmarshal(output, &result) != nil || result.Usage == nil {
			return u
		}
		x := result.Usage
		if x.Input < 0 || x.Cached < 0 || x.Creation < 0 || x.Output < 0 {
			return u
		}
		// Writes without a split count as five-minute writes, as the runner
		// counts them.
		u = probeUsage{input: x.Input + x.Cached + x.Creation, cached: x.Cached, write: x.Creation,
			output: x.Output, known: true}
		if x.Split != nil {
			u.writeHour = min(max(x.Split.Hour, 0), x.Creation)
		}
		if c := result.Cost; c != nil && *c >= 0 && *c <= 1e9 {
			u.cost = c
		}
		return u
	}
	scan := bufio.NewScanner(bytes.NewReader(output))
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		var event struct {
			Type  string `json:"type"`
			Usage *struct {
				Input  int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(scan.Bytes(), &event) != nil || event.Type != "turn.completed" || event.Usage == nil {
			continue
		}
		x := event.Usage
		if x.Input < 0 || x.Cached < 0 || x.Output < 0 || x.Cached > x.Input {
			continue
		}
		u.input, u.cached, u.output, u.known = u.input+x.Input, u.cached+x.Cached, u.output+x.Output, true
	}
	return u
}
