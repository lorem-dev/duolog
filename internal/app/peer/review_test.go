package peer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestAutoAnswerProtectsPrivateStores(t *testing.T) {
	// Kills evaluating a pane command without the private usage-store roots.
	r := newRig(t, baseScript())
	p := r.ready()
	private := filepath.Join(t.TempDir(), "usage")
	r.useDeps(func(*Deps) {})
	opts := r.options(Options{Root: r.root, Config: r.cfg, Lead: "lead-1", ProtectedRoots: []string{private}})
	ctx := WithOptions(context.Background(), opts)
	e, err := setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := e.find(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	screen := parseFor("codex", []byte(commandPrompt("cat "+filepath.Join(private, "events.ndjson"), false)))
	chosen, ok, err := e.chooseAnswer(entry, screen, testPane("codex").Facts)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || chosen.decision.Kind != "deny" {
		t.Fatalf("choice %+v ok %v", chosen, ok)
	}
}

func TestSendWaitsForPeerLock(t *testing.T) {
	// Kills readiness and task-generation updates outside a per-peer send lock.
	r := newRig(t, baseScript())
	p := r.ready()
	path := filepath.Join(r.reg.Dir, "peer-send-"+p.ID+".lock")
	lock := flock.New(path)
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Millisecond)
	defer cancel()
	err := Send(ctx, p.ID, "task.md")
	if err == nil || !strings.Contains(err.Error(), "context deadline") {
		t.Fatalf("send ignored held lock: %v", err)
	}
}

func TestOpenPrunesOldEnvFiles(t *testing.T) {
	// Kills leaving abandoned pane environment files with API keys on disk.
	r := newRig(t, baseScript())
	old := filepath.Join(r.reg.Dir, ".peer-env-old.json")
	recent := filepath.Join(r.reg.Dir, ".peer-env-recent.json")
	for _, p := range []string{old, recent} {
		if err := os.WriteFile(p, []byte(`{"KEY":"example"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(old, at, at); err != nil {
		t.Fatal(err)
	}
	r.ready()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("stale env remains: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent env removed: %v", err)
	}
}

func TestPeerCreditsEachTaskGeneration(t *testing.T) {
	// Kills deduplicating co-author records by peer for its entire lifetime.
	r := newRig(t, baseScript())
	r.git()
	r.lead("claude", mode.Edits)
	p := r.open("implement")
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, gen := range []string{"task-1", "task-2"} {
		entry, err := r.reg.Update(p.ID, func(x *session.Entry) { x.Task, x.TaskGeneration, x.Base = "task.md", gen, map[string]string{} })
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.root, "changed.go"), []byte(gen), 0600); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := e.credit(entry); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Settlement clears TaskGeneration, but repeated polls still belong to
	// the same admitted task and must not add a lifetime-key credit.
	entry, err := r.reg.Update(p.ID, func(x *session.Entry) { x.TaskGeneration = ""; x.AdmissionID = "task-2" })
	if err != nil {
		t.Fatal(err)
	}
	if err := e.credit(entry); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(r.reg.Dir, "coauthors.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 2 {
		t.Fatalf("records %s", data)
	}
}

func TestAutoAnswerImportsThePeersLocalDenies(t *testing.T) {
	// Kills importing the polling command's checkout instead of the peer's denies.
	r, one, two := twoWorktrees(t)
	r.lead("claude", mode.Edits)
	r.cfg.Policy.Rules = append(r.cfg.Policy.Rules, config.Rule{Action: "exec", Match: "go test *", Decision: "allow", Roles: []string{"implement"}})
	t.Chdir(one)
	p := r.open("implement")
	local := filepath.Join(one, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(local), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`{"permissions":{"deny":["Bash(go:*)"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(two)
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	chosen, ok, err := e.chooseAnswer(r.entry(p.ID), parseFor("codex", []byte(commandPrompt("go test ./...", false))), testPane("codex").Facts)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || chosen.decision.Kind != "deny" {
		t.Fatalf("choice %+v ok %v", chosen, ok)
	}
}
