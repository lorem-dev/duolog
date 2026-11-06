package learn

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

func defaults(t *testing.T) config.Learning {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Learning
}

func TestClassifyFirstMatchWins(t *testing.T) {
	l := defaults(t)
	if got := Classify(l.Classify, "debug_hard", map[string]string{"output": "diagnosis", "components": "2+"}); got != "debug_multi" {
		t.Fatalf("got %q", got)
	}
	if got := Classify(l.Classify, "debug_hard", map[string]string{"output": "diagnosis"}); got != "debug" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyRoleCondition(t *testing.T) {
	l := defaults(t)
	if got := Classify(l.Classify, "planner", nil); got != "feature" {
		t.Fatalf("got %q", got)
	}
	if got := Classify(l.Classify, "mechanical", map[string]string{}); got != "mechanical" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyNoMatch(t *testing.T) {
	l := defaults(t)
	if got := Classify(l.Classify, "review_final", map[string]string{}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyPlanReviewNeedsTarget(t *testing.T) {
	l := defaults(t)
	if got := Classify(l.Classify, "review_final", map[string]string{"output": "review", "target": "plan"}); got != "plan_review" {
		t.Fatalf("got %q", got)
	}
	if got := Classify(l.Classify, "review_final", map[string]string{"output": "review"}); got != "review" {
		t.Fatalf("got %q", got)
	}
}

func TestParseFeaturesRejectsUnknown(t *testing.T) {
	got, err := ParseFeatures("output=code,exemplar=no")
	if err != nil || got["output"] != "code" || got["exemplar"] != "no" {
		t.Fatalf("got %v, %v", got, err)
	}
	for in, want := range map[string]string{"colour=red": "exemplar", "output=poem": "diagnosis", "output": "name=value"} {
		if _, err := ParseFeatures(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%q: error %v does not contain %q", in, err, want)
		}
	}
}

func TestChooseSkipsUnavailableAgent(t *testing.T) {
	c, ok := Choose(defaults(t), "debug", "debug_hard", map[string]bool{"codex": true}, "")
	if !ok || c.Entry.Key() != "claude/opus/high" || c.Why != "prior" || c.Ladder != "debug" || c.Class != "debug" {
		t.Fatalf("got %+v, %v", c, ok)
	}
}

func TestChooseAllUnavailable(t *testing.T) {
	if c, ok := Choose(defaults(t), "tests", "tests", map[string]bool{"codex": true}, ""); ok {
		t.Fatalf("got %+v", c)
	}
}

func TestChooseAfterEntry(t *testing.T) {
	l := defaults(t)
	c, ok := Choose(l, "debug", "debug_hard", nil, "codex/terra/medium")
	if !ok || c.Entry.Key() != "codex/sol/medium" {
		t.Fatalf("got %+v, %v", c, ok)
	}
	for _, after := range []string{"claude/opus/high", "x/y/z"} {
		if c, ok := Choose(l, "debug", "debug_hard", nil, after); ok {
			t.Fatalf("after %s: got %+v", after, c)
		}
	}
}

func TestChooseUsesLearnedOrder(t *testing.T) {
	l := defaults(t)
	l.Learned = map[string]config.LearnedLadder{"debug": {Order: []string{"codex/sol/medium"}}}
	c, ok := Choose(l, "debug", "debug_hard", nil, "")
	if !ok || c.Entry.Key() != "codex/sol/medium" || c.Why != "learned" {
		t.Fatalf("got %+v, %v", c, ok)
	}
}

func TestChooseStepLadder(t *testing.T) {
	c, ok := Choose(defaults(t), "skeleton", "plan", nil, "")
	if !ok || c.Ladder != "skeleton.plan" || c.Entry.Key() != "claude/opus/high" {
		t.Fatalf("got %+v, %v", c, ok)
	}
}

func TestChooseRoleMismatch(t *testing.T) {
	c, ok := Choose(defaults(t), "tests", "review_final", nil, "")
	if !ok || !c.RoleMismatch || c.Entry.Key() != "codex/terra/medium" {
		t.Fatalf("got %+v, %v", c, ok)
	}
}
