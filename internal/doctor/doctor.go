// Package doctor reports local agent health; model calls require an explicit probe.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/trust"
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
	add("warning", "Hooks changed after review cannot be detected here; Codex itself will skip changed hooks. Review /hooks.")
	if opts.RexBin == "" {
		add("warning", "Rex unavailable; pane connectivity cannot be checked")
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
	var entries []catalogEntry
	if available["codex"] {
		var err error
		entries, err = readCatalog(ctx)
		if err != nil {
			add("warning", "Codex catalog: "+err.Error())
		} else {
			for _, f := range checkModels(cfg, entries) {
				out = append(out, f)
			}
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

func metadata(ctx context.Context, agent string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, agent, args...)
	cmd.Stdin = strings.NewReader("")
	return cmd.Output()
}
