package hook

import (
	"context"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/session"
)

// Stop runs in a peer's pane when the agent ends a turn. When the peer's task
// is still unfinished by the rule of duolog peer status, and no duolog peer
// wait watches the peer and so will report the turn itself, it puts one
// status line on the bus for the lead, whose hooks deliver it. The hook adds
// no context to the peer and never blocks its stop.
func Stop(ctx context.Context, e Env) Output {
	peer := value(environment(e), "DUOLOG_PEER")
	if ctx.Err() != nil || peer == "" || !worker(e) || (e.Agent != "claude" && e.Agent != "codex") {
		return Output{}
	}
	dir := filepath.Join(e.Root, ".duolog")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Output{}
	}
	// Best effort: a hook failure must not reach the peer's session.
	_ = tellStopped(e.Root, dir, peer)
	return Output{}
}

// tellStopped sends the lead "<peer> stopped; task <path> is unfinished".
func tellStopped(root, dir, peer string) error {
	reg, err := session.Open(dir)
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
	cfg, err := config.Load(root)
	if err != nil {
		return err
	}
	b, err := bus.Open(dir, cfg.Wire)
	if err != nil {
		return err
	}
	text := cutBytes(entry.ID+" stopped; task "+entry.Task+" is unfinished", cfg.Wire.MaxSummaryBytes)
	_, err = b.Send(bus.Message{From: entry.Agent + "/" + entry.Role, To: session.Address(lead), K: "status", Ref: entry.ID, S: text})
	return err
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
