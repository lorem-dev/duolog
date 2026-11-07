package skilltest

import (
	"github.com/lorem-dev/duolog/internal/core/config"
	"slices"
	"strings"
	"testing"
)

var testAgents = []string{"claude", "codex"}

func TestModelSelectionAndValidation(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{"sonnet": {Agent: "claude", Input: 1, Output: 2}, "opus": {Agent: "claude", Input: 4, Output: 20}, "luna": {Agent: "codex", Input: 1, Output: 2}}}
	runs := DefaultTargets(cfg, testAgents, map[string]bool{"codex": true})
	if len(runs) != 4 || runs[0].Model != "sonnet" || runs[2].Model != "opus" {
		t.Fatalf("%+v", runs)
	}
	if targets, err := ParseTargets(cfg, testAgents, "claude/sonnet/low,claude/sonnet/low", false); err != nil || len(targets) != 1 {
		t.Fatalf("%+v %v", targets, err)
	}
	for _, s := range []string{"claude/sonnet", "claude/sonnet/bogus", "other/sonnet/low", "codex/sonnet/low", "claude/../high"} {
		if _, err := ParseTargets(cfg, testAgents, s, false); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
}

func TestDefaultTargetsSkipManualOnlyModels(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{
		"haiku": {Agent: "claude", Input: 1, Output: 2}, "opus": {Agent: "claude", Input: 4, Output: 20}, "fable": {Agent: "claude", Input: 10, Output: 50},
		"luna": {Agent: "codex", Input: 1, Output: 2}, "sol": {Agent: "codex", Input: 3, Output: 12}, "astra": {Agent: "codex", Input: 9, Output: 40},
	}}
	cfg.Limits.ManualOnlyModels = []string{"fable", "astra"}
	var got []string
	for _, r := range DefaultTargets(cfg, testAgents, nil) {
		got = append(got, r.Agent+"/"+r.Model+"/"+r.Effort)
	}
	want := []string{"claude/haiku/low", "claude/haiku/high", "claude/opus/low", "claude/opus/high", "codex/luna/low", "codex/luna/high", "codex/sol/low", "codex/sol/high"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	// A model that is manual-only through config is skipped too, and fable and
	// astra stay reserved when the list is empty, as in the router.
	cfg.Limits.ManualOnlyModels = []string{"opus"}
	for _, r := range DefaultTargets(cfg, testAgents, nil) {
		if r.Model == "opus" || r.Model == "fable" || r.Model == "astra" {
			t.Errorf("picked %s", r.Model)
		}
	}
	cfg.Limits.ManualOnlyModels = nil
	for _, r := range DefaultTargets(cfg, testAgents, nil) {
		if r.Model == "fable" || r.Model == "astra" {
			t.Errorf("picked %s", r.Model)
		}
	}
}

func TestParseTargetsManualOnlyNeedsPermission(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{"fable": {Agent: "claude", Input: 10, Output: 50}, "opus": {Agent: "claude", Input: 4, Output: 20}}}
	cfg.Limits.ManualOnlyModels = []string{"opus"}
	for _, s := range []string{"claude/fable/low", "claude/opus/low", "claude/opus/low,claude/fable/low"} {
		if _, err := ParseTargets(cfg, testAgents, s, false); err == nil || !strings.Contains(err.Error(), "--allow-manual-model") {
			t.Errorf("accepted %s: %v", s, err)
		}
		if targets, err := ParseTargets(cfg, testAgents, s, true); err != nil || len(targets) == 0 {
			t.Errorf("refused %s: %v", s, err)
		}
	}
}
