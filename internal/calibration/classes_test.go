package calibration

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

// unmatched returns n candidates of one role and feature set that no default
// rule classifies.
func unmatched(n int) []Candidate {
	out := make([]Candidate, n)
	for i := range out {
		out[i] = Candidate{ID: fmt.Sprintf("k-%d", i), Role: "implement", Features: map[string]string{"output": "config", "scope": "1-3", "components": "1"}}
	}
	return out
}

func TestProposeClassesFromCandidatesNeedsFive(t *testing.T) {
	cfg := testConfig(t)
	if p := ProposeClassesFrom(cfg, unmatched(4), Survey{}); len(p.Proposals) != 0 {
		t.Fatalf("4 candidates: Proposals = %+v, want none", p.Proposals)
	}
	excluded := unmatched(5)
	excluded[4].Excluded = "revert"
	if p := ProposeClassesFrom(cfg, excluded, Survey{}); len(p.Proposals) != 0 {
		t.Fatalf("4 candidates and an excluded one: Proposals = %+v, want none", p.Proposals)
	}
	p := ProposeClassesFrom(cfg, unmatched(5), Survey{})
	if len(p.Proposals) != 1 || p.Proposals[0].Name != "implement_config" {
		t.Fatalf("5 candidates: Proposals = %+v, want implement_config", p.Proposals)
	}
	if len(p.Checks) != 0 {
		t.Fatalf("Checks = %v, want none without survey checks", p.Checks)
	}
}

func TestApplyClassesWritesChecks(t *testing.T) {
	cfg := testConfig(t)
	root := t.TempDir()
	s := Survey{Checks: map[string][]string{"implement": {"go vet ./...", "go test ./..."}}}
	p := ProposeClassesFrom(cfg, nil, s)
	old, updated, err := ApplyClasses(root, cfg, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(old, updated) {
		t.Fatalf("old == updated: %s", updated)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Learning.Classes["implement"].Checks; !reflect.DeepEqual(got, []string{"go vet ./...", "go test ./..."}) {
		t.Fatalf("Checks[implement] = %v, want [go vet ./... go test ./...]", got)
	}
}
