package learn

import (
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/record"
)

func TestReviewNonQualityRunsGetNoQualityOrOffers(t *testing.T) {
	cfg := reportConfig(t)
	for _, flag := range []string{`"run_here":true`, `"interrupted":true`, `"needs":["question"]`} {
		t.Run(flag, func(t *testing.T) {
			d := live()
			if err := json.Unmarshal([]byte("{"+flag+"}"), &d); err != nil {
				t.Fatal(err)
			}
			d.Done = false
			if st := Aggregate(cfg, []record.Delegation{d}, accepted(), nil); len(st) != 0 {
				t.Fatalf("non-quality run counted: %+v", st)
			}
			if offers := Offers(cfg, []record.Delegation{d}, nil, nil); len(offers) != 0 {
				t.Fatal(offers)
			}
		})
	}
}

func TestReviewCappedStatsUseActualEffort(t *testing.T) {
	d := live()
	d.Entry = "codex/sol/high"
	d.EffortCapped, d.Effort = true, "medium"
	st := Aggregate(nil, []record.Delegation{d}, accepted(), nil)["debug"]
	if st["codex/sol/medium"].Delegations != 1 || st[d.Entry].Delegations != 0 {
		t.Fatal(st)
	}
}
