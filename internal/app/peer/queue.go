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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
	corepane "github.com/lorem-dev/duolog/internal/core/pane"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
	"github.com/lorem-dev/duolog/internal/project"
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
	// queueRunLog is the output of the detached runs, in the state folder.
	queueRunLog = "peer-queue-run.log"
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

// dropStaleRunLock removes a run lock whose recorded holder is gone. The
// kernel frees a dead process's lock, but a child that inherited the file
// keeps it held, and then every new run would exit as busy. A new file has a
// new lock. A lock with no pid, or with a live holder, stays.
func dropStaleRunLock(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 || pid == os.Getpid() || session.ProcessAlive(pid) {
		return
	}
	_ = os.Remove(path)
}

// setupQueue is the setup of the queue commands that open nothing: add, list,
// remove and the start of a detached run. They need no Rex client; add
// refuses outside a Rex session itself.
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
	wait, cancel := context.WithTimeout(ctx, queueLockWait)
	defer cancel()
	unlock, err := store.LockFileContext(wait, filepath.Join(dir, queueLock), 10*time.Millisecond)
	if err != nil {
		return fmt.Errorf("peer queue lock: %w", err)
	}
	defer func() { _ = unlock() }()
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
// that peer is idle or closed. The lead must run in Rex, where the run opens
// the item: an item queued outside it would never open.
func QueueAdd(ctx context.Context, role, cwd, taskPath, after string) (QueueItem, error) {
	e, err := setupQueue(ctx)
	if err != nil {
		return QueueItem{}, err
	}
	if os.Getenv("REX_SESSION") == "" {
		return QueueItem{}, errors.New("queued peers open in Rex: REX_SESSION is not set; run duolog peer queue add in a Rex pane")
	}
	if _, ok := e.cfg.Roles[role]; !ok {
		return QueueItem{}, fmt.Errorf("unknown role %q", role)
	}
	if cwd, err = checkPlace(filepath.Dir(e.dir), cwd, taskPath); err != nil {
		return QueueItem{}, err
	}
	if after != "" {
		entry, err := e.find(after)
		if err != nil || entry.Lead != e.leadID {
			return QueueItem{}, fmt.Errorf("--after %s: no such peer of this lead", after)
		}
		after = entry.ID
	}
	item := QueueItem{ID: "q-" + strings.ToLower(rand.Text()[:6]), Lead: e.leadID, Role: role, Cwd: cwd, Task: filepath.ToSlash(filepath.FromSlash(taskPath)),
		After: after, State: QueueQueued, Added: time.Now().UTC()}
	err = updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) { return append(items, item), nil })
	return item, err
}

// checkPlace resolves cwd and its task file, symlinks followed, and checks
// both: cwd is a folder inside the project at root or one of its linked
// worktrees, and taskPath, relative to cwd with no .. in it, is a file inside
// cwd's checkout. It returns the real cwd. The add checks an item with it,
// and the run again before it opens one, since a link may change between.
func checkPlace(root, cwd, taskPath string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("--cwd %s is not a folder", abs)
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return "", fmt.Errorf("--cwd %s is not a folder", abs)
	}
	if !insideProject(root, real) {
		return "", fmt.Errorf("--cwd %s is outside the project and its linked worktrees", abs)
	}
	native := filepath.FromSlash(taskPath)
	if !filepath.IsLocal(native) || slices.Contains(strings.Split(filepath.ToSlash(native), "/"), "..") {
		return "", fmt.Errorf("--task %s must be a path relative to --cwd, with no .. in it", taskPath)
	}
	task := filepath.Join(real, native)
	info, err := os.Stat(task)
	if err != nil {
		return "", fmt.Errorf("--task %s: no such file in %s", taskPath, abs)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("--task %s is not a file", taskPath)
	}
	resolved, err := filepath.EvalSymlinks(task)
	if err != nil {
		return "", fmt.Errorf("--task %s: %w", taskPath, err)
	}
	if checkout := project.WorkRoot(real, root); !under(checkout, resolved) {
		return "", fmt.Errorf("--task %s leads outside the checkout %s", taskPath, checkout)
	}
	return real, nil
}

// under reports whether path is dir or lies in it. Aliases of dir, such as a
// symlinked temporary folder or another letter case, count as dir.
func under(dir, path string) bool {
	want, err := os.Stat(dir)
	if err != nil {
		return false
	}
	for d := filepath.Clean(path); ; d = filepath.Dir(d) {
		if info, err := os.Stat(d); err == nil && os.SameFile(want, info) {
			return true
		}
		if d == filepath.Dir(d) {
			return false
		}
	}
}

// QueueList returns the lead's items in order.
func QueueList(ctx context.Context) ([]QueueItem, error) {
	e, err := setupQueue(ctx)
	if err != nil {
		return nil, err
	}
	return e.leadItems()
}

