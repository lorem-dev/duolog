package hook

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

// maxPeerLines is the most live peer lines a compaction note holds.
const maxPeerLines = 20

// compactInstructions are what Claude adds to the instructions for the summary
// of a compaction, so that it keeps what the lead delegated.
const compactInstructions = "When you write the summary, keep the duolog delegation state: " +
	"the open peers, each with its task file and worktree; the asks still open; " +
	"and the branches and pull requests you are working on."

// compactNotes reports whether the lead of this project wants compaction
// notes: a Claude lead, not a worker, in a project that has duolog state, with
// lead.compact_notes on.
func compactNotes(e Env) bool {
	if worker(e) || e.Agent != "claude" {
		return false
	}
	info, err := os.Stat(filepath.Join(e.Root, ".duolog"))
	if err != nil || !info.IsDir() {
		return false
	}
	cfg, err := configfile.Load(e.Root)
	return err == nil && cfg.Lead.CompactNotes
}

// peerNote is the live peer list, at most maxPeerLines lines, one per peer as
// `duolog peer list` prints them; empty when there are none.
func peerNote(ctx context.Context, e Env) string {
	if e.Peers == nil {
		return ""
	}
	var h input
	if json.Unmarshal(e.Input, &h) != nil || h.ID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lines := e.Peers(ctx, h.ID)
	if len(lines) > maxPeerLines {
		lines = lines[:maxPeerLines]
	}
	if len(lines) == 0 {
		return ""
	}
	return "Live duolog peers:\n" + strings.Join(lines, "\n")
}

// PreCompact prints the plain text that Claude adds to the instructions for
// the summary: what to keep of the delegation, then the live peers. It prints
// nothing for a worker or with lead.compact_notes off.
func PreCompact(ctx context.Context, e Env) Output {
	if ctx.Err() != nil || !compactNotes(e) {
		return Output{}
	}
	text := compactInstructions
	if note := peerNote(ctx, e); note != "" {
		text += "\n" + note
	}
	return Output{Stdout: []byte(text + "\n")}
}

// compactStart is the live peer list that the session start after a
// compaction adds to what the hook prints.
func compactStart(ctx context.Context, e Env) string {
	var h input
	if json.Unmarshal(e.Input, &h) != nil || h.Source != "compact" || !compactNotes(e) {
		return ""
	}
	return peerNote(ctx, e)
}
