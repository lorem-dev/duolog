package peer

import (
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/bus"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPeerWaitReturnsPendingGateAskWithoutCapture(t *testing.T) {
	r := newRig(t, baseScript())
	opened, err := Open(r.ctx, "implement")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bus.Open(r.reg.Dir, r.cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	n, err := b.Send(bus.Message{From: "duolog/ask.pending", To: "codex/lead.lead-1", K: "ask", S: "approval required"})
	if err != nil {
		t.Fatal(err)
	}
	before := len(r.calls())
	started := time.Now()
	got, err := Wait(r.ctx, opened.ID)
	if err != nil || got.State != Question || !strings.Contains(got.Question, "approval required") || time.Since(started) > time.Second {
		t.Fatal(got, err)
	}
	if len(r.calls()) != before {
		t.Fatal("captured screen while gate question was pending")
	}
	lines, err := b.Inbox("codex/lead.lead-1")
	if err != nil || len(lines) != 1 {
		t.Fatal("wait consumed lead delivery cursor", err)
	}
	_ = n
}

func TestPeerEnvironmentReachesSpawnWithRole(t *testing.T) {
	r := newRig(t, baseScript())
	if _, err := Open(r.ctx, "implement"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.started(), " ")
	if !strings.Contains(joined, "DUOLOG_ROLE=implement") {
		t.Fatal("role missing from pane environment")
	}
}

func TestPeerUsesLinkedWorkRootForPane(t *testing.T) {
	r := newRig(t, baseScript())
	work := t.TempDir()
	git := filepath.Join(r.root, ".git")
	for _, dir := range []string{"objects", "refs/heads", "worktrees/feature"} {
		if err := os.MkdirAll(filepath.Join(git, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{filepath.Join(git, "HEAD"): "ref: refs/heads/main\n", filepath.Join(git, "config"): "[core]\nrepositoryformatversion=0\nbare=false\n", filepath.Join(git, "worktrees/feature/HEAD"): "ref: refs/heads/feature\n", filepath.Join(git, "worktrees/feature/commondir"): "../..\n", filepath.Join(git, "worktrees/feature/gitdir"): filepath.Join(work, ".git") + "\n", filepath.Join(work, ".git"): "gitdir: " + filepath.Join(git, "worktrees/feature") + "\n"}
	for path, text := range files {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(work)
	env, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(env.root)
	want, _ := os.Stat(work)
	if info == nil || !os.SameFile(info, want) {
		t.Fatal("peer pane uses main checkout", env.root)
	}
	// Split's cwd is env.root in the real Open path; the fake records that argv.
	if _, err := Open(r.ctx, "implement"); err != nil {
		t.Fatal(err)
	}
	args := r.named("split")
	if len(args) != 1 {
		t.Fatal(args)
	}
	if !strings.Contains(strings.Join(args[0], " "), work) {
		t.Fatal("pane -c lost work root", args)
	}
}

func TestExpiredAskIsNoLongerPending(t *testing.T) {
	dir := t.TempDir()
	address := "codex/lead.lead-1"
	write := func(sent time.Time, n int) {
		t.Helper()
		line, err := json.Marshal(bus.Message{N: int64(n), T: sent.UTC().Format(time.RFC3339Nano), From: "duolog/ask.x", To: address, K: "ask", S: "approval required"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), append(line, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(time.Now().Add(-askLifetime-time.Second), 1)
	if got, err := pendingAsk(dir, address); err != nil || got != "" {
		t.Fatal(got, err)
	}
	write(time.Now(), 2)
	if got, err := pendingAsk(dir, address); err != nil || got == "" {
		t.Fatal(got, err)
	}
}
