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

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
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

// symlink makes link point at target, or skips the test where the platform
// lets no test make one.
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
}

// A symlink is followed before the checks: a --cwd that links out of the
// project, or a --task that links out of the cwd's checkout, is refused, and
// one that stays inside is queued with its real folder.
func TestQueueAddFollowsSymlinks(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	outside := t.TempDir()
	for _, name := range []string{"task.md", "secret.md"} {
		if err := os.WriteFile(filepath.Join(outside, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	symlink(t, outside, filepath.Join(r.root, "out"))
	if _, err := QueueAdd(r.ctx, "implement", filepath.Join(r.root, "out"), "task.md", ""); err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Fatalf("a --cwd linked out of the project: %v", err)
	}
	symlink(t, filepath.Join(outside, "secret.md"), filepath.Join(r.root, "secret.md"))
	if _, err := QueueAdd(r.ctx, "implement", r.root, "secret.md", ""); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("a --task linked out of the checkout: %v", err)
	}
	if err := os.Mkdir(filepath.Join(r.root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(r.root, "sub"), filepath.Join(r.root, "alias"))
	symlink(t, filepath.Join(r.root, "task.md"), filepath.Join(r.root, "sub", "up.md"))
	item, err := QueueAdd(r.ctx, "implement", filepath.Join(r.root, "alias"), "up.md", "")
	if err != nil {
		t.Fatal(err)
	}
	if real, _ := filepath.EvalSymlinks(filepath.Join(r.root, "sub")); item.Cwd != real {
		t.Fatalf("cwd %q, want the real folder %q", item.Cwd, real)
	}
	if items := r.queue(); len(items) != 1 {
		t.Fatalf("%+v", items)
	}
}

// A link swapped after the add does not take the run out of the project: the
// run checks the item again and fails it without opening a pane.
func TestQueueRunChecksTheItemAgain(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	if err := os.Mkdir(filepath.Join(r.root, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, "sub", "task.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	r.queued("implement", filepath.Join(r.root, "sub"), "")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.root, "sub", "task.md")); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(outside, "secret.md"), filepath.Join(r.root, "sub", "task.md"))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].State != QueueFailed || !strings.Contains(items[0].Reason, "outside") || len(r.spawns()) != 0 {
		t.Fatalf("%+v %v", items, r.spawns())
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

// An open that fails for another reason, such as a role the config no longer
// has, marks the item failed with the reason and tells the lead, and the run
// goes on with the next one.
func TestQueueRunMarksFailedAndGoesOn(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	gone := r.queued("implement", r.root, "")
	r.queued("review_intermediate", r.root, "")
	delete(r.cfg.Roles, "implement")
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	items := r.queue()
	if len(items) != 2 {
		t.Fatalf("%+v", items)
	}
	if items[0].State != QueueFailed || !strings.Contains(items[0].StateLine(), `failed: unknown role "implement"`) {
		t.Fatalf("first: %+v", items[0])
	}
	if items[1].State != QueueOpened {
		t.Fatalf("second: %+v", items[1])
	}
	if got := r.messages("queue"); len(got) != 2 || got[0].Ref != gone.ID || !strings.Contains(got[0].S, gone.ID+" failed") {
		t.Fatalf("queue messages: %+v", got)
	}
}

// The lead in plan mode, or no agent available to the role, refuse the open
// as admission does: the run stops and every item stays queued.
func TestQueueRunStopsWhereRoutingRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		block func(r *rig)
	}{
		{"plan mode", func(r *rig) { r.lead("codex", mode.Plan) }},
		{"no agent", func(r *rig) {
			for _, agent := range []string{"claude", "codex"} {
				if err := r.reg.MarkUnavailable(agent, time.Now().Add(time.Hour)); err != nil {
					r.t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, withScreens(openAndSend()...))
			r.task()
			r.queued("implement", r.root, "")
			r.queued("review_intermediate", r.root, "")
			tc.block(r)
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
		})
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

// holdRunLock takes the lead's run lock, as a run that goes would, until
// release is called or the test ends.
func (r *rig) holdRunLock() (release func()) {
	r.t.Helper()
	unlock, err := store.LockFile(queueRunLock(filepath.Join(r.root, ".duolog"), "lead-1"))
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = unlock() })
	return func() { _ = unlock() }
}

// setState puts the item with id in state, as a run would.
func (r *rig) setState(id string, state QueueState) {
	r.t.Helper()
	if err := updateQueue(r.ctx, filepath.Join(r.root, ".duolog"), func(items []QueueItem) ([]QueueItem, error) {
		for i := range items {
			if items[i].ID == id {
				items[i].State = state
			}
		}
		return items, nil
	}); err != nil {
		r.t.Fatal(err)
	}
}

// An item a run is opening stays while the run goes; once no run holds the
// lock, it was left by a run that ended, and remove drops it.
func TestQueueRemoveDropsItemLeftOpening(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	item := r.queued("implement", r.root, "")
	r.setState(item.ID, QueueOpening)
	release := r.holdRunLock()
	if err := QueueRemove(r.ctx, item.ID); err == nil || !strings.Contains(err.Error(), "being opened") {
		t.Fatalf("remove while a run goes: %v", err)
	}
	release()
	if err := QueueRemove(r.ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); len(items) != 0 {
		t.Fatalf("%+v", items)
	}
}

// An item removed after the run picked it is not opened: the run finds it
// gone under the queue lock and skips it.
func TestQueueRunSkipsItemRemovedAfterPick(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	item := r.queued("implement", r.root, "")
	e, err := setup(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := QueueRemove(r.ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if stop, err := e.openItem(r.ctx, item); err != nil || stop {
		t.Fatalf("stop %v: %v", stop, err)
	}
	if len(r.spawns()) != 0 || len(r.queue()) != 0 || len(r.messages("queue")) != 0 {
		t.Fatalf("a removed item opened: %v %+v", r.spawns(), r.queue())
	}
}

// A run truncates the run log once it holds the lock: a run that only found
// another one going appends to the log and leaves the going run's lines.
func TestQueueRunLog(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	r.queued("implement", r.root, "")
	r.useDeps(func(d *Deps) { d.StartDetached = func([]string, string, string) error { return nil } })
	log := filepath.Join(r.root, ".duolog", queueRunLog)
	if err := os.WriteFile(log, []byte("the going run\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := StartQueueRun(r.ctx, nil); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if data, _ := os.ReadFile(log); string(data) != "the going run\n" {
		t.Fatalf("a start emptied the log of the going run: %q", data)
	}
	release := r.holdRunLock()
	if err := RunQueue(r.ctx); !errors.Is(err, ErrQueueBusy) {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); string(data) != "the going run\n" {
		t.Fatalf("a busy run emptied the log: %q", data)
	}
	release()
	r.rex(withScreens(openAndSend()...))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log); len(data) != 0 {
		t.Fatalf("the run kept the last run's log: %q", data)
	}
}

// A second run exits at once while one holds the lock.
func TestQueueRunExitsWhileAnotherRuns(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.task()
	r.queued("implement", r.root, "")
	r.holdRunLock()
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
// may have opened, and the lead is told on the bus.
func TestQueueRunFailsItemLeftOpening(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	item := r.queued("implement", r.root, "")
	r.setState(item.ID, QueueOpening)
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].ID != item.ID || items[0].State != QueueFailed || !strings.Contains(items[0].Reason, "peer list") {
		t.Fatalf("%+v", items)
	}
	if got := r.messages("queue"); len(got) != 1 || got[0].Ref != item.ID || !strings.Contains(got[0].S, item.ID+" failed") {
		t.Fatalf("queue messages: %+v", got)
	}
}

// A queue run never opens a paid peer: where only the paid variant would be
// admitted, the item stays queued and nothing is reserved.
func TestQueueRunOpensTheFreeVariantOnly(t *testing.T) {
	r := newRig(t, withScreens(openAndSend()...))
	r.paidCodex()
	r.task()
	r.queued("implement", r.root, "")
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].State != QueueQueued || len(r.spawns()) != 0 {
		t.Fatalf("%+v %v", items, r.spawns())
	}
	r.wantHeld(0, 0)
}

// remove takes the run lock only for an item left opening: removing a queued
// item leaves the lock to a run that starts meanwhile.
func TestQueueRemoveLeavesTheRunLockFree(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	item := r.queued("implement", r.root, "")
	dir := filepath.Join(r.root, ".duolog")
	unlock, err := store.LockFile(filepath.Join(dir, queueLock))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- QueueRemove(r.ctx, item.ID) }()
	// The remove now waits for the queue lock; the run lock stays free.
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		release, free, err := store.TryLockFile(queueRunLock(dir, "lead-1"))
		if err != nil || !free {
			_ = unlock()
			t.Fatalf("the remove of a queued item holds the run lock: %v", err)
		}
		_ = release()
	}
	_ = unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); len(items) != 0 {
		t.Fatalf("%+v", items)
	}
}

// An item cannot be queued outside a Rex session, where no run could open it.
func TestQueueAddNeedsRex(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	t.Setenv("REX_SESSION", "")
	if _, err := QueueAdd(r.ctx, "implement", r.root, "task.md", ""); err == nil || !strings.Contains(err.Error(), "REX_SESSION is not set") {
		t.Fatalf("add outside Rex: %v", err)
	}
	if items := r.queue(); len(items) != 0 {
		t.Fatalf("%+v", items)
	}
}

// detachedRuns records the runs StartQueueRun starts instead of starting them.
func (r *rig) detachedRuns() *[][]string {
	var started [][]string
	r.useDeps(func(d *Deps) {
		d.Executable = func() (string, error) { return "duolog", nil }
		d.StartDetached = func(argv []string, dir, log string) error {
			started = append(started, append([]string{dir, log}, argv...))
			return nil
		}
	})
	return &started
}

// A hook starts a run only while the load line shows a free session for a
// queued item's agent: the agent its role names.
func TestStartQueueRunNeedsAFreeSession(t *testing.T) {
	r := newRig(t, baseScript())
	started := r.detachedRuns()
	r.task()
	r.queued("implement", r.root, "")
	agent := r.cfg.Roles["implement"].Use.Agent
	var asked []string
	none := func(a string) bool { asked = append(asked, a); return false }
	if ok, err := StartQueueRun(r.ctx, none); err != nil || ok || len(*started) != 0 {
		t.Fatalf("no free session: %v %v %v", ok, err, *started)
	}
	if !slices.Equal(asked, []string{agent}) {
		t.Fatalf("asked for %q, want the role's agent %q", asked, agent)
	}
	if ok, err := StartQueueRun(r.ctx, func(a string) bool { return a == agent }); err != nil || !ok || len(*started) != 1 {
		t.Fatalf("a free %s session: %v %v %v", agent, ok, err, *started)
	}
}

// An item left opening by a run that ended starts a run without a free
// session, so that run fails it and tells the lead; not while a run holds the
// lock, which may be opening it.
func TestStartQueueRunForItemLeftOpening(t *testing.T) {
	r := newRig(t, baseScript())
	started := r.detachedRuns()
	r.task()
	item := r.queued("implement", r.root, "")
	r.setState(item.ID, QueueOpening)
	none := func(string) bool { return false }
	release := r.holdRunLock()
	if ok, err := StartQueueRun(r.ctx, none); err != nil || ok {
		t.Fatalf("a run holds the lock: %v %v", ok, err)
	}
	release()
	if ok, err := StartQueueRun(r.ctx, none); err != nil || !ok || len(*started) != 1 {
		t.Fatalf("an item left opening: %v %v %v", ok, err, *started)
	}
}

// StartQueueRun starts a detached run only when the lead has queued items.
func TestStartQueueRunOnlyWithItems(t *testing.T) {
	r := newRig(t, baseScript())
	var started [][]string
	r.useDeps(func(d *Deps) {
		d.Executable = func() (string, error) { return "duolog", nil }
		d.StartDetached = func(argv []string, dir, log string) error {
			started = append(started, append([]string{dir, log}, argv...))
			return nil
		}
	})
	if ok, err := StartQueueRun(r.ctx, nil); err != nil || ok || len(started) != 0 {
		t.Fatalf("empty queue: %v %v %v", ok, err, started)
	}
	r.task()
	r.queued("implement", r.root, "")
	if ok, err := StartQueueRun(r.ctx, nil); err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	want := []string{r.root, filepath.Join(r.root, ".duolog", queueRunLog), "duolog", "peer", "queue", "run", "--lead", "lead-1"}
	if len(started) != 1 || !slices.Equal(started[0], want) {
		t.Fatalf("%q, want %q", started, want)
	}
}

// The detached run is a real process that outlives nothing of the caller:
// it starts on every platform and is not waited for.
func TestStartQueueRunStartsDetachedProcess(t *testing.T) {
	r := newRig(t, baseScript())
	bin := testbin.Install(t, "duolog-queue", testbin.Scenario{})
	r.useDeps(func(d *Deps) {
		d.Executable = func() (string, error) { return bin, nil }
		d.StartDetached = runner.StartDetached
	})
	r.task()
	r.queued("implement", r.root, "")
	if ok, err := StartQueueRun(r.ctx, nil); err != nil || !ok {
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
