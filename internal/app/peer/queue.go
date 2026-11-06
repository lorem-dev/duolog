package peer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/bus"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/rex"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// QueueState is where a queued item stands.
type QueueState string

const (
	QueueQueued  QueueState = "queued"
	QueueOpening QueueState = "opening"
	QueueOpened  QueueState = "opened"
	QueueFailed  QueueState = "failed"
)

// QueueItem is one peer a lead queued: a role to open in a folder and the
// task file to send it. Task is relative to Cwd, which is absolute.
type QueueItem struct {
	ID    string     `json:"id"`
	Lead  string     `json:"lead"`
	Role  string     `json:"role"`
	Cwd   string     `json:"cwd"`
	Task  string     `json:"task"`
	After string     `json:"after,omitempty"`
	State QueueState `json:"state"`
	// Peer is the peer the item opened, set once it opened.
	Peer string `json:"peer,omitempty"`
	// Reason says why the item failed.
	Reason string    `json:"reason,omitempty"`
	Added  time.Time `json:"added"`
}

// StateLine is the state as list prints it: queued, opening, opened
// peer-<id> or failed: <reason>.
func (q QueueItem) StateLine() string {
	switch q.State {
	case QueueOpened:
		return "opened " + q.Peer
	case QueueFailed:
		return "failed: " + q.Reason
	}
	return string(q.State)
}

// ErrQueueBusy means another queue run of the lead holds the lock; this one
// did nothing.
var ErrQueueBusy = errors.New("another queue run is going")

const (
	queueFile = "peer-queue.ndjson"
	queueLock = "peer-queue.lock"
	// queueLockWait bounds how long a change to the queue file waits for
	// another one.
	queueLockWait = 10 * time.Second
	// queueRunWait is how long a run tries the run lock before it leaves the
	// work to the run that holds it.
	queueRunWait = 50 * time.Millisecond
)

// queueRunLock is the lock of the lead's queue runs, one file per lead in
// the state folder.
func queueRunLock(dir, lead string) string {
	safe := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, lead)
	return filepath.Join(dir, "peer-queue-run-"+safe+".lock")
}

// setupQueue is the setup of the queue commands that open nothing: add, list,
// remove and the start of a detached run. They need no Rex.
func setupQueue(ctx context.Context) (*env, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return nil, err
	}
	return openRecords(opts, true)
}

// readQueue reads every lead's items, in the order they were added.
func readQueue(dir string) ([]QueueItem, error) {
	data, err := store.ReadFile(filepath.Join(dir, queueFile))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []QueueItem
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var item QueueItem
		if err := json.Unmarshal(scan.Bytes(), &item); err != nil {
			return nil, fmt.Errorf("%s: %w", queueFile, err)
		}
		items = append(items, item)
	}
	return items, scan.Err()
}

// updateQueue changes every lead's items under the queue lock and writes them
// back in one rename.
func updateQueue(ctx context.Context, dir string, change func([]QueueItem) ([]QueueItem, error)) error {
	lock := flock.New(filepath.Join(dir, queueLock))
	wait, cancel := context.WithTimeout(ctx, queueLockWait)
	defer cancel()
	if err := store.Lock(wait, lock); err != nil {
		return fmt.Errorf("peer queue lock: %w", err)
	}
	defer lock.Close()
	items, err := readQueue(dir)
	if err != nil {
		return err
	}
	if items, err = change(items); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, item := range items {
		line, err := json.Marshal(item)
		if err != nil {
			return err
		}
		buf.Write(append(line, '\n'))
	}
	tmp, err := os.CreateTemp(dir, queueFile+".*.tmp")
	if err != nil {
		return err
	}
	_, err = tmp.Write(buf.Bytes())
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = store.Rename(tmp.Name(), filepath.Join(dir, queueFile))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// setItem changes one item of the queue by its id.
func (e *env) setItem(ctx context.Context, id string, change func(*QueueItem)) error {
	return updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		for i := range items {
			if items[i].ID == id {
				change(&items[i])
			}
		}
		return items, nil
	})
}

