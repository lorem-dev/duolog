package hook

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// personEvents is the optional hook output of an agent that can show the
// person a line next to its normal output: Claude Code's and Codex's
// systemMessage. alert says that the line wants the person's eyes now; an
// agent may ignore it.
type personEvents interface {
	Show(out []byte, text string, alert bool) []byte
}

// noticeDir holds one record of what the person was told per lead.
const noticeDir = "notices"

// snapshotWait is how long a hook waits for the snapshot. A slower one gives no
// notice: a hook never makes the person's turn wait.
const snapshotWait = 2 * time.Second

// noticeLimit is the longest notice in bytes, "duolog: " included.
const noticeLimit = 200

// noticeMark is what a lead was last told: the runs that had ended, the runs
// that were running, the peers that were idle and the asks that were open. A
// running id that leaves the snapshot altogether is a run that ended after
// the newest finished ones pushed it out, and is told as such. An idle peer is
// "<id> <turn>": the turn its Stop hook recorded, so that its next turn end is
// news again.
type noticeMark struct {
	Ended   []string `json:"ended"`
	Running []string `json:"running"`
	Idle    []string `json:"idle"`
	Asks    int      `json:"asks"`
}

// turns maps a peer id to the end of turn its Stop hook last recorded. The
// fast snapshot cannot tell an idle peer from a working one, so this is what
// idleness is taken from.
type turns map[string]string

// changes is the notice for what is new in s since m, and the mark after it.
// A nil m is the first look: the person is told of open asks only, as every
// ended run and idle peer is old news then. Ids that s does not hold drop out
// of the next mark. An id that m holds and s lists in any state stays, so that
// a hook that read an older snapshot does not make the next one repeat a line.
func changes(s runsnap.Snapshot, m *noticeMark, idle turns) (text string, next noticeMark) {
	next = noticeMark{Ended: []string{}, Running: []string{}, Idle: []string{}, Asks: s.AsksOpen}
	var items []string
	if s.AsksOpen > 0 && (m == nil || s.AsksOpen > m.Asks) {
		items = append(items, asksItem(s.AsksOpen))
	}
	listed := map[string]bool{}
	for _, r := range s.Runs {
		listed[r.ID] = true
		if r.State == "running" {
			next.Running = append(next.Running, r.ID)
			if m != nil && slices.Contains(m.Ended, r.ID) {
				next.Ended = append(next.Ended, r.ID)
			}
			continue
		}
		next.Ended = append(next.Ended, r.ID)
		// A run here started no worker: the lead got the answer from its own
		// call and does the work, so the person needs no line for it.
		if m != nil && !slices.Contains(m.Ended, r.ID) && r.State != "run_here" {
			items = append(items, runItem(r))
		}
	}
	if m != nil {
		for _, id := range m.Running {
			if !listed[id] {
				items = append(items, "run "+id+" ended")
			}
		}
	}
	next.Idle, items = idleChanges(s.Peers, m, idle, items)
	return join(items), next
}

// idleChanges adds the peers whose turn ended since m to items and returns the
// idle keys of the next mark. A peer that is working, exited or gone is not
// idle; one that m held stays held while it is listed.
func idleChanges(peers []runsnap.Peer, m *noticeMark, idle turns, items []string) (keys, all []string) {
	keys = []string{}
	for _, p := range peers {
		held := ""
		if m != nil {
			held = idleKey(m.Idle, p.ID)
		}
		turn, ok := idle[p.ID]
		if !ok || p.Working || p.State == "exited" || p.State == "gone" || p.State == "closed" {
			if held != "" {
				keys = append(keys, held)
			}
			continue
		}
		key := p.ID + " " + turn
		keys = append(keys, key)
		if m != nil && key != held {
			items = append(items, "peer "+p.ID+" is idle")
		}
	}
	return keys, items
}

