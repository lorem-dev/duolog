package delegate

import (
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/runner"
)

func TestReviewNonQualityOutcomeRecorded(t *testing.T) {
	for _, kind := range []string{"here", "interrupted", "needs"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			h.learning(true)
			if kind == "here" {
				h.lead("claude", mode.Full, "high")
				spec := h.cfg.Roles["planner"]
				spec.Context = "minimal"
				h.cfg.Roles["planner"] = spec
				h.cfg.Models["opus"] = h.cfg.Models["haiku"]
			}
			if kind == "interrupted" {
				h.results = []runner.Result{{Interrupted: true}}
			}
			if kind == "needs" {
				h.results = []runner.Result{{Done: false, Needs: []string{"clarify"}}}
			}
			role := "implement"
			opts := Options{Class: "implement"}
			if kind == "here" {
				role = "planner"
				opts.Class = "feature"
			}
			_, _ = h.runWith(role, opts)
			if r := h.record(); r.Outcome != journal.OutcomeLead {
				t.Fatalf("%s: %+v", kind, r)
			}
		})
	}
}

func TestReviewForcedEntryNeverFallsBack(t *testing.T) {
	for _, kind := range []string{"person", "profile", "judge", "quota"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t)
			o := Options{Entry: target("codex/sol/high")}
			switch kind {
			case "person":
				o.Person = true
			case "profile":
				o.ReplayOf = "original"
			case "judge":
				o.Judge = true
			case "quota":
				h.results = []runner.Result{{QuotaEnded: true}}
			}
			if kind != "quota" {
				h.unavailable("codex")
			}
			out, err := h.runWith("implement", o)
			if err == nil || !strings.Contains(strings.ToLower(out.Line+err.Error()), "ask the person") {
				t.Fatal(out, err)
			}
			want := 0
			if kind == "quota" {
				want = 1
			}
			h.requireCalls(want)
		})
	}
}

func TestReviewEscalationSkipsSameCappedTarget(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.lead("codex", mode.Full, "medium")
	if _, err := h.runWith("implement", Options{Class: "feature"}); err != nil {
		t.Fatal(err)
	}
	left := h.record()
	c := h.cfg.Learning.Classes["feature"]
	c.Ladder = append(c.Ladder, config.LadderEntry{Agent: "claude", Model: "opus", Effort: "high"})
	h.cfg.Learning.Classes["feature"] = c
	out, err := h.runWith("implement", Options{EscalateFrom: left.ID, EscalateReason: "not_green"})
	if err != nil {
		t.Fatal(out, err)
	}
	h.requireCalls(2)
	r := h.calls[1].Route
	if r.Agent != "claude" || route.Normalize(r.Agent, r.Model) != "opus" || r.Effort != "medium" {
		t.Fatal(r)
	}
	recs := h.records()
	if recs[1].Entry != "claude/opus/medium" || !strings.Contains(out.Line, "capped") {
		t.Fatal(recs[1], out.Line)
	}
}

func TestReviewCappedRunCanEscalateFromEffectiveEntry(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	h.lead("codex", mode.Full, "low")
	if _, err := h.runWith("implement", Options{Class: "implement"}); err != nil {
		t.Fatal(err)
	}
	left := h.record()
	if left.Entry != "codex/sol/low" {
		t.Fatal(left)
	}
	_, err := h.runWith("implement", Options{EscalateFrom: left.ID, EscalateReason: "not_green"})
	if err != nil {
		t.Fatal(err)
	}
	h.requireCalls(2)
	if h.calls[1].Route.Agent != "claude" {
		t.Fatal(h.calls[1].Route)
	}
}

func TestReviewDebugRoleMatchesDebugClass(t *testing.T) {
	h := newHarness(t)
	h.learning(true)
	out, err := h.runWith("debug", Options{Features: diagnosis()})
	if err != nil {
		t.Fatal(out, err)
	}
	if r := h.record(); r.RoleMismatch || strings.Contains(out.Line, "does not list role") {
		t.Fatal(r, out.Line)
	}
}

func TestReviewImplicitTaskNoteSaysToFinish(t *testing.T) {
	h := newHarness(t)
	h.learning(false)
	out, err := h.runWith("implement", Options{Class: "implement"})
	if err != nil || !strings.Contains(out.Line, "run duolog task finish when this task ends") || strings.Contains(out.Line, "task start next time") {
		t.Fatal(out, err)
	}
}
