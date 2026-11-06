// Package doctor reports local agent health; model calls require an explicit probe.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/peer"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// FixtureVersions is the single source of versions used to collect the fixtures.
const FixtureVersions = `{"codex":"0.165.0","claude":"2.1.312"}`

type Finding struct {
	Level   string
	Message string
}

// Pane is a caller-supplied pane record. Doctor never invokes Rex.
type Pane struct{ ID, Lead string }
type Options struct {
	Probe    bool
	Root     string
	Registry *session.Registry
	Panes    []Pane
	// RexBin is the located rex executable. Doctor never runs it.
	RexBin string
	// RexShadow is a rex in PATH that is not the Rex CLI, such as a Perl tool of
	// the same name. duolog skips it.
	RexShadow string
	// Lead holds observations of how the lead itself was started, by agent.
	// Without one the lead's billing is unknown; duolog's own environment
	// never stands for it.
	Lead map[string]session.LeadRoute
}

func Run(ctx context.Context, cfg *config.Config, opts Options) []Finding {
	// Bound metadata and probe calls even when the caller has no deadline.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var out []Finding
	add := func(level, message string) { out = append(out, Finding{level, message}) }
	if time.Duration(cfg.Policy.AskTimeoutSeconds)*time.Second > gate.MaxAskWait {
		add("warning", "policy.ask_timeout_seconds exceeds the safe hook budget; waits are capped at 120 seconds")
	}
	root := opts.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			add("error", err.Error())
			return out
		}
	}
	for _, path := range install.InstructionPaths(root) {
		data, err := os.ReadFile(path)
		present := false
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSuffix(line, "\r") == install.InstallOffer {
					present = true
				}
			}
		}
		add("info", fmt.Sprintf("duolog install offer in %s: present=%t", path, present))
	}
	folder, err := trust.Folder("codex", root)
	if err != nil {
		add("warning", "Codex folder trust: "+err.Error())
	}
	hooks, err := trust.Hooks("codex", root)
	if err != nil {
		add("warning", "Codex hook trust: "+err.Error())
	}
	add("info", fmt.Sprintf("Codex hooks approved=%t folder trusted=%t", hooks.Trusted, folder))
	if home, err := os.UserHomeDir(); err != nil {
		add("info", "Claude folder trust: unknown")
	} else if trusted, known := trust.ClaudeFolder(home, root); !known {
		add("info", "Claude folder trust: unknown (Claude's user state could not be read)")
	} else if trusted {
		add("info", "Claude folder trust: trusted")
	} else {
		add("warning", "Claude folder trust: not trusted; Claude ignores this project's .claude/settings.json permissions.allow until you run Claude Code in this folder once and accept the trust dialog")
	}
	add("warning", "Hooks changed after review cannot be detected here; Codex itself will skip changed hooks. Review /hooks.")
	if runtime.GOOS == "windows" {
		add("info", peer.WindowsNotice)
	} else if opts.RexBin == "" {
		add("warning", "Rex unavailable; pane connectivity cannot be checked")
	}
	if opts.RexShadow != "" && runtime.GOOS != "windows" {
		uses := "set REX_BIN_DIR to the folder of the Rex CLI or put that folder first in PATH"
		if opts.RexBin != "" {
			uses = "duolog uses " + opts.RexBin + "; to run it as rex, put its folder first in PATH"
		}
		add("warning", "rex in PATH ("+opts.RexShadow+") is a script, not the Rex CLI; duolog skips it. "+uses)
	}
	var baseline map[string]string
	_ = json.Unmarshal([]byte(FixtureVersions), &baseline)
	available := map[string]bool{}
	for _, agent := range []string{"claude", "codex"} {
		if _, err := exec.LookPath(agent); err != nil {
			add("warning", agent+" binary missing")
			continue
		}
		available[agent] = true
		if !capability.HasDuolog(agent, root) {
			add("warning", agent+" has no duolog hooks; its sessions get the sandbox only")
		}
		data, err := metadata(ctx, agent, "--version")
		if err != nil {
			add("warning", agent+" version: "+err.Error())
			continue
		}
		version := versionPattern.FindString(string(data))
		if version == "" {
			add("warning", agent+" version could not be read")
			continue
		}
		add("info", agent+" version "+version)
		if newer(version, baseline[agent]) {
			add("warning", fmt.Sprintf("%s %s is newer than fixture version %s", agent, version, baseline[agent]))
		}
	}
	add("info", bridgeLine(cfg, root, available))
	out = append(out, workerModeFindings(cfg, root)...)
	out = append(out, billing(ctx, cfg, root, available, opts.Lead)...)
	// Said every time while the person sets or lists a key that paid runs do
	// not let through, so the change is never silent. Names only, never
	// values. An explicit list replaces the default, so the example keeps the
	// agent's paid env, and its cap when it has one.
	dropped := workerenv.DroppedKeys(cfg, os.Environ())
	for _, agent := range []string{"claude", "codex"} {
		paid := cfg.PaidRuns.Agent(agent)
		limit := "5"
		if paid.DailyCapUSD > 0 {
			limit = strconv.FormatFloat(paid.DailyCapUSD, 'f', -1, 64)
		}
		for _, name := range dropped[agent] {
			env := slices.Clone(paid.Env)
			if !slices.Contains(env, name) {
				env = append(env, name)
			}
			add("warning", workerenv.MigrationLine(agent, name)+" Example config: paid_runs: {"+agent+": {allow: true, daily_cap_usd: "+limit+", env: ["+strings.Join(env, ", ")+"]}}")
		}
	}
	var entries []catalogEntry
	if available["codex"] {
		var err error
		entries, err = readCatalog(ctx)
		if err != nil {
			add("warning", "Codex catalog: "+err.Error())
		} else {
			out = append(out, checkModels(cfg, entries)...)
		}
	}
	if opts.Registry != nil {
		unavailable, err := opts.Registry.Unavailable()
		if err != nil {
			add("warning", "availability: "+err.Error())
		} else {
			for _, agent := range []string{"claude", "codex"} {
				if unavailable[agent] {
					add("warning", agent+"=unavailable")
				}
			}
		}
	}
	panes := append([]Pane(nil), opts.Panes...)
	sort.Slice(panes, func(i, j int) bool { return panes[i].ID < panes[j].ID })
	for _, pane := range panes {
		if opts.Registry == nil {
			add("warning", "pane "+pane.ID+": lead state unknown")
			continue
		}
		_, live, err := opts.Registry.Lead(pane.Lead)
		if err != nil {
			add("warning", "pane "+pane.ID+": "+err.Error())
		} else if !live {
			add("warning", fmt.Sprintf("Pane %s belongs to ended lead %s; duolog peer close %s", pane.ID, pane.Lead, pane.ID))
		}
	}
	if opts.Probe {
		for _, agent := range []string{"claude", "codex"} {
			if !available[agent] {
				continue
			}
			if err := probe(ctx, agent, cfg, entries); err != nil {
				add("warning", agent+" probe failed: "+err.Error())
				continue
			}
			if opts.Registry != nil {
				if err := opts.Registry.ClearUnavailable(agent); err != nil {
					add("error", agent+" probe succeeded but availability could not be cleared: "+err.Error())
					continue
				}
			}
			add("info", agent+" probe succeeded")
		}
	}
	return out
}

