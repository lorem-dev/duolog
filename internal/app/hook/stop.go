package hook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"unicode/utf8"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/bus"
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
	if ctx.Err() != nil || peer == "" || !worker(e) || !served(e) {
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
	return Output{}
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
