package delegate

import (
	"slices"
	"testing"
)

// review_cross is a Codex review: no default class lists it, and a class the
// lead names does not move it to another agent.
func TestReviewCrossStaysOnCodex(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	for name, c := range h.cfg.Learning.Classes {
		if slices.Contains(c.Roles, "review_cross") {
			t.Errorf("class %s lists review_cross", name)
		}
	}
	for _, class := range []string{"plan_review", "review"} {
		h.calls = nil
		out, err := h.runWith("review_cross", Options{Class: class})
		if err != nil {
			t.Fatal(out, err)
		}
		h.requireCalls(1)
		if r := h.calls[0].Route; r.Agent != "codex" {
			t.Fatalf("class %s: %+v", class, r)
		}
	}
}
