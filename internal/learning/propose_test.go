package learning

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"go.yaml.in/yaml/v3"
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

func TestProposeClassOffersNextGroupAfterApply(t *testing.T) {
	root, cfg := proposalRoot(t, "")
	ds := append(featureRecords(7, map[string]string{"exemplar": "yes"}), featureRecords(5, map[string]string{"exemplar": "no"})...)
	first := ProposeClasses(cfg, ds, nil)[0]
	if _, _, err := ApplyClass(root, cfg, first); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ps := ProposeClasses(loaded, ds, nil)
	if len(ps) != 1 || ps[0].Name != first.Name || ps[0].Class != nil || ps[0].Rule.When["exemplar"] != "no" {
		t.Fatal(ps)
	}
	if _, _, err := ApplyClass(root, loaded, ps[0]); err != nil {
		t.Fatal(err)
	}
	loaded, err = config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if ps := ProposeClasses(loaded, ds, nil); len(ps) != 0 {
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

func proposalRoot(t *testing.T, contents string) (string, *config.Config) {
	t.Helper()
	defaults(t)
	root := t.TempDir()
	if contents != "" {
		if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, cfg
}

func TestApplyClassKeepsCommentsAndLoads(t *testing.T) {
	contents := "# human note\nlearning:\n  enabled: true # keep switch\n  classify:\n    # keep classifier\n    - when: {output: diagnosis}\n      class: debug # keep rule\n"
	root, cfg := proposalRoot(t, contents)
	p := ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
	old, updated, err := ApplyClass(root, cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != contents {
		t.Fatal(string(old))
	}
	for _, comment := range []string{"# human note", "# keep switch", "# keep classifier", "# keep rule"} {
		if !bytes.Contains(updated, []byte(comment)) {
			t.Fatalf("lost %q: %s", comment, updated)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".duolog", "config.yaml"))
	if err != nil || !bytes.Equal(data, updated) {
		t.Fatal(string(data), err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := Classify(loaded.Learning.Classify, "review_final", nil); got != p.Name {
		t.Fatal(got)
	}
	if loaded.Learning.Classes[p.Name].Priority != "cost" || len(loaded.Learning.Classify) != len(cfg.Learning.Classify)+1 {
		t.Fatal(loaded.Learning)
	}
}

func TestApplyClassInsertsRuleBeforeFirstMatch(t *testing.T) {
	root, cfg := proposalRoot(t, "learning:\n  classify:\n    - when: {output: diagnosis}\n      class: debug # existing diagnosis rule\n    - when: {output: code}\n      class: implement\n")
	p := ClassProposal{Name: "rule:debug_multi", Rule: config.ClassRule{When: map[string]string{"output": "diagnosis", "components": "2+"}, Class: "debug_multi"}}
	_, _, err := ApplyClass(root, cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Learning.Classify) != 3 || loaded.Learning.Classify[0].Class != "debug_multi" || loaded.Learning.Classify[1].Class != "debug" || Classify(loaded.Learning.Classify, "debug_hard", p.Rule.When) != "debug_multi" || Classify(loaded.Learning.Classify, "debug_hard", map[string]string{"output": "diagnosis"}) != "debug" {
		t.Fatal(loaded.Learning.Classify)
	}
}

func TestApplyClassCreatesConfigWithEffectiveRules(t *testing.T) {
	root, cfg := proposalRoot(t, "")
	p := ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
	old, updated, err := ApplyClass(root, cfg, p)
	if err != nil || len(old) != 0 {
		t.Fatal(string(old), err)
	}
	if !strings.HasPrefix(string(updated), "# yaml-language-server: $schema=https://raw.githubusercontent.com/lorem-dev/duolog/main/duolog-config.schema.json\n") {
		t.Fatal(string(updated))
	}
	loaded, err := config.Load(root)
	if err != nil || len(loaded.Learning.Classify) != len(cfg.Learning.Classify)+1 {
		t.Fatal(loaded, err)
	}
	if got := Classify(loaded.Learning.Classify, "implement", map[string]string{"output": "code", "exemplar": "yes"}); got != Classify(cfg.Learning.Classify, "implement", map[string]string{"output": "code", "exemplar": "yes"}) {
		t.Fatal(got)
	}
}

func TestApplyClassRestoresOnLoadError(t *testing.T) {
	for _, contents := range []string{"# preserve bytes\nlearning: {enabled: true}\n", ""} {
		root, cfg := proposalRoot(t, contents)
		p := ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
		p.Class.Ladder[0].Model = "unknown-model"
		_, _, err := ApplyClass(root, cfg, p)
		if err == nil || !strings.Contains(err.Error(), "unknown-model") {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(root, ".duolog", "config.yaml"))
		if contents == "" {
			if !os.IsNotExist(err) {
				t.Fatal("failed apply left a config", err)
			}
		} else if err != nil || string(data) != contents {
			t.Fatal(string(data), err)
		}
	}
}

func TestApplyClassRestoresWhenLocalLayerMasksRule(t *testing.T) {
	contents := "# keep project bytes\nlearning: {enabled: true}\n"
	root, _ := proposalRoot(t, contents)
	local := filepath.Join(root, ".duolog", "config.local.yaml")
	if err := os.WriteFile(local, []byte("learning:\n  classify: [{when: {}, class: docs}]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	p := ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
	if _, _, err := ApplyClass(root, cfg, p); err == nil {
		t.Fatal("apply succeeded although a later layer hides the new rule")
	}
	data, err := os.ReadFile(filepath.Join(root, ".duolog", "config.yaml"))
	if err != nil || string(data) != contents {
		t.Fatal("ineffective apply changed project config", string(data), err)
	}
}

func TestEditConfigKeepsComments(t *testing.T) {
	root, _ := proposalRoot(t, "# team note\nlearning:\n  enabled: true\n")
	_, updated, err := EditConfig(root, func(l *yaml.Node) error {
		return SetClassChecks(l, "implement", []string{"go test ./..."})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(updated, []byte("# team note")) {
		t.Fatalf("lost the comment: %s", updated)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Learning.Classes["implement"].Checks; !reflect.DeepEqual(got, []string{"go test ./..."}) {
		t.Fatalf("Checks = %v, want [go test ./...]", got)
	}
}
