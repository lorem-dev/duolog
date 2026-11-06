package delegate

import (
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/session"
)

func TestFinalLine(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 600)
	tests := []struct {
		name    string
		line    string
		notices []string
		want    string
	}{
		{"appends a notice", "done=true summary=x", []string{"N1"}, "done=true summary=x; N1"},
		{"keeps a line that holds the notice", "done=true summary=x; N1", []string{"N1"}, "done=true summary=x; N1"},
		{"empty line trims the lead", "", []string{"N1", "N2"}, "N1; N2"},
		{"no notices", "done=true", nil, "done=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := finalLine(tt.line, tt.notices, "d"); got != tt.want {
				t.Errorf("finalLine = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("cuts a long line and keeps the notice whole", func(t *testing.T) {
		t.Parallel()
		got := finalLine(long, []string{"N1"}, "d")
		if len(got) > maxLine {
			t.Errorf("len = %d, want at most %d", len(got), maxLine)
		}
		if !strings.HasSuffix(got, "]; N1") {
			t.Errorf("line %q does not end with %q", got, "]; N1")
		}
		if !strings.Contains(got, " ... [cut; "+answerPointer("d")+"]") {
			t.Errorf("line %q lacks the cut tail", got)
		}
	})
}

func TestCapabilityPlan(t *testing.T) {
	t.Parallel()
	t.Run("bridge", func(t *testing.T) {
		t.Parallel()
		p := capabilityPlan([]capability.Resolution{{Need: "mcp:srv", Action: capability.ActionBridge}})
		if len(p.Bridge) != 1 || p.Bridge[0] != "srv" {
			t.Errorf("Bridge = %v, want [srv]", p.Bridge)
		}
	})
	t.Run("refuse", func(t *testing.T) {
		t.Parallel()
		r := capability.Resolution{Need: "skill:x", Action: capability.ActionRefuse, Hint: "no skill x"}
		p := capabilityPlan([]capability.Resolution{r})
		if p.Refuse == nil || *p.Refuse != r {
			t.Errorf("Refuse = %v, want %v", p.Refuse, r)
		}
	})
	t.Run("lead first", func(t *testing.T) {
		t.Parallel()
		p := capabilityPlan([]capability.Resolution{{Need: "mcp:a", Action: capability.ActionLeadFirst, Hint: "ask the lead"}})
		if len(p.Lacking) != 1 || p.Lacking[0] != "mcp:a" || len(p.Notes) != 1 || p.Notes[0] != "ask the lead" {
			t.Errorf("Lacking = %v, Notes = %v", p.Lacking, p.Notes)
		}
	})
	t.Run("reroute", func(t *testing.T) {
		t.Parallel()
		p := capabilityPlan([]capability.Resolution{{Need: "mcp:a", Action: capability.ActionReroute, Hint: "other agent"}})
		if !p.Reroute || len(p.Notes) != 1 || p.Notes[0] != "other agent" {
			t.Errorf("Reroute = %v, Notes = %v", p.Reroute, p.Notes)
		}
	})
	t.Run("default with a hint", func(t *testing.T) {
		t.Parallel()
		p := capabilityPlan([]capability.Resolution{
			{Need: "mcp:a", Action: capability.ActionPresent, Hint: "present"},
			{Need: "mcp:b", Action: capability.ActionPresent},
		})
		if len(p.Notes) != 1 || p.Notes[0] != "present" || len(p.Bridge) != 0 || len(p.Lacking) != 0 || p.Reroute || p.Refuse != nil {
			t.Errorf("plan = %+v", p)
		}
	})
}

func TestKeepThread(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	limits := config.Sessions{ResumeMaxInputTokens: 150000, IdleCloseMinutes: 30}
	tests := []struct {
		name   string
		entry  session.Entry
		limits config.Sessions
		want   bool
	}{
		{"fresh and small", session.Entry{Input: 1000, UpdatedAt: now.Add(-time.Minute)}, limits, true},
		{"input at the limit", session.Entry{Input: 150000, UpdatedAt: now}, limits, true},
		{"input over the limit", session.Entry{Input: 150001, UpdatedAt: now}, limits, false},
		{"idle past the limit", session.Entry{Input: 1, UpdatedAt: now.Add(-31 * time.Minute)}, limits, false},
		{"idle close off", session.Entry{Input: 1, UpdatedAt: now.Add(-24 * time.Hour)}, config.Sessions{ResumeMaxInputTokens: 150000}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := keepThread(&tt.entry, tt.limits, now); got != tt.want {
				t.Errorf("keepThread = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInlineBudget(t *testing.T) {
	t.Parallel()
	t.Run("room left", func(t *testing.T) {
		t.Parallel()
		got, err := inlineBudget(1000, 100, 200)
		if err != nil || got != 700 {
			t.Errorf("inlineBudget = %d, %v, want 700, nil", got, err)
		}
	})
	t.Run("nothing left", func(t *testing.T) {
		t.Parallel()
		_, err := inlineBudget(300, 100, 200)
		if err == nil || !strings.Contains(err.Error(), "take 300 of wire.inline_max_bytes (300); shorten the brief") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("over", func(t *testing.T) {
		t.Parallel()
		if _, err := inlineBudget(100, 100, 200); err == nil {
			t.Error("err = nil, want an error")
		}
	})
}