// leadItems keeps the items of the env's lead.
func (e *env) leadItems() ([]QueueItem, error) {
	all, err := readQueue(e.dir)
	if err != nil {
		return nil, err
	}
	var mine []QueueItem
	for _, item := range all {
		if item.Lead == e.leadID {
			mine = append(mine, item)
		}
	}
	return mine, nil
}

// QueueAdd queues a peer of role to open in cwd and be sent task, a path
// relative to cwd. cwd must be inside the project or one of its linked
// worktrees, and task must be a file there. With after, the item waits until
// that peer is idle or closed.
func QueueAdd(ctx context.Context, role, cwd, taskPath, after string) (QueueItem, error) {
	e, err := setupQueue(ctx)
	if err != nil {
		return QueueItem{}, err
	}
	if _, ok := e.cfg.Roles[role]; !ok {
		return QueueItem{}, fmt.Errorf("unknown role %q", role)
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return QueueItem{}, err
	}
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return QueueItem{}, fmt.Errorf("--cwd %s is not a folder", cwd)
	}
	if !insideProject(filepath.Dir(e.dir), cwd) {
		return QueueItem{}, fmt.Errorf("--cwd %s is outside the project and its linked worktrees", cwd)
	}
	native := filepath.FromSlash(taskPath)
	if !filepath.IsLocal(native) || slices.Contains(strings.Split(filepath.ToSlash(native), "/"), "..") {
		return QueueItem{}, fmt.Errorf("--task %s must be a path relative to --cwd, with no .. in it", taskPath)
	}
	info, err := os.Stat(filepath.Join(cwd, native))
	if err != nil {
		return QueueItem{}, fmt.Errorf("--task %s: no such file in %s", taskPath, cwd)
	}
	if !info.Mode().IsRegular() {
		return QueueItem{}, fmt.Errorf("--task %s is not a file", taskPath)
	}
	if after != "" {
		entry, err := e.find(after)
		if err != nil || entry.Lead != e.leadID {
			return QueueItem{}, fmt.Errorf("--after %s: no such peer of this lead", after)
		}
		after = entry.ID
	}
	item := QueueItem{ID: "q-" + strings.ToLower(rand.Text()[:6]), Lead: e.leadID, Role: role, Cwd: cwd, Task: filepath.ToSlash(native),
		After: after, State: QueueQueued, Added: time.Now().UTC()}
	err = updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) { return append(items, item), nil })
	return item, err
}

// QueueList returns the lead's items in order.
func QueueList(ctx context.Context) ([]QueueItem, error) {
	e, err := setupQueue(ctx)
	if err != nil {
		return nil, err
	}
	return e.leadItems()
}

// QueueRemove drops one of the lead's items. An item a run is opening stays.
func QueueRemove(ctx context.Context, id string) error {
	e, err := setupQueue(ctx)
	if err != nil {
		return err
	}
	return updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		at := slices.IndexFunc(items, func(q QueueItem) bool { return q.ID == id && q.Lead == e.leadID })
		if at < 0 {
			return nil, fmt.Errorf("no queued item %s", id)
		}
		if items[at].State == QueueOpening {
			return nil, fmt.Errorf("item %s is being opened; close its peer once it is open", id)
		}
		return slices.Delete(items, at, at+1), nil
	})
}

// QueueRelative names a queued folder as the lead reads it: relative to the
// project when inside it, absolute otherwise, with forward slashes.
func QueueRelative(root, dir string) string {
	if rel, err := filepath.Rel(root, dir); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(dir)
}