// idleKey is the key of the peer id in keys, "" when there is none.
func idleKey(keys []string, id string) string {
	for _, k := range keys {
		if name, _, _ := strings.Cut(k, " "); name == id {
			return k
		}
	}
	return ""
}

func asksItem(n int) string {
	if n == 1 {
		return "1 ask waits for an answer"
	}
	return strconv.Itoa(n) + " asks wait for an answer"
}

// runItem names an ended run: "run r2 (review) failed".
func runItem(r runsnap.Run) string {
	name := "run " + r.ID
	if r.Role != "" {
		name += " (" + r.Role + ")"
	}
	return name + " " + runWord(r.State)
}

// runWord is how a notice says that a run in state ended.
func runWord(state string) string {
	if state == "needs" {
		return "needs input"
	}
	return state
}

// join is the notice for items: the first few named, the rest counted, and
// never longer than noticeLimit.
func join(items []string) string {
	const prefix = "duolog: "
	if len(items) == 0 {
		return ""
	}
	var text string
	for n := min(3, len(items)); n >= 1; n-- {
		text = prefix + strings.Join(items[:n], ", ")
		if n < len(items) {
			text += " and " + strconv.Itoa(len(items)-n) + " more"
		}
		if len(text) <= noticeLimit {
			return text
		}
	}
	for len(text) > noticeLimit {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}
	return text
}

// notice is what the person is told of the lead with this session id, "" when
// nothing is new or anything fails: a worker, a peer, a session that is no
// registered lead of e.Agent, no Snapshot, a snapshot that fails, panics or
// takes longer than snapshotWait. What it returns it records as told, so that
// the next hook has nothing to say of it.
func notice(ctx context.Context, e Env, leadID string) string {
	text, _ := noticeAlert(ctx, e, leadID, false)
	return text
}

// noticeAlert is notice, and with alert whether the notice should call the
// person back as well: lead.notify, read within the same budget as the
// snapshot. A config that fails, panics or comes late gives no alert.
func noticeAlert(ctx context.Context, e Env, leadID string, alert bool) (text string, alerted bool) {
	if e.Snapshot == nil || subagent(e.Input) || leadID == "" || peerOrWorker(e) || !served(e) || ctx.Err() != nil {
		return "", false
	}
	defer func() {
		if recover() != nil {
			text, alerted = "", false
		}
	}()
	dir := project.LeadHome(e.Root)
	if !folderExists(dir) {
		return "", false
	}
	// One budget for the registry, the snapshot and the config together.
	ctx, cancel := context.WithTimeout(ctx, snapshotWait)
	defer cancel()
	idle, ok := registeredIdle(ctx, e, dir, leadID)
	if !ok {
		return "", false
	}
	snap, ok := snapshotWithin(ctx, e, leadID)
	if !ok || snap.Lead == nil {
		return "", false
	}
	text = recordNotice(dir, leadID, snap, idle)
	return text, alert && text != "" && notifiesWithin(ctx, e)
}

// recordNotice is the notice for snap, recorded as told in the lead's notice
// record under dir.
func recordNotice(dir, leadID string, snap runsnap.Snapshot, idle turns) (text string) {
	withLeadRecord(dir, noticeDir, leadID, func(m **noticeMark) bool {
		var next noticeMark
		text, next = changes(snap, *m, idle)
		if *m != nil && sameMark(**m, next) {
			// A lead that looks stays: pruneNotices keeps what changed lately.
			now := time.Now()
			_ = os.Chtimes(recordPath(dir, noticeDir, leadID), now, now)
			return false
		}
		*m = &next
		return true
	})
	return text
}

func sameMark(a, b noticeMark) bool {
	return slices.Equal(a.Ended, b.Ended) && slices.Equal(a.Running, b.Running) && slices.Equal(a.Idle, b.Idle) && a.Asks == b.Asks
}

