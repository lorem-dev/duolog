package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func submoduleFile(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSubmoduleCommandRegistryReaders(t *testing.T) {
	for _, name := range []string{"mode", "route", "roster", "doctor", "quota", "skill", "task start", "task finish", "peer known"} {
		t.Run(name, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Setenv("DUOLOG_DEPTH", "")
			t.Setenv("DUOLOG_LEAD", "")
			f := submodule.New(t)
			submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\nroles:\n  implement:\n    use: {agent: claude, model: sonnet}\n")
			submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: true\nroles:\n  implement:\n    use: {agent: claude, model: haiku}\n    fallback: {agent: codex, model: luna}\n    balance: false\n")
			t.Chdir(f.Linked)
			reg, err := session.Open(project.LeadHome(f.Linked))
			if err != nil {
				t.Fatal(err)
			}
			if err := reg.RegisterLead(session.Lead{ID: "shared-lead", Target: config.Target{Agent: "claude", Model: "opus"}, Mode: mode.Full, PID: os.Getpid()}); err != nil {
				t.Fatal(err)
			}
			if err := reg.SetMode("shared-lead", mode.Plan); err != nil {
				t.Fatal(err)
			}
			if err := reg.MarkUnavailable("claude", time.Time{}); err != nil {
				t.Fatal(err)
			}
			checkSubmoduleCommand(t, name, f, reg)
			if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "sessions.json")); !os.IsNotExist(err) {
				t.Fatal("work-local registry", err)
			}
		})
	}
}

func checkSubmoduleCommand(t *testing.T, name string, f submodule.Fixture, reg *session.Registry) {
	t.Helper()
	switch name {
	case "mode":
		out, err := executeBusCommand(t, "mode", "plan")
		if err != nil || strings.TrimSpace(out) != "plan" {
			t.Fatal(out, err)
		}
		_, effective, err := reg.EffectiveMode("shared-lead")
		if err != nil || effective != mode.Plan {
			t.Fatal(effective, err)
		}
	case "route":
		out, err := executeBusCommand(t, "route", "implement", "--json")
		if err != nil {
			t.Fatal(out, err)
		}
		var got struct {
			Agent, Model string
			IsFallback   bool `json:"is_fallback"`
			Permission   struct{ Level mode.Mode }
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(out, err)
		}
		if got.Agent != "codex" || got.Model != "luna" || !got.IsFallback || got.Permission.Level != mode.Plan {
			t.Fatal(out)
		}
	case "roster":
		d := testDeps(t)
		a := d.Delegate.Agents["codex"]
		a.Models = submoduleModels{t: t, cache: filepath.Join(project.StateRoot(f.Linked), ".duolog", "cache")}
		d.Delegate.Agents["codex"] = a
		out, err := executeBusCommand(t, "roster", "--versions")
		if err != nil || !strings.Contains(out, "lead=shared-lead") || !strings.Contains(out, "claude=unavailable") || !strings.Contains(out, "implement=haiku") {
			t.Fatal(out, err)
		}
	case "doctor":
		testDeps(t).LocateRex = func() (string, bool) { return "", false }
		out, err := executeBusCommand(t, "doctor")
		if err != nil || !strings.Contains(out, "claude=unavailable") {
			t.Fatal(out, err)
		}
	case "quota":
		out, err := executeBusCommand(t, "quota", "--json")
		if err != nil {
			t.Fatal(out, err)
		}
		var got map[string]struct {
			Cleared bool `json:"cleared_unavailable"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatal(out, err)
		}
		if !got["claude"].Cleared {
			t.Fatal(out)
		}
		marks, err := reg.Unavailable()
		if err != nil || marks["claude"] {
			t.Fatal(marks, err)
		}
	case "skill":
		checkSubmoduleSkill(t, f, reg)
	case "task start", "task finish":
		out, err := executeBusCommand(t, "task", "start", "submodule work")
		if err != nil {
			t.Fatal(out, err)
		}
		j := &journal.Journal{Dir: filepath.Join(f.Submodule, ".duolog", "journal")}
		if _, ok, err := j.CurrentTask("shared-lead"); err != nil || !ok {
			t.Fatal("missing local task", ok, err)
		}
		if name == "task finish" {
			out, err = executeBusCommand(t, "task", "finish")
			if err != nil {
				t.Fatal(out, err)
			}
			if _, ok, err := j.CurrentTask("shared-lead"); err != nil || ok {
				t.Fatal("task not closed", ok, err)
			}
		}
		if _, err := os.Stat(filepath.Join(f.Superproject, ".duolog", "journal")); !os.IsNotExist(err) {
			t.Fatal("moved journal", err)
		}
	case "peer known":
		if err := reg.Record(session.Entry{ID: "shared-peer", Lead: "shared-lead", Block: "block:fake", WorkRoot: f.Linked}); err != nil {
			t.Fatal(err)
		}
		if !peerKnown(reg, "shared-peer") {
			t.Fatal("missing shared peer")
		}
	}
}

type submoduleModels struct {
	t     *testing.T
	cache string
}

func (m submoduleModels) Resolve(_ context.Context, cfg *config.Config, cache string, _ config.Target) (string, error) {
	m.t.Helper()
	if cache != m.cache {
		m.t.Errorf("cache = %s, want %s", cache, m.cache)
	}
	if cfg.Roles["implement"].Use.Model != "haiku" {
		m.t.Error("loaded lead config for work")
	}
	return "gpt-test", nil
}

func checkSubmoduleSkill(t *testing.T, f submodule.Fixture, reg *session.Registry) {
	t.Helper()
	submoduleFile(t, f.Linked, "skills/demo/SKILL.md", "Reply yes.\n")
	submoduleFile(t, f.Linked, ".duolog/skill-tests/demo.plan.yaml", "situations: [{id: s1, text: 'What now?', check: {starts: yes}}]\n")
	testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"result":"{\"s1\":\"yes\"}"}`})
	args := []string{"skill", "test", "skills/demo/SKILL.md", "--models", "claude/haiku/low", "--yes"}
	out, err := executeBusCommand(t, args...)
	if err == nil || !strings.Contains(err.Error(), "agent claude is unavailable") {
		t.Fatal(out, err)
	}
	if err := reg.ClearUnavailable("claude"); err != nil {
		t.Fatal(err)
	}
	out, err = executeBusCommand(t, args...)
	if err != nil || !strings.Contains(out, "1/1") {
		t.Fatal(out, err)
	}
	for _, path := range []string{filepath.Join(f.Linked, ".duolog", "skill-tests", "demo", "claude-haiku-low.json"), filepath.Join(f.Submodule, ".duolog", "usage.ndjson")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(path, err)
		}
	}
}