// RunQueue opens the lead's queued items in order while admission allows, as
// peer open does, and sends each its task as peer send does. It stops at the
// first item admission refuses and leaves it queued; an item whose open or
// send fails for another reason is marked failed and the run goes on. An item
// after a peer that still works is passed over. Only one run of a lead goes
// at a time: a second one returns ErrQueueBusy at once.
func RunQueue(ctx context.Context) error {
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	lock := flock.New(queueRunLock(e.dir, e.leadID))
	try, cancel := context.WithTimeout(ctx, queueRunWait)
	err = store.Lock(try, lock)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrQueueBusy
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	// An item still opening was left by a run that ended in the middle: its
	// peer may be open.
	if err := updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		for i := range items {
			if items[i].Lead == e.leadID && items[i].State == QueueOpening {
				items[i].State, items[i].Reason = QueueFailed, "the run that opened it ended first; see duolog peer list"
			}
		}
		return items, nil
	}); err != nil {
		return err
	}
	passed := map[string]bool{}
	for {
		item, ok, err := e.nextItem(ctx, passed)
		if err != nil || !ok {
			return err
		}
		stop, err := e.openItem(ctx, item)
		if err != nil || stop {
			return err
		}
	}
}

// nextItem is the first queued item that may open now. An item after a peer
// that still works is passed over for the rest of the run.
func (e *env) nextItem(ctx context.Context, passed map[string]bool) (QueueItem, bool, error) {
	items, err := e.leadItems()
	if err != nil {
		return QueueItem{}, false, err
	}
	for _, item := range items {
		if item.State != QueueQueued || passed[item.ID] {
			continue
		}
		if item.After != "" {
			done, err := e.peerDone(ctx, item.After)
			if err != nil {
				return QueueItem{}, false, err
			}
			if !done {
				passed[item.ID] = true
				continue
			}
		}
		return item, true, nil
	}
	return QueueItem{}, false, nil
}

// peerDone reports whether a peer is closed or idle: it finished its turn and
// shows an empty prompt. It reads the pane and presses nothing.
func (e *env) peerDone(ctx context.Context, id string) (bool, error) {
	all, err := e.reg.Entries()
	if err != nil {
		return false, err
	}
	// A peer that peer close removed is no longer recorded.
	at := slices.IndexFunc(all, func(x session.Entry) bool { return x.ID == id && x.Block != "" })
	if at < 0 || all[at].Closed {
		return true, nil
	}
	entry := all[at]
	presence, err := e.clientFor(entry).Probe(ctx, entry.Block)
	if err != nil {
		return false, err
	}
	if presence != rex.Running {
		return true, nil
	}
	screen, err := e.screen(ctx, entry)
	if rex.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if screen.State != Ready || screen.Working || !screen.Empty {
		return false, nil
	}
	return entry.Task == "" || time.Since(e.sentAt(entry.ID)) >= workGrace, nil
}

// openItem opens one item and sends its task. stop is set when admission
// refused it: the item stays queued and the run ends.
func (e *env) openItem(ctx context.Context, item QueueItem) (stop bool, err error) {
	if err := e.setItem(ctx, item.ID, func(q *QueueItem) { q.State = QueueOpening }); err != nil {
		return true, err
	}
	opts := e.opts
	opts.WorkDir = item.Cwd
	at := WithOptions(ctx, opts)
	where := fmt.Sprintf("(%s) in %s for %s", item.Role, QueueRelative(filepath.Dir(e.dir), item.Cwd), item.Task)
	opened, err := Open(at, item.Role)
	switch {
	case errors.Is(err, ErrRefused), err == nil && opened.ID == "":
		// Refused, or placed in the lead: nothing opened, and it may later.
		return true, e.setItem(ctx, item.ID, func(q *QueueItem) { q.State = QueueQueued })
	case err != nil:
		return false, e.fail(ctx, item, "", oneLine(err.Error()))
	case opened.State != Ready:
		why := string(opened.State)
		if detail := opened.Question + opened.Note; detail != "" {
			why += ": " + detail
		}
		reason := fmt.Sprintf("opened %s but it is %s; the task was not sent", opened.ID, oneLine(why))
		return false, e.fail(ctx, item, opened.ID, reason)
	}
	err = Send(at, opened.ID, filepath.Join(item.Cwd, filepath.FromSlash(item.Task)))
	var here *RunHereError
	if errors.As(err, &here) {
		err = errors.New(here.Line)
	}
	if err != nil {
		return false, e.fail(ctx, item, opened.ID, fmt.Sprintf("opened %s; the task was not sent: %s", opened.ID, oneLine(err.Error())))
	}
	if err := e.setItem(ctx, item.ID, func(q *QueueItem) { q.State, q.Peer = QueueOpened, opened.ID }); err != nil {
		return false, err
	}
	return false, e.tellLead(item, "queue: opened "+opened.ID+" "+where)
}

