package hook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if s == nil {
		return ""
	}
	line, _ = computeLoad(ctx, e, s.lead.ID)
	if line == "" {
		return ""
	}
	dir := filepath.Join(e.Root, ".duolog")
	now := time.Now()
	if quiet && !loadDue(dir, s.lead.ID, line, now) {
		return ""
	}
	if !quiet {
		loadMark(dir, s.lead.ID, line, now)
	}
	return line
}

// computeLoad is the lead's load line from e.Load, "" when there is none. A
// panic in Load comes back as an error.
func computeLoad(ctx context.Context, e Env, leadID string) (line string, err error) {
	if e.Load == nil || peerOrWorker(e) {
		return "", nil
	}
	defer func() {
		if r := recover(); r != nil {
			line, err = "", fmt.Errorf("load line: %v", r)
		}
	}()
	return e.Load(ctx, leadID).Line(), nil
}

// LoadLine is the load line of the lead whose session id is in e.Input, as
// the prompt hook shows it, for a caller outside the hooks. It has no side
// effects: it reads no bus line, moves no cursor, marks no line as shown and
// starts no queue run. It is "" for a peer, a worker or when e has no Load.
func LoadLine(ctx context.Context, e Env) (string, error) {
	var h input
	if err := json.Unmarshal(e.Input, &h); err != nil || h.ID == "" {
		return "", errors.New("load line: the input carries no session_id")
	}
	return computeLoad(ctx, e, h.ID)
}

// loadEvery is how long the same load line stays quiet on a bus delivery.
const loadEvery = 5 * time.Minute

// shownLine is the load line a lead was last shown, and when, and the context
// size at which it was last offered a fresh session (0: never).
type shownLine struct {
	Line string    `json:"line"`
	At   time.Time `json:"at"`
	Hint int       `json:"hint,omitempty"`
}

// shownDir holds one record of the shown load line per lead, with its lock.
const shownDir = "load-lines"

// shownName is the lead's file name in shownDir, without extension: the lead
// id with any character a file name may not hold replaced.
func shownName(leadID string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, leadID)
}

// shownLock is the lock of the lead's record. Each lead has its own, so the
// hooks of one lead never wait for those of another: a wait is bounded by
// store.NoticeWait, and on Windows a write and its rename are slow enough
// that hooks of many leads queued on one lock ran out of it and lost marks.
func shownLock(dir, leadID string) string {
	return filepath.Join(dir, shownDir, shownName(leadID)+".lock")
}

func shownPath(dir, leadID string) string {
	return filepath.Join(dir, shownDir, shownName(leadID)+".json")
}

// withShown changes the lead's record of its shown load line under the
// lead's lock and, when change reports a change, writes it back in one
// rename. It waits for the lock at most store.NoticeWait and reports whether
// change ran.
func withShown(dir, leadID string, change func(seen *shownLine) bool) bool {
	if err := os.MkdirAll(filepath.Join(dir, shownDir), 0o700); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), store.NoticeWait)
	defer cancel()
	unlock, err := store.LockFileContext(ctx, shownLock(dir, leadID), 5*time.Millisecond)
	if err != nil {
		return false
	}
	defer func() { _ = unlock() }()
	var seen shownLine
	if data, err := store.ReadFile(shownPath(dir, leadID)); err == nil {
		_ = json.Unmarshal(data, &seen)
	}
	if !change(&seen) {
		return true
	}
	data, err := json.Marshal(seen)
	if err != nil {
		return true
	}
	tmp, err := os.CreateTemp(filepath.Join(dir, shownDir), "*.tmp")
	if err != nil {
		return true
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = store.Rename(tmp.Name(), shownPath(dir, leadID))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return true
}

// loadMark records that the lead was shown line at now.
func loadMark(dir, leadID, line string, now time.Time) {
	withShown(dir, leadID, func(seen *shownLine) bool {
		seen.Line, seen.At = line, now
		return true
	})
}

// loadDue says whether a bus delivery may carry line now: it was not shown to
// the lead within the last 5 minutes, or a number in it changed. A due line
// is recorded as shown under the same lock, so two deliveries at once do not
// both carry it. A record it cannot lock makes the line not due.
func loadDue(dir, leadID, line string, now time.Time) bool {
	due := false
	withShown(dir, leadID, func(seen *shownLine) bool {
		if seen.Line == line && now.Sub(seen.At) < loadEvery && !now.Before(seen.At) {
			return false
		}
		seen.Line, seen.At = line, now
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
