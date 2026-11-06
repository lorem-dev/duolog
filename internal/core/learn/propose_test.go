package learn

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

func unclassifiedRecords(n int) []journal.Delegation {
	var ds []journal.Delegation
	for i := 0; i < n; i++ {
		ds = append(ds, journal.Delegation{ID: fmt.Sprintf("d%d", i), Task: "t1", Role: "review_final", Class: Unclassified, RuleClass: Unclassified, Done: true})
	}
	return ds
}

func TestProposeClassAfterFiveUnclassified(t *testing.T) {
	cfg := reportConfig(t)
	ps := ProposeClasses(cfg, unclassifiedRecords(5), nil)
	if len(ps) != 1 {
		t.Fatal(ps)
	}
	p := ps[0]
	if p.Name != "review_final_misc" || p.Rule.Class != p.Name || !reflect.DeepEqual(p.Rule.When, map[string]string{"role": "review_final"}) || p.Class == nil {
		t.Fatalf("%+v", p)
	}
	c := p.Class
	if c.Priority != "cost" || !reflect.DeepEqual(c.Roles, []string{"review_final"}) || c.Description != "review_final work with output misc, proposed from 5 unclassified delegations" || !reflect.DeepEqual(c.Rubric, []string{"correct against the tests and the brief", "false claims (count)"}) {
		t.Fatalf("%+v", c)
	}
	role := cfg.Roles["review_final"]
	if len(c.Ladder) != 2 || c.Ladder[0].Target() != role.Use || c.Ladder[1].Target() != role.Fallback || c.Ladder[0].Prior != 2.9 || c.Ladder[1].Prior != 2.9 || p.Evidence == "" {
		t.Fatal(p)
	}
}

func TestNoProposalBelowThreshold(t *testing.T) {
	cfg := reportConfig(t)
	for _, kind := range []string{"four", "replay", "infra", "features", "role", "existing"} {
		t.Run(kind, func(t *testing.T) {
			ds := unclassifiedRecords(5)
			local := *cfg
			switch kind {
			case "four":
				ds = ds[:4]
			case "replay":
				ds[0].ProfileOf = "original"
			case "infra":
				ds[0].Outcome = journal.OutcomeInfra
			case "features":
				ds[0].Features = map[string]string{"exemplar": "yes"}
			case "role":
				ds[0].Role = "security_review"
			case "existing":
				local.Learning.Classes = map[string]config.Class{"review_final_misc": {}}
			}
			if ps := ProposeClasses(&local, ds, nil); len(ps) != 0 {
				t.Fatal(ps)
			}
		})
	}
}

func TestProposeClassUsesFullFeatureSet(t *testing.T) {
	cfg := reportConfig(t)
	ds := unclassifiedRecords(5)
	for i := range ds {
		ds[i].Features = map[string]string{"output": "review", "target": "plan"}
	}
	ps := ProposeClasses(cfg, ds, nil)
	if len(ps) != 1 || ps[0].Name != "review_final_review" || !reflect.DeepEqual(ps[0].Rule.When, map[string]string{"role": "review_final", "output": "review", "target": "plan"}) {
		t.Fatal(ps)
	}
}

func featureRecords(n int, features map[string]string) []journal.Delegation {
	ds := unclassifiedRecords(n)
	for i := range ds {
		ds[i].Features = features
	}
	return ds
}

func TestProposeClassOffersLargestGroupAndStableFeatureTie(t *testing.T) {
	cfg := reportConfig(t)
	yes := featureRecords(5, map[string]string{"exemplar": "yes"})
	no := featureRecords(6, map[string]string{"exemplar": "no"})
	for _, ds := range [][]journal.Delegation{append(append([]journal.Delegation{}, yes...), no...), append(append([]journal.Delegation{}, no...), yes...)} {
		ps := ProposeClasses(cfg, ds, nil)
		if len(ps) != 1 || ps[0].Rule.When["exemplar"] != "no" {
			t.Fatal(ps)
		}
	}
	yes = featureRecords(6, map[string]string{"exemplar": "yes"})
	ps := ProposeClasses(cfg, append(yes, no...), nil)
	if len(ps) != 1 || ps[0].Rule.When["exemplar"] != "no" {
		t.Fatal(ps)
	}
}

