package gate

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func TestOwnToolResults(t *testing.T) {
	claude := filepath.Join(t.TempDir(), ".claude")
	own := filepath.Join(claude, "projects", "-work-app", "sess-1", "tool-results")
	cases := []struct {
		name, session, target string
		want                  bool
	}{
		{"own folder", "sess-1", own, true},
		{"own file", "sess-1", filepath.Join(own, "out.txt"), true},
		{"another session", "sess-2", own, false},
		{"another session folder", "sess-1", filepath.Join(claude, "projects", "-work-app", "sess-2", "tool-results", "out.txt"), false},
		{"other file of the session", "sess-1", filepath.Join(claude, "projects", "-work-app", "sess-1", "sess-1.jsonl"), false},
		{"settings", "sess-1", filepath.Join(claude, "settings.json"), false},
		{"credentials", "sess-1", filepath.Join(claude, ".credentials.json"), false},
		{"projects folder", "sess-1", filepath.Join(claude, "projects"), false},
		{"dotdot escape", "sess-1", own + "/../../../../settings.json", false},
		{"dotdot project", "sess-1", filepath.Join(claude, "projects", "..", "sess-1", "tool-results"), false},
		{"no session id", "", own, false},
		{"relative", "sess-1", "tool-results/out.txt", false},
	}
	for _, c := range cases {
		if got := OwnToolResults(claude, c.session, c.target); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if OwnToolResults("", "sess-1", own) {
		t.Error("an empty config folder allowed a read")
	}
}

func TestOwnResultSkipsTheOutsideAsk(t *testing.T) {
	p := policy(t)
	outsideRead := Action{Kind: "read", Target: "../../.claude/projects/-work-app/sess-1/tool-results/out.txt", Role: "worker"}
	check(t, p, LeadRules{}, mode.Ask, outsideRead, Ask)
	own := outsideRead
	own.OwnResult = true
	check(t, p, LeadRules{}, mode.Ask, own, Allow)
	// The flag grants reads only: a write is not allowed by it.
	write := own
	write.Kind = "write"
	if d := Evaluate(p, LeadRules{}, mode.Ask, write); d.Kind == Allow {
		t.Fatalf("the flag allowed a write: %+v", d)
	}
}