// registeredIdle opens the registry of dir once. It says whether leadID is a
// lead of e.Agent there, and gives the end of turn that each of its open peers
// recorded.
func registeredIdle(ctx context.Context, e Env, dir, leadID string) (turns, bool) {
	reg, err := session.OpenWithin(ctx, dir)
	if err != nil {
		return nil, false
	}
	lead, alive, err := reg.Lead(leadID)
	if err != nil || !alive || lead.Agent != e.Agent {
		return nil, false
	}
	entries, err := reg.Entries()
	if err != nil {
		return nil, false
	}
	idle := turns{}
	for _, x := range entries {
		if x.Lead == leadID && !x.Closed && x.IdleTurn != "" {
			idle[x.ID] = x.IdleTurn
		}
	}
	return idle, true
}

// snapshotWithin is e.Snapshot's reading, given up when ctx ends. A Snapshot
// that ignores its context goes on in its own goroutine and its answer is
// dropped.
func snapshotWithin(ctx context.Context, e Env, leadID string) (runsnap.Snapshot, bool) {
	type answer struct {
		snap runsnap.Snapshot
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- answer{err: fmt.Errorf("snapshot: %v", r)}
			}
		}()
		s, err := e.Snapshot(ctx, leadID)
		done <- answer{s, err}
	}()
	select {
	case a := <-done:
		return a.snap, a.err == nil
	case <-ctx.Done():
		return runsnap.Snapshot{}, false
	}
}

// withNotice adds the notice for the lead whose session id is in e.Input to
// out, next to what out carries. An agent that cannot show it gets none. alert
// asks the agent to call the person back as well, unless lead.notify is false
// or the config cannot be read: the lead's Stop alerts, as the person may have
// left while it worked.
func withNotice(ctx context.Context, e Env, out Output, alert bool) Output {
	show, ok := e.Events.(personEvents)
	if !ok {
		return out
	}
	var h input
	if json.Unmarshal(e.Input, &h) != nil {
		return out
	}
	if text, alerted := noticeAlert(ctx, e, h.ID, alert); text != "" {
		out.Stdout = show.Show(out.Stdout, text, alerted)
	}
	return out
}

// notifiesWithin says whether lead.notify leaves the alerts on, given up when
// ctx ends. A config that fails to load, a load that panics and one that
// outlasts ctx give no alert; a late load goes on in its own goroutine and its
// answer is dropped.
func notifiesWithin(ctx context.Context, e Env) bool {
	done := make(chan bool, 1)
	go func() {
		on := false
		defer func() {
			_ = recover()
			done <- on
		}()
		cfg, err := loadConfig(e)
		on = err == nil && cfg != nil && cfg.Lead.Notifies()
	}()
	select {
	case on := <-done:
		return on
	case <-ctx.Done():
		return false
	}
}

// subagent says whether a Claude subagent made the hook call: it shares its
// lead's session id, and the person is told through the lead's own hooks.
func subagent(input []byte) bool {
	var h struct {
		AgentID string `json:"agent_id"`
	}
	return json.Unmarshal(input, &h) == nil && h.AgentID != ""
}

// pruneNotices removes the notice records of leads that nothing changed for
// store.KeepFor: a lead that is gone leaves its record and lock behind. A lead
// that is still there only sees its first look again, which is quiet.
func pruneNotices(dir string, now time.Time) {
	parent := filepath.Join(dir, noticeDir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, x := range entries {
		if info, err := x.Info(); err == nil && strings.HasSuffix(x.Name(), ".json") && now.Sub(info.ModTime()) > store.KeepFor {
			_ = os.Remove(filepath.Join(parent, x.Name()))
		}
	}
	// A lock goes with its record only: a lead's lock may be old while it looks.
	for _, x := range entries {
		if stem, ok := strings.CutSuffix(x.Name(), ".lock"); ok {
			if _, err := os.Stat(filepath.Join(parent, stem+".json")); err != nil {
				_ = os.Remove(filepath.Join(parent, x.Name()))
			}
		}
	}
}
