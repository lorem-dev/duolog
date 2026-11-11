package peer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func peerSubmoduleFile(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPeerSubmoduleWorkStateOnOpenAndSend(t *testing.T) {
	r := newRig(t, baseScript())
	f := submodule.New(t)
	peerSubmoduleFile(t, f.Superproject, ".duolog/config.yaml", "paid_runs:\n  usable_share: 0.4\nroles:\n  implement:\n    use: {agent: claude, model: sonnet}\n    balance: false\nrex:\n  auto_answers: []\n")
	peerSubmoduleFile(t, f.Submodule, ".duolog/config.yaml", "paid_runs:\n  usable_share: 0.2\nroles:\n  implement:\n    use: {agent: codex, model: luna}\n    balance: false\nrex:\n  auto_answers: [{match: 'Which environment', answer: Staging}]\n")
	r.root = f.Superproject
	var err error
	r.cfg, err = configfile.Load(r.root)
	if err != nil {
		t.Fatal(err)
	}
	r.reg, err = session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	r.lead("codex", mode.Ask)
	models, paidGate := r.deps.Models, r.deps.PaidGate
	var inputs []corelaunch.ProcessInput
	r.useDeps(func(d *Deps) {
		build := d.Build
		d.Build = func(in corelaunch.ProcessInput) (corelaunch.Process, error) {
			inputs = append(inputs, in)
			return build(in)
		}
		d.Models = modelsOf(func(_ context.Context, cfg *config.Config, cache string, target config.Target) (string, error) {
			if cfg.Roles["implement"].Use.Agent != "codex" || !sameWorkRoot(filepath.Dir(cache), filepath.Join(f.Submodule, ".duolog")) {
				t.Fatal("wrong model config or cache", cache)
			}
			return "gpt-6.1-" + target.Model, nil
		})
		d.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
			if cfg.Roles["implement"].Use.Agent != "codex" || cfg.PaidRuns.UsableShare != 0.2 {
				t.Fatal("wrong admission config")
			}
			return r.gate, nil
		}
		d.OnEnvFile = func(path string) {
			if !sameWorkRoot(filepath.Dir(path), filepath.Join(f.Submodule, ".duolog")) {
				t.Fatal("wrong launch state", path)
			}
		}
	})
	t.Chdir(f.Superproject)
	opened, err := Open(WithWorkDir(r.ctx, f.Linked), "implement")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := r.reg.Entries()
	if err != nil || len(entry) != 1 || entry[0].WorkRoot != f.Linked {
		t.Fatal(entry, err)
	}
	if calls := r.named("open-session"); len(calls) != 1 || !strings.Contains(strings.Join(calls[0], " "), f.Linked) {
		t.Fatal(calls)
	}
	peerSubmoduleFile(t, f.Linked, "task.md", "## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n")
	peerSubmoduleFile(t, f.Linked, "a.go", "package a\n")
	line := corescreen.TaskLine(f.Linked, filepath.Join(f.Linked, "task.md"), policy.ReportPath(opened.ID))
	r.panes(withScreens(codexIdle(""), codexIdle(""), codexIdle(line), working()))
	if err := Send(r.ctx, opened.ID, filepath.Join(f.Linked, "task.md")); err != nil {
		t.Fatal(err)
	}
	for _, in := range inputs {
		if in.Agent != "codex" || in.Config.Roles["implement"].Use.Model != "luna" || !sameWorkRoot(in.StateDir, f.Submodule) {
			t.Fatalf("wrong work input: %+v", in)
		}
	}
	checkPeerSubmoduleState(t, r, f, entry[0])
	r.useDeps(func(d *Deps) { d.Models, d.PaidGate, d.OnEnvFile = models, paidGate, func(string) {} })
	r.panes(withScreens(claudeIdle("")))
	if _, err := Open(r.ctx, "implement"); err != nil {
		t.Fatal(err)
	}
	entries, err := r.reg.Entries()
	if err != nil || len(entries) != 2 || entries[0].RexSession != entries[1].RexSession {
		t.Fatal("separate Rex sessions", entries, err)
	}
	if len(r.named("open-session")) != 0 || len(r.named("open-tab")) != 1 {
		t.Fatal(r.calls())
	}
}

func checkPeerSubmoduleState(t *testing.T, r *rig, f submodule.Fixture, entry session.Entry) {
	t.Helper()
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !sameWorkRoot(e.bus.Directory(), project.LeadHome(f.Linked)) {
		t.Fatal(e.bus.Directory())
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "coauthor-"+entry.ID+".model")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "sessions.json")); !os.IsNotExist(err) {
		t.Fatal("local registry", err)
	}
	if e.sessionLabel() != "duolog | "+filepath.Base(f.Superproject) {
		t.Fatal(e.sessionLabel())
	}
	owned, err := e.forEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if owned.cfg.Roles[entry.Role].Use.Agent != "codex" || e.cfg.Roles[entry.Role].Use.Agent != "claude" {
		t.Fatal("entry rules mutated caller")
	}
	question := Screen{Question: "Which environment", Options: []Option{{Label: "Staging", Selected: true}}}
	if answered, err := e.autoAnswer(context.Background(), entry, question); err != nil || !answered {
		t.Fatal("wrong automatic answer config", answered, err)
	}
	if !sameWorkRoot(e.adoptionRoot(), f.Superproject) {
		t.Fatal("wrong caller adoption root")
	}
	if !sameWorkRoot(owned.adoptionRoot(), f.Submodule) {
		t.Fatal("copied superproject settings")
	}
	leadRoot := f.SuperLinked
	if _, err := r.reg.ObserveLead(session.LeadObservation{ID: "lead-1", Agent: "codex", Root: &leadRoot}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !sameWorkRoot(owned.leadRoot(), f.SuperLinked) {
		t.Fatal("lost recorded lead checkout")
	}
}