// billing reports, per available agent, how its worker, its peer and its lead
// are billed, as quota does: verdicts with their signals, the account, the
// spend and the sessions. Reporting reads the account state and admits
// nothing. A lead without an observation is unknown.
func billing(ctx context.Context, cfg *config.Config, root string, available map[string]bool, leads map[string]session.LeadRoute) []Finding {
	out := []Finding{{"info", "managed policy outside files is not checked: duolog reads managed settings files only, not device management profiles or the Windows registry"}}
	g, err := delegate.NewGate(cfg)
	if err != nil {
		return append(out, Finding{"warning", "billing: " + err.Error()})
	}
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	report := func(r paidruns.Report) {
		for i, line := range r.Lines() {
			level := "info"
			if i == 0 && r.Assessment.Verdict != paidruns.Subscription || strings.Contains(line, "paid overage") {
				level = "warning"
			}
			out = append(out, Finding{level, line})
		}
	}
	// The account's lines come once, from the first worker report.
	var account []string
	for _, agent := range []string{"claude", "codex"} {
		if !available[agent] {
			continue
		}
		var worker paidruns.Report
		for _, shape := range []workerenv.Shape{workerenv.Worker, workerenv.Peer} {
			in := workerenv.ProcessInput{Agent: agent, Shape: shape, Variant: workerenv.Free, Config: cfg, Environ: environ,
				Locations: workerenv.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ}}
			r, err := g.Report(ctx, in)
			if r.Assessment.Agent == "" {
				out = append(out, Finding{"warning", fmt.Sprintf("%s %s billing: %v", agent, shape, err)})
				continue
			}
			report(r)
			if shape == workerenv.Worker {
				worker = r
				if account == nil {
					account = r.AccountLines()
				}
			}
		}
		in, ok := session.LeadInput(cfg, leads[agent], home, root)
		if !ok {
			out = append(out, Finding{"info", agent + " lead: unknown (no observation of the lead's command and environment)"})
			continue
		}
		lead, err := g.Report(ctx, in)
		if lead.Assessment.Agent == "" {
			out = append(out, Finding{"warning", agent + " lead billing: " + err.Error()})
			continue
		}
		report(lead)
		if worker.Assessment.Agent != "" {
			if diff := paidruns.CompareLead(lead, worker); diff != "" {
				out = append(out, Finding{"warning", diff})
			}
		}
	}
	for _, line := range account {
		level := "info"
		// A hold kept without a conclusion takes a slot and its estimate.
		if strings.HasPrefix(line, "held: ") {
			level = "warning"
		}
		out = append(out, Finding{level, line})
	}
	return out
}

// bridgeLine names the MCP servers each agent could hand to a worker of the
// other, and whether bridging is on. Names only: no value is read out.
func bridgeLine(cfg *config.Config, root string, available map[string]bool) string {
	state := "off (capabilities.on_missing.mcp: " + cfg.Capabilities.OnMissing.MCP + ")"
	if cfg.Capabilities.OnMissing.MCP == capability.ActionBridge {
		state = "on"
	}
	parts := []string{"MCP bridge " + state}
	for _, agent := range []string{"claude", "codex"} {
		if !available[agent] {
			continue
		}
		ok, _ := capability.Bridgeable(agent, root)
		names := "none"
		if len(ok) > 0 {
			names = strings.Join(ok, ", ")
		}
		parts = append(parts, agent+" can lend: "+names)
	}
	return strings.Join(parts, "; ")
}

func metadata(ctx context.Context, agent string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, agent, args...)
	cmd.Stdin = strings.NewReader("")
	return cmd.Output()
}
