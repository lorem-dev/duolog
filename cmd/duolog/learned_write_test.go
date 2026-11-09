package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

var testRule = config.Rule{Action: "exec", Match: "make lint", Decision: "allow", Roles: []string{"implement"}}

const testRuleText = "- action: exec\n  match: make lint\n  decision: allow\n  roles:\n    - implement\n"

func appendTo(t *testing.T, before string) (string, bool, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "learned.yaml")
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := addLearnedRule(path, testRule)
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data), added, err
}

func TestLearnedAppendKeepsEveryExistingByte(t *testing.T) {
	before := "# my notes\n\nrules:\n  # keep me\n  - {action: exec, match: 'make x', decision: allow, roles: [a]}\n\n  - action: \"exec\"\n    match: \"make y\"\n    decision: allow\n    roles: [b]\n# tail\n"
	got, added, err := appendTo(t, before)
	if err != nil || !added {
		t.Fatal(added, err)
	}
	want := before + "  - action: exec\n    match: make lint\n    decision: allow\n    roles:\n      - implement\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLearnedAppendKeepsCRLFAndAddsMissingNewline(t *testing.T) {
	before := "rules:\r\n- action: exec\r\n  match: make x\r\n  decision: allow\r\n  roles: [a]"
	got, added, err := appendTo(t, before)
	if err != nil || !added {
		t.Fatal(added, err)
	}
	want := before + "\r\n" + strings.ReplaceAll(testRuleText, "\n", "\r\n")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLearnedAppendCreatesOrExtends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "learned.yaml")
	if _, err := addLearnedRule(path, testRule); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "# Commands a person allowed") || !strings.HasSuffix(string(data), "rules:\n"+testRuleText) {
		t.Fatalf("new file:\n%s", data)
	}
	got, added, err := appendTo(t, "# only a comment\n")
	if err != nil || !added || got != "# only a comment\nrules:\n"+testRuleText {
		t.Fatalf("%v %v\n%s", added, err, got)
	}
	got, added, err = appendTo(t, "")
	if err != nil || !added || !strings.HasSuffix(got, "rules:\n"+testRuleText) {
		t.Fatalf("%v %v\n%s", added, err, got)
	}
}

func TestLearnedAppendSavedRuleChangesNothing(t *testing.T) {
	before := "# c\nrules:\n  - {action: exec, match: make lint, decision: allow, roles: [implement]}\n"
	got, added, err := appendTo(t, before)
	if err != nil || added || got != before {
		t.Fatal(added, err, got)
	}
}

func TestLearnedAppendRefusesWhatItCannotKeep(t *testing.T) {
	for name, before := range map[string]string{
		"broken yaml":    "rules: [\n",
		"flow list":      "rules: []\n",
		"rules not last": "rules:\n  - {action: exec, match: x, decision: allow, roles: [a]}\nother: 1\n",
		"not a mapping":  "- a\n- b\n",
	} {
		got, added, err := appendTo(t, before)
		if err == nil || added || got != before {
			t.Errorf("%s: added=%v err=%v file=%q", name, added, err, got)
		}
	}
}
