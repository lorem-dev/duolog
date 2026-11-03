package skilltest

import (
	"github.com/lorem-dev/duolog/internal/config"
	"testing"
)

func TestModelSelectionAndValidation(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{"sonnet": {Agent: "claude", Input: 1, Output: 2}, "opus": {Agent: "claude", Input: 4, Output: 20}, "luna": {Agent: "codex", Input: 1, Output: 2}}}
	runs := DefaultTargets(cfg, map[string]bool{"codex": true})
	if len(runs) != 4 || runs[0].Model != "sonnet" || runs[2].Model != "opus" {
		t.Fatalf("%+v", runs)
	}
	if targets, err := ParseTargets(cfg, "claude/sonnet/low,claude/sonnet/low"); err != nil || len(targets) != 1 {
		t.Fatalf("%+v %v", targets, err)
	}
	for _, s := range []string{"claude/sonnet", "claude/sonnet/bogus", "other/sonnet/low", "codex/sonnet/low", "claude/../high"} {
		if _, err := ParseTargets(cfg, s); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
