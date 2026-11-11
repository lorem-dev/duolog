package peer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestQueueRefusesSubmoduleWithoutConfigExplicitly(t *testing.T) {
	r, f := queueSubmoduleRig(t)
	peerSubmoduleFile(t, f.Nested, "task.md", queueTask)
	t.Chdir(f.Superproject)
	_, err := QueueAdd(r.ctx, "implement", f.Nested, "task.md", "")
	if err == nil || !strings.Contains(err.Error(), "submodules without .duolog/config.yaml") {
		t.Fatalf("plain submodule refusal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.Nested, ".duolog")); !os.IsNotExist(err) {
		t.Fatalf("plain submodule acquired state: %v", err)
	}
}

const (
	queueSuperConfig     = "roles:\n  implement:\n    use: {agent: claude, model: sonnet}\n    balance: false\n"
	queueSubmoduleConfig = "roles:\n  implement:\n    use: {agent: codex, model: luna}\n    balance: false\n  port:\n    use: {agent: codex, model: luna}\n    fallback: {agent: claude, model: sonnet}\n    balance: false\n"
	queueTask            = "## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n- a.go\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n"
)

// queueSubmoduleRig is a rig whose lead lives in the superproject's lead home,
// with a different implement agent in the superproject and the submodule.
func queueSubmoduleRig(t *testing.T) (*rig, submodule.Fixture) {
	t.Helper()
	r := newRig(t, withScreens(openAndSend()...))
	f := submodule.New(t)
	peerSubmoduleFile(t, f.Superproject, ".duolog/config.yaml", queueSuperConfig)
	peerSubmoduleFile(t, f.Submodule, ".duolog/config.yaml", queueSubmoduleConfig)
	for _, dir := range []string{f.Linked, f.Unrelated} {
		peerSubmoduleFile(t, dir, "task.md", queueTask)
		peerSubmoduleFile(t, dir, "a.go", "package a\n")
	}
	r.root = f.Superproject
	var err error
	if r.cfg, err = configfile.Load(r.root); err != nil {
		t.Fatal(err)
	}
	if r.reg, err = session.Open(project.LeadHome(f.Linked)); err != nil {
		t.Fatal(err)
	}
	r.lead("codex", mode.Ask)
	r.useDeps(func(*Deps) {})
	return r, f
}

// submoduleCtx is the rig's options as a command run in the submodule has them.
func (r *rig) submoduleCtx(f submodule.Fixture) context.Context {
	r.t.Helper()
	cfg, err := configfile.Load(f.Submodule)
	if err != nil {
		r.t.Fatal(err)
	}
	return WithOptions(context.Background(), r.options(Options{Root: f.Submodule, Config: cfg, Lead: "lead-1"}))
}

func TestSubmoduleQueueUsesSharedHome(t *testing.T) {
	r, f := queueSubmoduleRig(t)
	started := r.detachedRuns()
	var launched []string
	r.useDeps(func(d *Deps) {
		build := d.Build
		d.Build = func(in corelaunch.ProcessInput) (corelaunch.Process, error) {
			launched = append(launched, in.Agent)
			return build(in)
		}
	})
	sub := r.submoduleCtx(f)
	outside := t.TempDir()
	peerSubmoduleFile(t, outside, "secret.md", queueTask)
	symlink(t, filepath.Join(outside, "secret.md"), filepath.Join(f.Linked, "escape.md"))

	t.Chdir(f.Linked)
	if _, err := QueueAdd(sub, "implement", f.Linked, "task.md", ""); err != nil {
		t.Fatal(err)
	}
	_, escapingTaskErr := QueueAdd(sub, "implement", f.Linked, "escape.md", "")
	if ok, err := StartQueueRun(sub, nil); err != nil || !ok || len(*started) != 1 {
		t.Fatal("no detached run", ok, err, *started)
	}
	run := (*started)[0]
	logDir := filepath.Dir(run[1])
	if !sameWorkRoot(run[0], f.Superproject) || !slices.Contains(run, "lead-1") {
		t.Fatal("detached run", run)
	}

	t.Chdir(f.Superproject)
	_, unrelatedErr := QueueAdd(r.ctx, "implement", f.Unrelated, "task.md", "")
	port, err := QueueAdd(r.ctx, "port", f.Linked, "task.md", "")
	if err != nil {
		t.Fatal("queued role not read from the queued project", err)
	}
	if err := QueueRemove(r.ctx, port.ID); err != nil {
		t.Fatal(err)
	}
	items := r.queue()
	real, err := filepath.EvalSymlinks(f.Linked)
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	if _, err := StartQueueRun(r.ctx, func(agent string) bool { asked = append(asked, agent); return false }); err != nil {
		t.Fatal(err)
	}
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	launchedAgent := ""
	if len(launched) > 0 {
		launchedAgent = launched[0]
	}

	if len(items) != 1 || items[0].Cwd != real {
		t.Fatal(items)
	}
	if !slices.Equal(asked, []string{"codex"}) {
		t.Fatal("head role judged by the caller's config", asked)
	}
	if launchedAgent != "codex" {
		t.Fatal(launchedAgent)
	}
	if !sameWorkRoot(logDir, project.LeadHome(f.Linked)) {
		t.Fatal(logDir)
	}
	if unrelatedErr == nil || escapingTaskErr == nil {
		t.Fatal("unsafe queue target", unrelatedErr, escapingTaskErr)
	}
	if got := r.queue(); len(got) != 1 || got[0].State != QueueOpened {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", queueFile)); !os.IsNotExist(err) {
		t.Fatal("local queue", err)
	}
}
