// Package doctor reports local agent health; model calls require an explicit probe.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/peer"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
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

func Run(ctx context.Context, d Deps, cfg *config.Config, opts Options) []Finding {
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
	for _, path := range d.instructionPaths(root) {
		data, err := os.ReadFile(path)
		present := false
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSuffix(line, "\r") == coreagent.InstallOffer {
					present = true
				}
			}
		}
		add("info", fmt.Sprintf("duolog install offer in %s: present=%t", path, present))
	}
	var folder bool
	var hooks coreagent.TrustState
	if t := d.Agents["codex"].Trust; t != nil {
		var err error
		if folder, err = t.Folder(root, os.Environ()); err != nil {
			add("warning", "Codex folder trust: "+err.Error())
		}
		if hooks, err = t.Hooks(root, os.Environ()); err != nil {
			add("warning", "Codex hook trust: "+err.Error())
		}
	}
	add("info", fmt.Sprintf("Codex hooks approved=%t folder trusted=%t", hooks.Trusted, folder))
	if home, err := os.UserHomeDir(); err != nil {
		add("info", "Claude folder trust: unknown")
	} else if trusted, known := d.ClaudeFolder(home, root); !known {
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
	for _, agent := range d.Names {
		if !d.Available(agent) {
			add("warning", agent+" binary missing")
			continue
		}
		available[agent] = true
		if !d.hasDuolog(agent, root) {
			add("warning", agent+" has no duolog hooks; its sessions get the sandbox only")
		}
		data, err := d.metadata(ctx, agent, "--version")
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
	add("info", bridgeLine(d, cfg, root, available))
	out = append(append(out, installFindings(d, root)...), workerModeFindings(d, cfg, root)...)
	out = append(out, billing(ctx, d, cfg, root, available, opts.Lead)...)
	// Said every time while the person sets or lists a key that paid runs do
	// not let through, so the change is never silent. Names only, never
	// values. An explicit list replaces the default, so the example keeps the
	// agent's paid env, and its cap when it has one.
	dropped := d.DroppedKeys(cfg, os.Environ())
	for _, agent := range d.Names {
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
			add("warning", corelaunch.MigrationLine(agent, name)+" Example config: paid_runs: {"+agent+": {allow: true, daily_cap_usd: "+limit+", env: ["+strings.Join(env, ", ")+"]}}")
		}
	}
	var entries []catalogEntry
	if available["codex"] {
		var err error
		entries, err = readCatalog(ctx, d)
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
			for _, agent := range d.Names {
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
		for _, agent := range d.Names {
			if !available[agent] {
				continue
			}
			if err := probe(ctx, d, agent, cfg, entries); err != nil {
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
	return append(out, d.instructionNotes(root)...)
}

// installFindings warns of each install that is out of date: a binary upgrade
// leaves the installed hooks and skills as they were.
func installFindings(d Deps, root string) []Finding {
	if d.InstallCheck == nil {
		return nil
	}
	home, _ := os.UserHomeDir()
	var out []Finding
	for _, line := range d.InstallCheck(root, home) {
		out = append(out, Finding{"warning", line})
	}
	return out
}

// billing reports, per available agent, how its worker, its peer and its lead
// are billed, as quota does: verdicts with their signals, the account, the
// spend and the sessions. Reporting reads the account state and admits
// nothing. A lead without an observation is unknown.
func billing(ctx context.Context, d Deps, cfg *config.Config, root string, available map[string]bool, leads map[string]session.LeadRoute) []Finding {
	out := []Finding{{"info", "managed policy outside files is not checked: duolog reads managed settings files only, not device management profiles or the Windows registry"}}
	g, err := d.PaidGate(cfg)
	if err != nil {
		return append(out, Finding{"warning", "billing: " + err.Error()})
	}
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	report := func(r paidruns.Report) {
		for i, line := range r.Lines() {
			level := "info"
			if i == 0 && r.Assessment.Verdict != paidrules.Subscription || strings.Contains(line, "paid overage") {
				level = "warning"
			}
			out = append(out, Finding{level, line})
		}
	}
	// The account's lines come once, from the first worker report.
	var account []string
	for _, agent := range d.Names {
		if !available[agent] {
			continue
		}
		var worker paidruns.Report
		for _, shape := range []corelaunch.Shape{corelaunch.Worker, corelaunch.Peer} {
			in := corelaunch.ProcessInput{Agent: agent, Shape: shape, Variant: corelaunch.Free, Config: cfg, Environ: environ,
				Locations: corelaunch.Locations{Home: home, Root: root, GOOS: runtime.GOOS, Environ: environ}}
			r, err := g.Report(ctx, in)
			if r.Assessment.Agent == "" {
				out = append(out, Finding{"warning", fmt.Sprintf("%s %s billing: %v", agent, shape, err)})
				continue
			}
			report(r)
			if shape == corelaunch.Worker {
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
// other, and whether bridging is on. Names only: no value is read out. Both
// the servers and the worker's own config are read from duolog's environment.
// With several agents, a server is lendable when any other agent accepts it.
func bridgeLine(d Deps, cfg *config.Config, root string, available map[string]bool) string {
	state := "off (capabilities.on_missing.mcp: " + cfg.Capabilities.OnMissing.MCP + ")"
	if cfg.Capabilities.OnMissing.MCP == caps.ActionBridge {
		state = "on"
	}
	parts := []string{"MCP bridge " + state}
	for _, agent := range d.Names {
		if !available[agent] {
			continue
		}
		ok := d.lendable(agent, root)
		lend := "none"
		if len(ok) > 0 {
			lend = strings.Join(ok, ", ")
		}
		parts = append(parts, agent+" can lend: "+lend)
	}
	return strings.Join(parts, "; ")
}

// lendable names the MCP servers of agent that some other registered agent
// accepts; an agent without Caps lends none.
func (d Deps) lendable(agent, root string) []string {
	from := d.Agents[agent].Caps
	if from == nil {
		return nil
	}
	ok, _ := caps.Lendable(from.Servers(root, os.Environ()), func(s caps.Server) string {
		reason := "no other agent"
		for _, other := range d.Names {
			to := d.Agents[other].Caps
			if other == agent || to == nil {
				continue
			}
			if reason = to.Refusal(root, s, os.Environ()); reason == "" {
				return ""
			}
		}
		return reason
	})
	return ok
}

// metadata runs agent's own metadata command through its Probe.
func (d Deps) metadata(ctx context.Context, agent string, args ...string) ([]byte, error) {
	pr, ok := d.Probes[agent]
	if !ok {
		return nil, fmt.Errorf("no probe for %s", agent)
	}
	return pr.Metadata(ctx, args...)
}

// instructionNotes says, for each instruction file the agents read in the
// project at root, whether it holds duolog's block as this version writes it.
func (d Deps) instructionNotes(root string) []Finding {
	var notes []Finding
	for _, path := range d.instructionPaths(root) {
		notes = append(notes, instructionFinding(path))
	}
	return notes
}

// instructionFinding says whether the instruction file at path holds duolog's
// block as this version writes it: current, outdated (install replaces it), or
// missing. Unpaired markers are a warning, as install refuses such a file.
func instructionFinding(path string) Finding {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Finding{"warning", fmt.Sprintf("duolog instructions in %s: %v", path, err)}
	}
	current, present, err := coreagent.BlockCurrent(data)
	switch {
	case err != nil:
		return Finding{"warning", fmt.Sprintf("duolog instructions in %s: %v; fix the markers, then run duolog install", path, err)}
	case current:
		return Finding{"info", fmt.Sprintf("duolog instructions in %s: current", path)}
	case present:
		return Finding{"info", fmt.Sprintf("duolog instructions in %s: outdated; run duolog install", path)}
	}
	return Finding{"info", fmt.Sprintf("duolog instructions in %s: missing", path)}
}
