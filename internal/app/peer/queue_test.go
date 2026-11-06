package peer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// queued adds an item for role in dir with the rig's task file, or fails.
func (r *rig) queued(role, dir, after string) QueueItem {
	r.t.Helper()
	item, err := QueueAdd(r.ctx, role, dir, "task.md", after)
	if err != nil {
		r.t.Fatal(err)
	}
	return item
}

func (r *rig) queue() []QueueItem {
	r.t.Helper()
	items, err := QueueList(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	return items
}

// openAndSend is the screen sequence of one queued item: two ready samples
// for the open, then a clean send of the rig's task.
func openAndSend() []string {
	return append([]string{codexIdle(""), codexIdle("")}, sendOK("task.md")...)
}

func TestQueueAddChecksCwdAndTask(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	if err := os.Mkdir(filepath.Join(r.root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, "sub", "next.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "task.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, role, cwd, task, want string
	}{
		{"cwd outside the project", "implement", outside, "task.md", "outside the project"},
		{"absolute task", "implement", r.root, filepath.Join(r.root, "task.md"), "relative"},
		{"task with ..", "implement", filepath.Join(r.root, "sub"), "../task.md", "relative"},
		{"missing task", "implement", r.root, "none.md", "none.md"},
		{"task is a folder", "implement", r.root, "sub", "not a file"},
		{"unknown role", "nosuchrole", r.root, "task.md", "nosuchrole"},
		{"unknown after", "implement", r.root, "task.md", "peer-nope"},
	} {
		after := ""
		if tt.name == "unknown after" {
			after = "peer-nope"
		}
		if _, err := QueueAdd(r.ctx, tt.role, tt.cwd, tt.task, after); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
	if items := r.queue(); len(items) != 0 {
		t.Fatalf("a refused add was queued: %+v", items)
	}
	item, err := QueueAdd(r.ctx, "implement", filepath.Join(r.root, "sub"), "next.md", "")
	if err != nil {
		t.Fatal(err)
	}
	items := r.queue()
	if len(items) != 1 || items[0].ID != item.ID || items[0].Role != "implement" || items[0].Task != "next.md" || items[0].StateLine() != "queued" {
		t.Fatalf("%+v", items)
	}
	if !sameWorkRoot(items[0].Cwd, filepath.Join(r.root, "sub")) {
		t.Fatalf("cwd %q", items[0].Cwd)
	}
	if _, err := os.Stat(filepath.Join(r.root, ".duolog", "peer-queue.ndjson")); err != nil {
		t.Fatal(err)
	}
}

// A linked worktree of the project is a place a queued peer may work in.
func TestQueueAddAcceptsLinkedWorktree(t *testing.T) {
	r := newRig(t, baseScript())
	r.git()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", r.root, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("commit", "-q", "--allow-empty", "-m", "start")
	tree := filepath.Join(t.TempDir(), "lane")
	run("worktree", "add", "-q", tree)
	if err := os.WriteFile(filepath.Join(tree, "task.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := QueueAdd(r.ctx, "implement", tree, "task.md", ""); err != nil {
		t.Fatal(err)
	}
}

func TestQueueRemoveDropsItem(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	first, second := r.queued("implement", r.root, ""), r.queued("implement", r.root, "")
	if err := QueueRemove(r.ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("%+v", items)
	}
	if err := QueueRemove(r.ctx, first.ID); err == nil || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("a second remove: %v", err)
	}
}

// Another lead's queue is its own.
func TestQueueIsPerLead(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	r.queued("implement", r.root, "")
	if err := r.reg.RegisterLead(session.Lead{ID: "lead-2", Mode: mode.Ask, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	other := WithOptions(context.Background(), Options{Root: r.root, Config: r.cfg, Lead: "lead-2"})
	if items, err := QueueList(other); err != nil || len(items) != 0 {
		t.Fatalf("%+v %v", items, err)
	}
}

// A run opens the item through peer open, sends its task as peer send does
// and tells the lead on the bus.
func TestQueueRunOpensAndSends(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	item := r.queued("implement", r.root, "")
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	items := r.queue()
	if len(items) != 1 || items[0].State != QueueOpened || !strings.HasPrefix(items[0].Peer, "peer-") {
		t.Fatalf("%+v", items)
	}
	id := items[0].Peer
	if got := items[0].StateLine(); got != "opened "+id {
		t.Fatalf("state %q", got)
	}
	if len(r.spawns()) != 1 {
		t.Fatalf("spawns: %v", r.spawns())
	}
	if entry := r.entry(id); entry.Task != filepath.Join(r.root, "task.md") {
		t.Fatalf("task not recorded on the peer: %+v", entry)
	}
	if tasks := r.messages("task"); len(tasks) != 1 || tasks[0].Ref != id {
		t.Fatalf("task messages: %+v", tasks)
	}
	want := "queue: opened " + id + " (implement) in . for task.md"
	if got := r.messages("queue"); len(got) != 1 || got[0].S != want || got[0].Ref != item.ID || got[0].To != r.messages("task")[0].From {
		t.Fatalf("queue messages: %+v, want %q", got, want)
	}
}

// Admission refuses: the run stops at that item and leaves it and every
// later item queued.
func TestQueueRunStopsWhereAdmissionRefuses(t *testing.T) {
	r := newRig(t, withScreens(codexIdle("")))
	r.cfg.Limits.MaxTotalSessions = 1
	r.ready()
	r.task()
	r.queued("plan", r.root, "")
	r.queued("implement", r.root, "")
	r.rex(withScreens(claudeIdle("")))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.spawns()) != 0 {
		t.Fatalf("a refused item opened a pane: %v", r.spawns())
	}
	for _, item := range r.queue() {
		if item.State != QueueQueued {
			t.Fatalf("%+v", item)
		}
	}
	if got := r.messages("queue"); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

// An open that fails for another reason marks the item failed with the
// reason, and the run goes on with the next one.
func TestQueueRunMarksFailedAndGoesOn(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.lead("codex", mode.Plan)
	r.task()
	r.queued("implement", r.root, "")
	r.queued("review_intermediate", r.root, "")
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	items := r.queue()
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
	if items[0].State != QueueFailed || !strings.Contains(items[0].StateLine(), "failed: mode plan refuses write role implement") {
		t.Fatalf("first: %+v", items[0])
	}
	if items[1].State != QueueOpened {
		t.Fatalf("second: %+v", items[1])
	}
}

// An item after a peer waits while that peer works and opens once it is
// closed; the items behind it do not wait for it.
func TestQueueRunWaitsForAfterPeer(t *testing.T) {
	r := newRig(t, withScreens(codexIdle("")))
	busy := r.ready()
	r.task()
	r.queued("plan", r.root, busy.ID)
	r.rex(withScreens(working()))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].State != QueueQueued || len(r.spawns()) != 0 {
		t.Fatalf("%+v %v", items, r.spawns())
	}
	if _, err := r.reg.Update(busy.ID, func(x *session.Entry) { x.Closed = true }); err != nil {
		t.Fatal(err)
	}
	r.rex(withScreens(claudeIdle(""), claudeIdle(""), claudeIdle(""), claudeIdle(""), claudeIdle(line("task.md")), working()))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].State != QueueOpened {
		t.Fatalf("%+v", items)
	}
}

// A second run exits at once while one holds the lock.
func TestQueueRunExitsWhileAnotherRuns(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	r.queued("implement", r.root, "")
	held := flock.New(queueRunLock(filepath.Join(r.root, ".duolog"), "lead-1"))
	if err := held.Lock(); err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	started := time.Now()
	err := RunQueue(r.ctx)
	if !errors.Is(err, ErrQueueBusy) || time.Since(started) > 2*time.Second {
		t.Fatalf("%v after %s", err, time.Since(started))
	}
	if items := r.queue(); items[0].State != QueueQueued || len(r.spawns()) != 0 {
		t.Fatalf("%+v", items)
	}
}

// An item left opening by a run that died is marked failed, since its peer
// may have opened.
func TestQueueRunFailsItemLeftOpening(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	item := r.queued("implement", r.root, "")
	if err := updateQueue(r.ctx, filepath.Join(r.root, ".duolog"), func(items []QueueItem) ([]QueueItem, error) {
		items[0].State = QueueOpening
		return items, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].ID != item.ID || items[0].State != QueueFailed || !strings.Contains(items[0].Reason, "peer list") {
		t.Fatalf("%+v", items)
	}
}

// StartQueueRun starts a detached run only when the lead has queued items.
func TestStartQueueRunOnlyWithItems(t *testing.T) {
	r := newRig(t, baseScript())
	var started [][]string
	old := startDetached
	startDetached = func(cmd *exec.Cmd) error {
		started = append(started, append([]string{cmd.Dir}, cmd.Args[1:]...))
		return nil
	}
	t.Cleanup(func() { startDetached = old })
	if ok, err := StartQueueRun(r.ctx); err != nil || ok || len(started) != 0 {
		t.Fatalf("empty queue: %v %v %v", ok, err, started)
	}
	r.task()
	r.queued("implement", r.root, "")
	if ok, err := StartQueueRun(r.ctx); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	want := []string{r.root, "peer", "queue", "run", "--lead", "lead-1"}
	if len(started) != 1 || !slices.Equal(started[0], want) {
		t.Fatalf("%q, want %q", started, want)
	}
}

// The detached run is a real process that outlives nothing of the caller:
// it starts on every platform and is not waited for.
func TestStartQueueRunStartsDetachedProcess(t *testing.T) {
	r := newRig(t, baseScript())
	bin := testbin.Install(t, "duolog-queue", testbin.Scenario{})
	old := queueExecutable
	queueExecutable = func() (string, error) { return bin, nil }
	t.Cleanup(func() { queueExecutable = old })
	r.task()
	r.queued("implement", r.root, "")
	if ok, err := StartQueueRun(r.ctx); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(bin + ".calls.ndjson")
		if err == nil && strings.Contains(string(data), `"peer","queue","run","--lead","lead-1"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the detached run did not start: %s %v", data, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