func TestProposeClassDeclineOnlySkipsOfferedGroup(t *testing.T) {
	cfg := reportConfig(t)
	ds := append(featureRecords(7, map[string]string{"exemplar": "yes"}), featureRecords(5, map[string]string{"exemplar": "no"})...)
	first := ProposeClasses(cfg, ds, nil)[0]
	declined := []string{first.DeclineKey()}
	ps := ProposeClasses(cfg, ds, declined)
	if len(ps) != 1 || ps[0].Name != first.Name || ps[0].Rule.When["exemplar"] != "no" {
		t.Fatal(ps)
	}
	// More observations of a declined group must not revive it or decline a
	// different group if their relative sizes change later.
	ds = append(ds, featureRecords(10, map[string]string{"exemplar": "yes"})...)
	ps = ProposeClasses(cfg, ds, declined)
	if len(ps) != 1 || ps[0].Rule.When["exemplar"] != "no" {
		t.Fatal(ps)
	}
	declined = append(declined, ps[0].DeclineKey())
	if ps := ProposeClasses(cfg, ds, declined); len(ps) != 0 {
		t.Fatal(ps)
	}
}

func TestProposeRuleCollisionUsesGroupCountsAndFeatures(t *testing.T) {
	cfg := reportConfig(t)
	cfg.Learning.Classify = []config.ClassRule{{When: map[string]string{"output": "diagnosis"}, Class: "debug"}}
	makeRuns := func(n int, components, rule string) []journal.Delegation {
		ds := make([]journal.Delegation, n)
		for i := range ds {
			ds[i] = journal.Delegation{Class: "debug_multi", StatedClass: "debug_multi", RuleClass: rule, Features: map[string]string{"components": components, "output": "diagnosis"}}
		}
		return ds
	}
	ds := append(makeRuns(3, "1", "debug"), makeRuns(4, "2+", "debug")...)
	ps := ProposeClasses(cfg, ds, nil)
	if len(ps) != 1 || ps[0].Name != "rule:debug_multi" || ps[0].Rule.When["components"] != "2+" {
		t.Fatal(ps)
	}
	ps = ProposeClasses(cfg, ds, []string{ps[0].DeclineKey()})
	if len(ps) != 1 || ps[0].Rule.When["components"] != "1" {
		t.Fatal(ps)
	}
	// Identical features from different disagreement pairs remain distinct.
	ds = append(makeRuns(3, "1", "debug"), makeRuns(4, "1", "implement")...)
	first := ProposeClasses(cfg, ds, nil)[0]
	ps = ProposeClasses(cfg, ds, []string{first.DeclineKey()})
	if len(ps) != 1 || ps[0].DeclineKey() == first.DeclineKey() {
		t.Fatal(ps)
	}
}

func TestProposeRuleForRepeatedDisagreement(t *testing.T) {
	cfg := reportConfig(t)
	cfg.Learning.Classify = []config.ClassRule{{When: map[string]string{"output": "diagnosis"}, Class: "debug"}}
	var ds []journal.Delegation
	for i := 0; i < 3; i++ {
		ds = append(ds, journal.Delegation{Role: "debug_hard", Class: "debug_multi", StatedClass: "debug_multi", RuleClass: "debug", Features: map[string]string{"output": "diagnosis", "components": "2+"}})
	}
	ps := ProposeClasses(cfg, ds, nil)
	if len(ps) != 1 || ps[0].Name != "rule:debug_multi" || ps[0].Class != nil || ps[0].Rule.Class != "debug_multi" || !reflect.DeepEqual(ps[0].Rule.When, ds[0].Features) {
		t.Fatal(ps)
	}
	ds[0].ProfileOf = "original"
	if ps := ProposeClasses(cfg, ds, nil); len(ps) != 0 {
		t.Fatal(ps)
	}
	ds[0].ProfileOf = ""
	ds[0].RuleClass = "implement"
	if ps := ProposeClasses(cfg, ds, nil); len(ps) != 0 {
		t.Fatal(ps)
	}
}

func TestDeclinedProposalNotRepeated(t *testing.T) {
	cfg := reportConfig(t)
	if ps := ProposeClasses(cfg, unclassifiedRecords(5), []string{"review_final_misc"}); len(ps) != 0 {
		t.Fatal(ps)
	}
	ds := unclassifiedRecords(3)
	for i := range ds {
		ds[i].Class = "debug_multi"
		ds[i].StatedClass = "debug_multi"
		ds[i].RuleClass = "debug"
	}
	if ps := ProposeClasses(cfg, ds, []string{"rule:debug_multi"}); len(ps) != 0 {
		t.Fatal(ps)
	}
}