// QueueRemove drops one of the lead's items. An item a run is opening stays
// while that run goes; one left opening by a run that ended, when no run
// holds the lead's run lock, goes too. Only an opening item takes the run
// lock, so the remove of another item never makes a run that starts
// meanwhile exit as busy.
func QueueRemove(ctx context.Context, id string) error {
	e, err := setupQueue(ctx)
	if err != nil {
		return err
	}
	items, err := e.leadItems()
	if err != nil {
		return err
	}
	held := false
	if slices.ContainsFunc(items, func(q QueueItem) bool { return q.ID == id && q.State == QueueOpening }) {
		// Holding the run lock keeps a run from starting while the item goes.
		dropStaleRunLock(queueRunLock(e.dir, e.leadID))
		unlock, free, err := store.TryLockFile(queueRunLock(e.dir, e.leadID))
		if err != nil {
			return err
		}
		if free {
			defer func() { _ = unlock() }()
		}
		held = free
	}
	return updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		at := slices.IndexFunc(items, func(q QueueItem) bool { return q.ID == id && q.Lead == e.leadID })
		if at < 0 {
			return nil, fmt.Errorf("no queued item %s", id)
		}
		// An item a run claimed since the read is being opened too.
		if items[at].State == QueueOpening && !held {
			return nil, fmt.Errorf("item %s is being opened; close its peer once it is open", id)
		}
		return slices.Delete(items, at, at+1), nil
	})
}

// QueueRelative names a queued folder as the lead reads it: relative to the
// project when inside it, absolute otherwise, with forward slashes. The
// project's real path counts as the project, since an item keeps its real
// folder.
func QueueRelative(root, dir string) string {
	roots := []string{root}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != root {
		roots = append(roots, real)
	}
	for _, r := range roots {
		if rel, err := filepath.Rel(r, dir); err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(dir)
}

// RunQueue opens the lead's queued items in order while admission allows, as
// peer open does but on the free variant only, and sends each its task as
// peer send does. It stops at the
// first item admission refuses and leaves it queued; an item whose open or
// send fails for another reason is marked failed, the lead is told, and the
// run goes on. An item after a peer that still works is passed over. Only one
// run of a lead goes at a time: a second one returns ErrQueueBusy at once.
// A run that holds the lock empties the run log, which the runs started
// detached write to.
func RunQueue(ctx context.Context) error {
	e, err := setup(ctx)
	if err != nil {
		return err
	}
	lock := queueRunLock(e.dir, e.leadID)
	dropStaleRunLock(lock)
	try, cancel := context.WithTimeout(ctx, queueRunWait)
	unlock, err := store.LockFileContext(try, lock, 10*time.Millisecond)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, store.ErrLockUnavailable) {
		return ErrQueueBusy
	}
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	// Best effort: the pid lets the next run tell a dead holder from a live one.
	_ = os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600)
	if err := os.Truncate(filepath.Join(e.dir, queueRunLog), 0); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := e.failLeftOpening(ctx); err != nil {
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

// failLeftOpening marks failed the lead's items still opening, which a run
// that ended in the middle left: their peer may be open, so the lead hears of
// each. The caller holds the run lock.
func (e *env) failLeftOpening(ctx context.Context) error {
	const left = "the run that opened it ended first; see duolog peer list"
	var ended []QueueItem
	if err := updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		for i := range items {
			if items[i].Lead == e.leadID && items[i].State == QueueOpening {
				items[i].State, items[i].Reason = QueueFailed, left
				ended = append(ended, items[i])
			}
		}
		return items, nil
	}); err != nil {
		return err
	}
	for _, item := range ended {
		if err := e.tellLead(item, "queue: "+item.ID+" failed: "+left); err != nil {
			return err
		}
	}
	return nil
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
	presence, err := e.panes.Probe(ctx, e.paneOf(entry))
	if err != nil {
		return false, err
	}
	if presence != corepane.Running {
		return true, nil
	}
	screen, err := e.screen(ctx, entry)
	if e.panes.Gone(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if screen.State != Ready || screen.Working || !screen.Empty {
		return false, nil
	}
	return entry.Task == "" || e.opts.Deps.Clock.Now().Sub(e.sentAt(entry.ID)) >= e.opts.Deps.Pauses.WorkGrace, nil
}

