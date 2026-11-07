package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/lorem-dev/duolog/internal/store"
)

// peerOrWorker reports a session in a peer's pane or a worker. A Codex peer
// would otherwise register as a lead at its first prompt, so DUOLOG_PEER
// counts as well as the depth.
func peerOrWorker(e Env) bool {
	return worker(e) || value(environment(e), "DUOLOG_PEER") != ""
}

// loadLine is the lead's load line, or "" when there is none to show: the
// session is no registered lead, no Load is set, nothing is running or
// limited, or the line fails to compute. A hook never fails over it.
//
// A prompt always shows the line. A bus delivery (quiet) shows it only when it
// is due: a number changed, or 5 minutes passed since the lead last saw it.
func loadLine(ctx context.Context, e Env, s *state, quiet bool) (line string) {
	if s == nil || e.Load == nil || peerOrWorker(e) {
		return ""
	}
	defer func() {
		if recover() != nil {
			line = ""
		}
	}()
	dir := filepath.Join(e.Root, ".duolog")
	line = e.Load(ctx, s.lead.ID).Line()
	if line == "" {
		return ""
	}
	now := time.Now()
	if quiet && !loadDue(dir, s.lead.ID, line, now) {
		return ""
	}
	if !quiet {
		loadMark(dir, s.lead.ID, line, now)
	}
	return line
}

// loadEvery is how long the same load line stays quiet on a bus delivery.
const loadEvery = 5 * time.Minute

// shownLine is the load line a lead was last shown, and when.
type shownLine struct {
	Line string    `json:"line"`
	At   time.Time `json:"at"`
}

func shownPath(dir string) string { return filepath.Join(dir, "load-line.json") }

// withShown changes the record of shown load lines under its lock and, when
// change reports a change, writes it back in one rename, so that the hooks of
// every lead can mark at once. It waits for the lock at most
// store.NoticeWait and reports whether change ran.
func withShown(dir string, change func(seen map[string]shownLine) bool) bool {
	ctx, cancel := context.WithTimeout(context.Background(), store.NoticeWait)
	defer cancel()
	unlock, err := store.LockFileContext(ctx, filepath.Join(dir, "load-line.lock"), 5*time.Millisecond)
	if err != nil {
		return false
	}
	defer func() { _ = unlock() }()
	seen := map[string]shownLine{}
	if data, err := store.ReadFile(shownPath(dir)); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	if !change(seen) {
		return true
	}
	data, err := json.Marshal(seen)
	if err != nil {
		return true
	}
	tmp, err := os.CreateTemp(dir, "load-line.*.tmp")
	if err != nil {
		return true
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = store.Rename(tmp.Name(), shownPath(dir))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return true
}

// loadMark records that the lead was shown line at now.
func loadMark(dir, leadID, line string, now time.Time) {
	withShown(dir, func(seen map[string]shownLine) bool {
		seen[leadID] = shownLine{Line: line, At: now}
		return true
	})
}

// loadDue says whether a bus delivery may carry line now: it was not shown to
// the lead within the last 5 minutes, or a number in it changed. A due line
// is recorded as shown under the same lock, so two deliveries at once do not
// both carry it. A record it cannot lock makes the line not due.
func loadDue(dir, leadID, line string, now time.Time) bool {
	due := false
	withShown(dir, func(seen map[string]shownLine) bool {
		last, ok := seen[leadID]
		if ok && last.Line == line && now.Sub(last.At) < loadEvery && !now.Before(last.At) {
			return false
		}
		seen[leadID] = shownLine{Line: line, At: now}
		due = true
		return true
	})
	return due
}

// startQueue lets a registered lead's prompt start its queue run, which opens
// queued peers while sessions are free. A peer or worker starts none.
func startQueue(ctx context.Context, e Env, s *state) {
	if s == nil || e.QueueRun == nil || peerOrWorker(e) {
		return
	}
	e.QueueRun(ctx, s.lead.ID)
}

// followIdle calls PeerIdle once for each peer whose peer-idle message the
// lead was just given, so a peer that finished its turn lets queued work
// start.
func followIdle(ctx context.Context, e Env, s *state, idle []string) {
	if s == nil || e.PeerIdle == nil || peerOrWorker(e) {
		return
	}
	for _, id := range idle {
		e.PeerIdle(ctx, s.lead.ID, id)
	}
}
