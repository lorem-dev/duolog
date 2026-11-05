package peer

import (
	"encoding/json"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/testbin"
	"os"
	"path/filepath"
	"slices"
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
	n, err := b.Send(bus.Message{From: "duolog/ask.pending", To: "codex/lead.lead-1", K: "ask", Ref: opened.ID, S: "approval required"})
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

// A gate ask names the peer whose worker sent it: waiting on another peer of
// the same lead must not stop on it.
func TestPeerWaitStopsOnlyOnItsOwnGateAsk(t *testing.T) {
	r := newRig(t, baseScript())
	r.cfg.Limits.MaxLiveSessions = 2
	first := r.open("implement")
	script := baseScript()
	script["window new"] = testbin.Scenario{Stdout: strings.ReplaceAll(splitReply, "block:peer", "block:second")}
	r.rex(script)
	second := r.open("mechanical")
	if first.State != Ready || second.State != Ready || first.ID == second.ID {
		t.Fatal(first, second)
	}
	b, err := bus.Open(r.reg.Dir, r.cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(bus.Message{From: "duolog/ask.first", To: "codex/lead.lead-1", K: "ask", Ref: first.ID, S: "exec make deploy"}); err != nil {
		t.Fatal(err)
	}
	got, err := Wait(r.ctx, second.ID)
	if err != nil || got.ID != second.ID || got.State != Ready || strings.Contains(got.Question, "make deploy") {
		t.Fatalf("the other peer's ask stopped this wait: %+v %v", got, err)
	}
	got, err = Wait(r.ctx, first.ID)
	if err != nil || got.ID != first.ID || got.State != Question || !strings.Contains(got.Question, "make deploy") {
		t.Fatalf("the peer's own ask did not stop its wait: %+v %v", got, err)
	}
}

// An ask that names no peer comes from a worker outside a pane, or from a pane
// opened before asks named their peer: it belongs to no peer's wait. The lead's
// hook still delivers it.
func TestPeerWaitSkipsAskOfNoPeer(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.open("implement")
	b, err := bus.Open(r.reg.Dir, r.cfg.Wire)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Send(bus.Message{From: "duolog/ask.worker", To: "codex/lead.lead-1", K: "ask", S: "exec make deploy"}); err != nil {
		t.Fatal(err)
	}
	got, err := Wait(r.ctx, e.ID)
	if err != nil || got.State != Ready || strings.Contains(got.Question, "make deploy") {
		t.Fatalf("an ask of no peer stopped the wait: %+v %v", got, err)
	}
}

// An ask whose asker gave up is no longer pending, though it was never answered:
// its gate hook has denied already, so it must not wake the wait.
func TestPeerWaitSkipsAskPastItsAskersWait(t *testing.T) {
	dir := t.TempDir()
	address := "codex/lead.lead-1"
	m := bus.Message{N: 1, T: time.Now().Add(-31 * time.Second).UTC().Format(time.RFC3339Nano), From: "duolog/ask.x", To: address, K: "ask", Ref: "peer-test", S: "approval required", W: 30}
	line, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), append(line, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := pendingAsk(dir, address, "peer-test"); err != nil || got != "" {
		t.Fatalf("an ask past its asker's wait is pending: %q %v", got, err)
	}
}

func TestPeerEnvironmentNamesThePeer(t *testing.T) {
	r := newRig(t, baseScript())
	e := r.open("implement")
	if !slices.Contains(r.started(), "DUOLOG_PEER="+e.ID) {
		t.Fatal("peer id missing from pane environment", r.started())
	}
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
	args := r.spawns()
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
		line, err := json.Marshal(bus.Message{N: int64(n), T: sent.UTC().Format(time.RFC3339Nano), From: "duolog/ask.x", To: address, K: "ask", Ref: "peer-test", S: "approval required"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bus.ndjson"), append(line, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(time.Now().Add(-(gate.MaxAskWait+30*time.Second)-time.Second), 1)
	if got, err := pendingAsk(dir, address, "peer-test"); err != nil || got != "" {
		t.Fatal(got, err)
	}
	write(time.Now(), 2)
	if got, err := pendingAsk(dir, address, "peer-test"); err != nil || got == "" {
		t.Fatal(got, err)
	}
}
