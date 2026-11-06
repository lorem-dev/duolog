package policy

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestOverrideGlobsCannotReadState(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{
		"rg -g .duolog -g bus.ndjson -g decisions.ndjson . .",
		"rg --iglob=.DUOLOG x .", "rg --glob .duolog x .",
		"rg -g{.duolog,*.ndjson} x .",
		"rg -g* x .", "rg --glob=** x .",
		"rg -g '*o*' x .", "rg -g '*g' -g '*.ndjson' x .",
		"rg -g '.d*/**' x .", "rg -g '[.]duolog' x .",
		"rg -g '?duolog' x .", "rg -g '{*.go,*O*}' x .",
	} {
		t.Run(command, func(t *testing.T) {
			d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root})
			if d.Kind != Deny || d.Rule != RuleState {
				t.Fatal(d)
			}
		})
	}
	for _, command := range []string{"rg x .", "rg -g '*.go' x .", "rg -g '*.go' -g '!*.txt' x ."} {
		check(t, p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root}, Allow)
	}
}

func TestUncertainWordsKeepNamedStatePaths(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, command := range []string{"cat .duolog/decisions.ndjson x*", "cat .duolog/*.ndjson", "cat .duolog/decisions.ndjson ~someone/file", "rg t .d*"} {
		t.Run(command, func(t *testing.T) {
			d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: root, Cwd: root, State: root})
			if d.Kind != Deny || d.Rule != RuleState {
				t.Fatal(d)
			}
		})
	}
	targets, reason := readCommandTargets("cat .duolog/decisions.ndjson x*")
	if len(targets) != 2 || reason == "" {
		t.Fatal(targets, reason)
	}
}

func TestProfileWorkRootDoesNotCountAsState(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	work := filepath.Join(root, ".duolog", "profile", "worktree")
	for _, command := range []string{"rg token internal", "rg x .", "grep -r x src"} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: command, Role: "implement", Work: work, Cwd: work, State: root})
		if d.Kind == Deny && d.Rule == RuleState {
			t.Errorf("%q: %+v", command, d)
		}
	}
	d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: "rg x " + filepath.ToSlash(filepath.Join(root, ".duolog")), Role: "implement", Work: work, Cwd: work, State: root})
	if d.Kind != Deny || d.Rule != RuleState {
		t.Fatal(d)
	}
}

func TestGrepToolCannotSearchState(t *testing.T) {
	p := policy(t)
	root := t.TempDir()
	for _, tc := range []struct{ path, glob string }{{".", "{.duolog,*.ndjson}"}, {".", "*o*"}, {".duolog", ""}, {"sub/.duolog", ""}} {
		data, _ := json.Marshal(map[string]any{"tool_name": "Grep", "tool_input": map[string]string{"path": tc.path, "glob": tc.glob, "pattern": "x"}})
		a, err := Normalize("claude", data)
		if err != nil {
			t.Fatal(err)
		}
		a.Role, a.Work, a.Cwd, a.State = "implement", root, root, root
		d := Evaluate(p, LeadRules{}, mode.Edits, a)
		if d.Kind != Deny || d.Rule != RuleState {
			t.Errorf("%+v: %+v", tc, d)
		}
	}
}

func TestExecOutsideAskNeedsPerson(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "exec", Match: "git*", Decision: Ask}, {Action: "exec", Match: "go test*", Decision: Ask}}, p.Rules...)
	root := t.TempDir()
	for _, tc := range []struct {
		command string
		person  bool
	}{
		{"cp ~/.ssh/k k", true}, {"tar czf x.tgz ~/notes", true}, {"git -C " + absoluteTestPath(root, "/other") + " log", true},
		{"/other/tool", false},
		{"go test ./...", false}, {"git log", false},
	} {
		d := Evaluate(p, LeadRules{}, mode.Edits, Action{Kind: "exec", Target: tc.command, Role: "implement", Work: root, Cwd: root})
		if d.Kind != Ask || PersonOnly(d) != tc.person {
			t.Errorf("%q: %+v, person=%v", tc.command, d, tc.person)
		}
	}
}
