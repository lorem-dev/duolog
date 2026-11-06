package hook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
)

// Stop runs in a peer's pane when the agent ends a turn. When the peer's task
// is still unfinished by the rule of duolog peer status, and no duolog peer
// wait watches the peer and so will report the turn itself, it puts one
// status line on the bus for the lead, whose hooks deliver it, once per sent
// task. The hook adds no context to the peer and never blocks its stop: it
// waits for the registry and bus locks at most store.NoticeWait in all, and
// less when ctx ends first, and drops the line then.
func Stop(ctx context.Context, e Env) Output {
	peer := value(environment(e), "DUOLOG_PEER")
	if ctx.Err() != nil || peer == "" || !worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return Output{}
	}
	dir := filepath.Join(e.Root, ".duolog")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Output{}
	}
	ctx, cancel := context.WithTimeout(ctx, store.NoticeWait)
	defer cancel()
	// Best effort: a hook failure must not reach the peer's session.
	_ = tellStopped(ctx, e.Root, dir, peer)
	_ = tellIdle(ctx, e, dir, peer)
	return Output{}
}

// idleSummaryRunes is the longest first line of the last message a peer-idle
// message carries.
const idleSummaryRunes = 160

// tellIdle sends the lead one peer-idle message for the turn that just ended:
// "<peer> idle in <cwd>", then the first line of the turn's last assistant
// message when the hook payload carries one. A Stop hook that runs again for
// the same turn, with the same transcript length, sends nothing. After the
// send it calls Env.PeerIdle, the hook point of the ready queue.
func tellIdle(ctx context.Context, e Env, dir, peer string) error {
	var h struct {
		TranscriptPath string `json:"transcript_path"`
		LastMessage    string `json:"last_assistant_message"`
	}
	_ = json.Unmarshal(e.Input, &h)
	reg, err := session.OpenWithin(ctx, dir)
	if err != nil {
		return err
	}
	entries, err := reg.Entries()
	if err != nil {
		return err
	}
	var entry session.Entry
	for _, x := range entries {
		if x.ID == peer {
			entry = x
		}
	}
	if entry.ID == "" || entry.Closed {
		return nil
	}
	lead, alive, err := reg.Lead(entry.Lead)
	if err != nil || !alive {
		return err
	}
	cfg, err := configfile.Load(e.Root)
	if err != nil {
		return err
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return err
	}
	turn := turnKey(h.TranscriptPath)
	previous, first, err := reg.ClaimIdle(peer, turn)
	if err != nil || !first {
		return err
	}
	text := cutBytes(idleText(e.Root, entry, h.LastMessage), cfg.Wire.MaxSummaryBytes)
	if _, err := b.Within(ctx).Send(bus.Message{From: entry.Agent + "/" + entry.Role, To: session.Address(lead), K: "peer-idle", Ref: entry.ID, S: text}); err != nil {
		release, cancel := context.WithTimeout(context.WithoutCancel(ctx), store.NoticeWait)
		defer cancel()
		return errors.Join(err, reg.Within(release).ReleaseIdle(previous, turn))
	}
	if e.PeerIdle != nil {
		e.PeerIdle(ctx, lead.ID, entry.ID)
	}
	return nil
}

// tellExited posts one peer-exit message to the lead for each of its peers
// whose process `duolog peer list` sees as exited or gone, once per peer: the
// peer's record remembers it. It is best effort, bounded by store.NoticeWait.
func tellExited(ctx context.Context, e Env, s *state) {
	if e.Peers == nil || ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, store.NoticeWait)
	defer cancel()
	for _, l := range e.Peers(ctx, s.lead.ID) {
		id, st, ok := strings.Cut(l, " ")
		if !ok || (st != "exited" && st != "gone") {
			continue
		}
		entry, first, err := s.reg.Within(ctx).ClaimExit(id)
		if err != nil || !first {
			continue
		}
		text := cutBytes(id+" exited in "+relativeCwd(e.Root, entry.WorkRoot), s.cfg.Wire.MaxSummaryBytes)
		if _, err := s.bus.Within(ctx).Send(bus.Message{From: entry.Agent + "/" + entry.Role, To: session.Address(s.lead), K: "peer-exit", Ref: id, S: text}); err != nil {
			release, cancel := context.WithTimeout(context.WithoutCancel(ctx), store.NoticeWait)
			_ = s.reg.Within(release).ReleaseExit(id)
			cancel()
		}
	}
}

// turnKey names the turn that just ended by the transcript's length; with no
// readable transcript every turn end is a new turn.
func turnKey(transcript string) string {
	if info, err := os.Stat(transcript); transcript != "" && err == nil {
		return strconv.FormatInt(info.Size(), 10)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

// idleText is the text of a peer-idle message.
func idleText(root string, entry session.Entry, last string) string {
	text := entry.ID + " idle in " + relativeCwd(root, entry.WorkRoot)
	if summary := firstLine(last); summary != "" {
		text += ": " + summary
	}
	return text
}

// relativeCwd names the peer's checkout relative to the project, "." for the
// project itself; a checkout outside it keeps its whole path.
func relativeCwd(root, workRoot string) string {
	if workRoot == "" {
		return "."
	}
	rel, err := filepath.Rel(root, workRoot)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(workRoot)
	}
	return filepath.ToSlash(rel)
}

// firstLine is the first non-empty line of s, at most idleSummaryRunes long.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > idleSummaryRunes {
				return string(r[:idleSummaryRunes])
			}
			return l
		}
	}
	return ""
}

// tellStopped sends the lead "<peer> stopped; task <path> is unfinished",
// once per task.
func tellStopped(ctx context.Context, root, dir, peer string) error {
	reg, err := session.OpenWithin(ctx, dir)
	if err != nil {
		return err
	}
	entries, err := reg.Entries()
	if err != nil {
		return err
	}
	var entry session.Entry
	for _, x := range entries {
		if x.ID == peer {
			entry = x
		}
	}
	if entry.ID == "" || !entry.Unfinished() {
		return nil
	}
	if watched, err := reg.Watched(peer); err != nil || watched {
		return err
	}
	lead, alive, err := reg.Lead(entry.Lead)
	if err != nil || !alive {
		return err
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		return err
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return err
	}
	// The claim comes last before the send, so a stop that only failed to
	// read its state does not use up the task's one line.
	entry, first, err := reg.ClaimStopNotice(peer)
	if err != nil || !first {
		return err
	}
	text := cutBytes(entry.ID+" stopped; task "+entry.Task+" is unfinished", cfg.Wire.MaxSummaryBytes)
	return sendClaimed(ctx, reg, b, entry, bus.Message{From: entry.Agent + "/" + entry.Role, To: session.Address(lead), K: "status", Ref: entry.ID, S: text})
}

// sendClaimed sends the stop line that entry's claim stands for. When it is
// not sent, the claim goes back, so a later stop on the same task sends it;
// that wait is bounded too, since ctx may be over.
func sendClaimed(ctx context.Context, reg *session.Registry, b *bus.Bus, entry session.Entry, m bus.Message) error {
	_, err := b.Within(ctx).Send(m)
	if err == nil {
		return nil
	}
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), store.NoticeWait)
	defer cancel()
	return errors.Join(err, reg.Within(release).ReleaseStopNotice(entry))
}

// cutBytes keeps at most n bytes of s and never splits a character.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
