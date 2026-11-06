package gate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/store"
)

// askGrace is how long past its wait a pending ask is still trusted: a hook
// that outlives it has been killed, and its ask no longer has an asker.
const askGrace = 2 * time.Second

// askMemo is what the hooks of one tool call share about its ask. Claude runs
// PreToolUse and then PermissionRequest for the same call, and both reach the
// same question, so the first to ask records it here and the other reuses it.
type askMemo struct {
	Done   bool      `json:"done"`
	Start  time.Time `json:"start"`
	Wait   int       `json:"wait"`
	Kind   string    `json:"kind,omitempty"`
	Reason string    `json:"reason,omitempty"`
	Rule   string    `json:"rule,omitempty"`
}

// until is when the memo stops counting: an answer is reused only within the
// wait of the ask it answers, and never past MaxAskWait.
func (m askMemo) until() time.Time {
	return m.Start.Add(min(time.Duration(m.Wait)*time.Second, MaxAskWait))
}

// questionKey starts a key made from the question, which is not hex.
const questionKey = "q"

// AskKey names the ask of one tool call: its session and tool_use_id, or, when
// the hook input has no tool_use_id, the session and the question itself. It
// is empty when nothing identifies the session, so that no ask is shared. A
// question key can name two tool calls that ask the same, so its reply is
// reused at most once. A person-only ask has keys of its own, so that a reply
// from the lead never answers it.
func AskKey(sessionID, toolUseID, question string, person bool) string {
	if sessionID == "" {
		return ""
	}
	if person {
		sessionID += "\x00person"
	}
	if toolUseID == "" {
		sum := sha256.Sum256([]byte(sessionID + "\x00q\x00" + question))
		return questionKey + hex.EncodeToString(sum[:16])
	}
	sum := sha256.Sum256([]byte(sessionID + "\x00id\x00" + toolUseID))
	return hex.EncodeToString(sum[:16])
}

// EscalateOnce runs ask for the first hook of a tool call. A later hook for the
// same key waits for that ask and returns its decision instead of asking again.
// A decision is reused only when it is an allow or deny that was given inside the
// ask's wait. An expired or missing record leaves the hook to ask for itself,
// a record it cannot read makes it wait and then deny, and a wait that ran out
// is never shared, so nothing here turns a deny into an allow or lets an
// unanswered ask through.
//
// ask returns the decision and whether a reply gave it; only a reply is shared.
func EscalateOnce(ctx context.Context, dir, key string, wait time.Duration, ask func() (policy.Decision, bool)) policy.Decision {
	own := func() policy.Decision { d, _ := ask(); return d }
	if key == "" {
		return own()
	}
	folder := filepath.Join(dir, "asks")
	if os.MkdirAll(folder, 0o750) != nil {
		return own()
	}
	path := filepath.Join(folder, key+".json")
	forgetStale(folder)
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		pending, err := claim(path, wait)
		if err == nil {
			d, replied := ask()
			record(path, d, replied, pending)
			return d
		}
		if !errors.Is(err, os.ErrExist) {
			return own()
		}
		if d, ok := sharedDecision(path, strings.HasPrefix(key, questionKey)); ok {
			return d
		}
		// Another hook is asking, or its record is half-written. Wait for it; a
		// wait that runs out denies.
		select {
		case <-waitCtx.Done():
			return policy.Decision{Kind: policy.Deny, Reason: "ask timeout or cancellation", Rule: "ask lead"}
		case <-time.After(AskPoll):
		}
	}
}

// claim creates the record of a new ask, and fails with os.ErrExist when
// another hook already holds one.
func claim(path string, wait time.Duration) ([]byte, error) {
	pending, err := json.Marshal(askMemo{Start: time.Now().UTC(), Wait: int((wait + time.Second - 1) / time.Second)})
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.Write(pending)
	if closeErr := f.Close(); writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.Join(writeErr, closeErr)
	}
	return pending, nil
}

// sharedDecision returns the reply recorded for the key when it is still inside
// the wait of its ask. An expired reply, a pending ask whose asker is gone, or a
// record that starts in the future is removed so that the caller can ask anew.
// A once record is taken by the first hook that reuses it: it moves the file
// aside, reads it again and removes it, so no other hook reuses it.
func sharedDecision(path string, once bool) (policy.Decision, bool) {
	m, ok := readMemo(path)
	now := time.Now()
	switch {
	case !ok:
	case m.Start.After(now) || m.Wait < 0:
		_ = os.Remove(path)
	case reusable(m, now) && !once:
		return policy.Decision{Kind: m.Kind, Reason: m.Reason, Rule: m.Rule}, true
	case reusable(m, now):
		taken := path + "." + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(now.UnixNano(), 36) + ".used"
		if os.Rename(path, taken) != nil {
			return policy.Decision{}, false
		}
		defer os.Remove(taken)
		if m, ok := readMemo(taken); ok && !m.Start.After(now) && m.Wait >= 0 && reusable(m, now) {
			return policy.Decision{Kind: m.Kind, Reason: m.Reason, Rule: m.Rule}, true
		}
	case m.Done || now.After(m.until().Add(askGrace)):
		_ = os.Remove(path)
	}
	return policy.Decision{}, false
}

// reusable reports a reply that is still inside the wait of its ask.
func reusable(m askMemo, now time.Time) bool {
	return m.Done && now.Before(m.until()) && (m.Kind == policy.Allow || m.Kind == policy.Deny)
}

// record stores the asker's decision for the hooks that wait on it. A timeout is
// removed instead, so that the other hook asks for itself rather than inherit a
// wait that has run out.
func record(path string, d policy.Decision, replied bool, pending []byte) {
	var m askMemo
	if json.Unmarshal(pending, &m) != nil || !replied || d.Kind != policy.Allow && d.Kind != policy.Deny {
		_ = os.Remove(path)
		return
	}
	m.Done, m.Kind, m.Reason, m.Rule = true, d.Kind, d.Reason, d.Rule
	data, err := json.Marshal(m)
	if err != nil {
		_ = os.Remove(path)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ask-*")
	if err != nil {
		_ = os.Remove(path)
		return
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil || store.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
		_ = os.Remove(path)
	}
}

func readMemo(path string) (askMemo, bool) {
	data, err := os.ReadFile(path)
	var m askMemo
	if err != nil || json.Unmarshal(data, &m) != nil || m.Start.IsZero() {
		return askMemo{}, false
	}
	return m, true
}

// forgetStale removes records whose ask can no longer be waited for.
func forgetStale(folder string) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, e := range entries {
		path := filepath.Join(folder, e.Name())
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > MaxAskWait+time.Minute {
			_ = os.Remove(path)
		}
	}
}
