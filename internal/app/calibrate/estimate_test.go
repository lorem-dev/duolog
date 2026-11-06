package calibration

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/journal"
)

// scoped is a candidate of class implement with the given scope feature.
func scoped(scope string) Candidate {
	return Candidate{Class: "implement", Files: files("a", 2, 40), Lines: 40, Components: []string{"a"},
		Features: map[string]string{"output": "code", "scope": scope, "components": "1"}}
}

// delegations returns quality runs of class with these token totals, each
// split into input and output, and 600 seconds each.
func delegations(class string, tokens ...int64) []journal.Delegation {
	var ds []journal.Delegation
	for _, n := range tokens {
		ds = append(ds, journal.Delegation{Class: class, Input: n - n/4, Output: n / 4, Seconds: 600})
	}
	return ds
}

func TestEstimateDefaultsByScope(t *testing.T) {
	cfg := testConfig(t).Calibration
	cases := []struct {
		scope     string
		low, high int64
		minutes   float64
	}{
		{"1-3", 80000, 250000, 8},
		{"4-20", 200000, 600000, 20},
		{"20+", 500000, 1500000, 40},
	}
	for _, tc := range cases {
		e := EstimateTask(scoped(tc.scope), nil, 2.5, cfg)
		if e.TokensLow != tc.low || e.TokensHigh != tc.high || e.Minutes != tc.minutes {
			t.Errorf("scope %s: %d-%d tokens, %v minutes, want %d-%d, %v", tc.scope, e.TokensLow, e.TokensHigh, e.Minutes, tc.low, tc.high, tc.minutes)
		}
		if e.Files != 2 || e.Lines != 40 || e.Components != 1 || e.CheckSeconds != 2.5 {
			t.Errorf("scope %s: estimate %+v, want 2 files, 40 lines, 1 component, 2.5 s of checks", tc.scope, e)
		}
	}
}

func TestEstimateUsesJournalMeans(t *testing.T) {
	ds := delegations("implement", 100000, 200000, 300000)
	// Records that say nothing about the class's real work are left out.
	ds = append(ds,
		journal.Delegation{Class: "implement", Input: 9000000, Outcome: journal.OutcomeLead},
		journal.Delegation{Class: "implement", Input: 9000000, ProfileOf: "d-1"},
	)
	means := JournalMeans(ds)
	if m := means["implement"]; m.N != 3 || m.Tokens != 200000 || m.Seconds != 600 {
		t.Fatalf("means = %+v, want 3 records, 200000 tokens, 600 s", m)
	}
	e := EstimateTask(scoped("1-3"), means, 0, testConfig(t).Calibration)
	if e.TokensLow != 200000 || e.TokensHigh != 400000 || e.Minutes != 10 {
		t.Fatalf("estimate %d-%d tokens, %v minutes, want 200000-400000, 10", e.TokensLow, e.TokensHigh, e.Minutes)
	}

	more := JournalMeans(delegations("implement", 1000000, 900000, 800000, 700000, 600000, 500000, 400000, 300000, 200000, 100000))
	if e := EstimateTask(scoped("1-3"), more, 0, testConfig(t).Calibration); e.TokensHigh != 900000 {
		t.Fatalf("with 10 records TokensHigh = %d, want the 90th percentile 900000", e.TokensHigh)
	}
}

func TestLargeTaskAndCellCap(t *testing.T) {
	cfg := testConfig(t)
	if cfg.Calibration.CellMaxTokens != 1500000 {
		t.Fatalf("CellMaxTokens = %d, want the default 1500000", cfg.Calibration.CellMaxTokens)
	}
	c := scoped("1-3")
	e := EstimateTask(c, JournalMeans(delegations("implement", 300000, 450000, 600000)), 0, cfg.Calibration)
	if e.TokensHigh != 900000 || !e.Large {
		t.Fatalf("high %d Large %v, want 900000 and Large", e.TokensHigh, e.Large)
	}

	c.ID = "k-over"
	means := JournalMeans(delegations("implement", 600000, 800000, 1000000))
	m, err := Mine(context.Background(), MineInput{Cfg: cfg, Survey: bigSurvey(), Candidates: []Candidate{c},
		Means: means, Now: testNow, Validate: validAll})
	if err != nil {
		t.Fatal(err)
	}
	if got := reason(m, "k-over"); got != "estimate above the cell cap" {
		t.Fatalf("high 1600000: reason %q, want estimate above the cell cap", got)
	}
}
