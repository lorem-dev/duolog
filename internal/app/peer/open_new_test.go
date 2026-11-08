package peer

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
)

// OpenNew starts a second pane for a role that has a live peer in the
// checkout, where Open would return that peer.
func TestOpenNewStartsASecondPeer(t *testing.T) {
	r := newRig(t, baseScript())
	r.cfg.Limits.MaxLiveSessions = 2
	first := r.ready()
	script := baseScript()
	script["open-tab"] = peertest.Reply{Block: "block:second"}
	r.panes(script)
	second, err := OpenNew(r.ctx, "implement")
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.State != Ready {
		t.Fatalf("%+v %+v", first, second)
	}
	if again := r.open("implement"); again.ID != first.ID && again.ID != second.ID {
		t.Fatalf("a plain open started a third peer: %+v", again)
	}
}

// OpenNew keeps the session limits: with the limit reached it is refused and
// the live peer stays.
func TestOpenNewRespectsTheSessionLimit(t *testing.T) {
	r := newRig(t, baseScript())
	first := r.ready()
	_, err := OpenNew(r.ctx, "implement")
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), first.ID) {
		t.Fatalf("%v", err)
	}
	if n := len(r.spawns()); n != 1 || r.entry(first.ID).Closed {
		t.Fatalf("%d splits; %+v", n, r.entry(first.ID))
	}
}

// A run lock whose recorded holder is gone does not stop a new run, even
// when something else still holds the old file.
func TestQueueRunIgnoresADeadRunsLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows removes no file another handle holds open, and Go's handles there are not inherited, so a held lock has a live holder")
	}
	r := newRig(t, baseScript())
	r.task()
	r.queued("implement", r.root, "")
	r.holdRunLock()
	dead := exec.Command(os.Args[0], "-test.run=^$")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	lock := queueRunLock(r.root+"/.duolog", "lead-1")
	if err := os.WriteFile(runLockPid(lock), []byte(strconv.Itoa(dead.ProcessState.Pid())), 0o600); err != nil {
		t.Fatal(err)
	}
	r.panes(withScreens(openAndSend()...))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatalf("a dead run's lock blocked the run: %v", err)
	}
	if items := r.queue(); items[0].State != QueueOpened {
		t.Fatalf("%+v", items)
	}
}

// An item waits for a peer that works and starts as soon as that peer is
// idle, without the peer being closed.
func TestQueueRunStartsWhenAfterPeerIsIdle(t *testing.T) {
	r := newRig(t, withScreens(codexIdle("")))
	idle := r.ready()
	r.task()
	r.queued("plan", r.root, idle.ID)
	r.panes(withScreens(codexIdle(""), claudeIdle(""), claudeIdle(""), claudeIdle(""), claudeIdle(""), claudeIdle(line("task.md")), working()))
	if err := RunQueue(r.ctx); err != nil {
		t.Fatal(err)
	}
	if items := r.queue(); items[0].State != QueueOpened {
		t.Fatalf("%+v", items)
	}
}

// A lock recorded for a live process still blocks.
func TestQueueRunKeepsALiveRunsLock(t *testing.T) {
	r := newRig(t, baseScript())
	r.task()
	r.queued("implement", r.root, "")
	r.holdRunLock()
	if err := os.WriteFile(runLockPid(queueRunLock(r.root+"/.duolog", "lead-1")), []byte(strconv.Itoa(os.Getppid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunQueue(r.ctx); !errors.Is(err, ErrQueueBusy) {
		t.Fatal(err)
	}
}

// A run records its pid next to the lock, not in it, and removes it before
// it lets the lock go.
func TestRunLockHolderPidSitsBesideTheLock(t *testing.T) {
	lock := queueRunLock(t.TempDir(), "lead-1")
	unlocked := false
	release := recordRunLockHolder(lock, func() error {
		if _, err := os.Stat(runLockPid(lock)); !os.IsNotExist(err) {
			t.Errorf("the pid outlived the lock: %v", err)
		}
		unlocked = true
		return nil
	})
	data, err := os.ReadFile(runLockPid(lock))
	if err != nil || string(data) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("%q %v", data, err)
	}
	release()
	if !unlocked {
		t.Fatal("release kept the lock")
	}
}