// fail marks an item failed and tells the lead when a peer opened for it.
func (e *env) fail(ctx context.Context, item QueueItem, peerID, reason string) error {
	err := e.setItem(ctx, item.ID, func(q *QueueItem) { q.State, q.Peer, q.Reason = QueueFailed, peerID, reason })
	return errors.Join(err, e.tellLead(item, "queue: "+item.ID+" failed: "+reason))
}

// tellLead posts one queue line to the lead's bus.
func (e *env) tellLead(item QueueItem, text string) error {
	_, err := e.bus.Send(bus.Message{From: "duolog/queue", To: e.address(e.leadID), K: "queue", Ref: item.ID, S: corescreen.Cut(text, e.cfg.Wire.MaxSummaryBytes)})
	return err
}

// queueExecutable is the duolog that a detached run starts; tests replace it.
var queueExecutable = os.Executable

// startDetached starts a run that the caller does not wait for; tests
// replace it. The child is reaped when it ends, in a caller that lives on.
var startDetached = func(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// StartQueueRun starts duolog peer queue run for the lead as a detached
// process when the lead has queued items, and reports whether it did. The run
// takes the queue's run lock itself, so a run already going makes the new one
// exit at once. It writes its output to peer-queue-run.log in the state
// folder. peer close calls it; the lead's prompt hook and a peer's idle event
// call it too.
func StartQueueRun(ctx context.Context) (bool, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return false, err
	}
	// An empty queue needs no lead, as for a close outside one.
	e, err := openRecords(opts, false)
	if err != nil {
		return false, err
	}
	queued := func(q QueueItem) bool { return q.State == QueueQueued }
	if all, err := readQueue(e.dir); err != nil || !slices.ContainsFunc(all, queued) {
		return false, err
	}
	lead, err := e.reg.ResolveLead(opts.Lead)
	if err != nil {
		return false, err
	}
	e.leadID = lead.ID
	items, err := e.leadItems()
	if err != nil || !slices.ContainsFunc(items, queued) {
		return false, err
	}
	exe, err := queueExecutable()
	if err != nil {
		return false, err
	}
	logFile, err := os.OpenFile(filepath.Join(e.dir, "peer-queue-run.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	defer logFile.Close()
	cmd := exec.Command(exe, "peer", "queue", "run", "--lead", e.leadID)
	cmd.Dir = filepath.Dir(e.dir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	runner.Detach(cmd)
	if err := startDetached(cmd); err != nil {
		return false, err
	}
	return true, nil
}

// insideProject reports whether dir is the project root, one of its linked
// worktrees, or a folder in one. Aliases of a folder, such as a symlinked
// temporary folder or another letter case, count as the folder.
func insideProject(root, dir string) bool {
	var places []os.FileInfo
	for _, p := range append([]string{root}, linkedWorktrees(root)...) {
		if info, err := os.Stat(p); err == nil {
			places = append(places, info)
		}
	}
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if info, err := os.Stat(d); err == nil && slices.ContainsFunc(places, func(p os.FileInfo) bool { return os.SameFile(p, info) }) {
			return true
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}

// linkedWorktrees lists the checkouts git worktree list names for the project,
// none outside Git.
func linkedWorktrees(root string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "list", "--porcelain")
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var trees []string
	for line := range strings.Lines(string(out)) {
		if path, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "worktree "); ok {
			trees = append(trees, filepath.FromSlash(path))
		}
	}
	return trees
}