// openItem opens one item and sends its task. stop is set when admission
// refused it: the item stays queued and the run ends. An item that is no
// longer queued when the run claims it, under the queue lock, such as one
// removed since the run picked it, is skipped. An item whose folder or task
// now leads out of the project fails without an open.
func (e *env) openItem(ctx context.Context, item QueueItem) (stop bool, err error) {
	claimed := false
	if err := updateQueue(ctx, e.dir, func(items []QueueItem) ([]QueueItem, error) {
		for i := range items {
			if items[i].ID == item.ID && items[i].State == QueueQueued {
				items[i].State, claimed = QueueOpening, true
			}
		}
		return items, nil
	}); err != nil || !claimed {
		return err != nil, err
	}
	if _, err := checkPlace(filepath.Dir(e.dir), item.Cwd, item.Task); err != nil {
		return false, e.fail(ctx, item, "", oneLine(err.Error()))
	}
	// A run that nobody started by hand never opens a paid peer, and never
	// hands the task to a live peer, which may be paid or busy with another.
	opts := e.opts
	opts.WorkDir, opts.FreeOnly, opts.NewOnly = item.Cwd, true, true
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
	err = Send(at, opened.ID, e.taskRef(item))
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

// taskRef names the item's task file for peer send: relative to the checkout
// the peer works in, which send resolves against its own root, so that the
// item's real folder still reads as inside a root named by an alias, such as
// a symlinked temporary folder. checkPlace has kept the file inside it.
func (e *env) taskRef(item QueueItem) string {
	file := filepath.Join(item.Cwd, filepath.FromSlash(item.Task))
	checkout := project.WorkRoot(item.Cwd, filepath.Dir(e.dir))
	if real, err := filepath.EvalSymlinks(checkout); err == nil {
		if rel, err := filepath.Rel(real, file); err == nil && filepath.IsLocal(rel) {
			return rel
		}
	}
	return file
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

// StartQueueRun starts duolog peer queue run for the lead as a detached
// process when the run has work, and reports whether it did. The run has work
// when the item it would open first, past those that wait for a peer still
// working, has an agent with a free session by free: the role's agent or, for
// a role that names none, any agent; a nil free counts that item. Outside Rex
// no run starts.
// It has work too when an item was left opening and no run holds the lock:
// the run fails that item and tells the lead. The run takes the queue's run
// lock itself, so a run already going makes the new one exit at once. It
// appends its output to peer-queue-run.log in the state folder, which a run
// empties once it holds the lock. peer close calls it with no free; the lead's
// prompt hook and a peer's idle event call it with the load line's free
// sessions.
func StartQueueRun(ctx context.Context, free func(agent string) bool) (bool, error) {
	opts, err := callerOptions(ctx)
	if err != nil {
		return false, err
	}
	// An empty queue needs no lead, as for a close outside one.
	e, err := openRecords(opts, false)
	if err != nil {
		return false, err
	}
	waiting := func(q QueueItem) bool { return q.State == QueueQueued || q.State == QueueOpening }
	if all, err := readQueue(e.dir); err != nil || !slices.ContainsFunc(all, waiting) {
		return false, err
	}
	lead, err := e.reg.ResolveLead(opts.Lead)
	if err != nil {
		return false, err
	}
	e.leadID = lead.ID
	// A run opens nothing outside Rex, and judging the head item may probe
	// the peer it waits for.
	leadSession, ok := opts.Deps.Pane.Lead()
	if os.Getenv("REX_SESSION") == "" || !ok {
		return false, nil
	}
	e.panes, e.leadSession = opts.Deps.Pane, leadSession
	items, err := e.leadItems()
	if err != nil {
		return false, err
	}
	if due, err := e.runDue(ctx, items, free); err != nil || !due {
		return false, err
	}
	exe, err := opts.Deps.Executable()
	if err != nil {
		return false, err
	}
	argv := []string{exe, "peer", "queue", "run", "--lead", e.leadID}
	if err := opts.Deps.StartDetached(argv, filepath.Dir(e.dir), filepath.Join(e.dir, queueRunLog)); err != nil {
		return false, err
	}
	return true, nil
}

// runDue reports whether a run of the lead's items has work, as StartQueueRun
// says. Only the item the run would open first counts, the one nextItem
// picks: the run stops at the first refusal, so a free session for a later
// item's agent gives it no work.
func (e *env) runDue(ctx context.Context, items []QueueItem, free func(agent string) bool) (bool, error) {
	if slices.ContainsFunc(items, func(q QueueItem) bool { return q.State == QueueOpening }) {
		dropStaleRunLock(queueRunLock(e.dir, e.leadID))
		unlock, unheld, err := store.TryLockFile(queueRunLock(e.dir, e.leadID))
		if err == nil && unheld {
			_ = unlock()
			return true, nil
		}
	}
	head, ok, err := e.nextItem(ctx, map[string]bool{})
	if err != nil || !ok {
		return false, err
	}
	return free == nil || free(e.cfg.Roles[head.Role].Use.Agent), nil
}

// insideProject reports whether dir is the project root, one of its linked
// worktrees, or a folder in one. Aliases of a folder, such as a symlinked
// temporary folder or another letter case, count as the folder. dir must
// have its symlinks resolved: the walk up its parents is by name.
func insideProject(root, dir string) bool {
	return slices.ContainsFunc(append([]string{root}, linkedWorktrees(root)...), func(place string) bool { return under(place, dir) })
}

// linkedWorktrees lists the checkouts git worktree list names for the project,
// none outside Git.
func linkedWorktrees(root string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := project.Git(ctx, nil, "-C", root, "worktree", "list", "--porcelain")
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
