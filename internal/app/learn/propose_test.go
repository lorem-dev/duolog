package learn

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	corelearn "github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/core/record"
	"go.yaml.in/yaml/v3"
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

func unclassifiedRecords(n int) []record.Delegation {
	var ds []record.Delegation
	for i := 0; i < n; i++ {
		ds = append(ds, record.Delegation{ID: fmt.Sprintf("d%d", i), Task: "t1", Role: "review_final", Class: corelearn.Unclassified, RuleClass: corelearn.Unclassified, Done: true})
	}
	return ds
}

func featureRecords(n int, features map[string]string) []record.Delegation {
	ds := unclassifiedRecords(n)
	for i := range ds {
		ds[i].Features = features
	}
	return ds
}

func TestProposeClassOffersNextGroupAfterApply(t *testing.T) {
	root, cfg := proposalRoot(t, "")
	ds := append(featureRecords(7, map[string]string{"exemplar": "yes"}), featureRecords(5, map[string]string{"exemplar": "no"})...)
	first := corelearn.ProposeClasses(cfg, ds, nil)[0]
	if _, _, err := ApplyClass(root, cfg, first); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ps := corelearn.ProposeClasses(loaded, ds, nil)
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
	if ps := corelearn.ProposeClasses(loaded, ds, nil); len(ps) != 0 {
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
	p := corelearn.ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
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
	if got := corelearn.Classify(loaded.Learning.Classify, "review_final", nil); got != p.Name {
		t.Fatal(got)
	}
	if loaded.Learning.Classes[p.Name].Priority != "cost" || len(loaded.Learning.Classify) != len(cfg.Learning.Classify)+1 {
		t.Fatal(loaded.Learning)
	}
}

func TestApplyClassInsertsRuleBeforeFirstMatch(t *testing.T) {
	root, cfg := proposalRoot(t, "learning:\n  classify:\n    - when: {output: diagnosis}\n      class: debug # existing diagnosis rule\n    - when: {output: code}\n      class: implement\n")
	p := corelearn.ClassProposal{Name: "rule:debug_multi", Rule: config.ClassRule{When: map[string]string{"output": "diagnosis", "components": "2+"}, Class: "debug_multi"}}
	_, _, err := ApplyClass(root, cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Learning.Classify) != 3 || loaded.Learning.Classify[0].Class != "debug_multi" || loaded.Learning.Classify[1].Class != "debug" || corelearn.Classify(loaded.Learning.Classify, "debug_hard", p.Rule.When) != "debug_multi" || corelearn.Classify(loaded.Learning.Classify, "debug_hard", map[string]string{"output": "diagnosis"}) != "debug" {
		t.Fatal(loaded.Learning.Classify)
	}
}

func TestApplyClassCreatesConfigWithEffectiveRules(t *testing.T) {
	root, cfg := proposalRoot(t, "")
	p := corelearn.ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
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
	if got := corelearn.Classify(loaded.Learning.Classify, "implement", map[string]string{"output": "code", "exemplar": "yes"}); got != corelearn.Classify(cfg.Learning.Classify, "implement", map[string]string{"output": "code", "exemplar": "yes"}) {
		t.Fatal(got)
	}
}

func TestApplyClassRestoresOnLoadError(t *testing.T) {
	for _, contents := range []string{"# preserve bytes\nlearning: {enabled: true}\n", ""} {
		root, cfg := proposalRoot(t, contents)
		p := corelearn.ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
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
	p := corelearn.ProposeClasses(cfg, unclassifiedRecords(5), nil)[0]
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
