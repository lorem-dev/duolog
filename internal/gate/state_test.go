package gate

import (
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
)

func TestStateReadDeniesLogs(t *testing.T) {
	for _, target := range []string{
		".duolog/decisions.ndjson", ".duolog/requests.ndjson", ".duolog/bus.ndjson",
		".duolog/usage.ndjson", ".duolog/sessions.json", ".duolog/cursors/lead",
		".duolog/journal/tasks.ndjson", ".duolog/routing.learned.yaml",
		".duolog/tasks/task-1/stdout.log", ".duolog/tasks/task-1/answer.json",
		".DUOLOG/decisions.ndjson", "sub/.duolog/bus.ndjson",
		"/abs/main/.duolog/decisions.ndjson", `C:\abs\main\.duolog\bus.ndjson`,
	} {
		if !StateRead(target) {
			t.Error(target)
		}
	}
}

func TestRecursiveReadOfStateDenied(t *testing.T) {
	p := policy(t)
	state := t.TempDir()
	a := func(cmd string) Action {
		return Action{Kind: "exec", Target: cmd, Role: "implement", Work: state, Cwd: state, State: state}
	}
	for _, cmd := range []string{"grep -r token .", "grep -rn token .", "grep -R token .duolog", "rg --hidden token .", "rg -uu token .", "rg --no-ignore token ."} {
		check(t, p, LeadRules{}, mode.Full, a(cmd), Deny)
	}
	for _, cmd := range []string{"rg token .", "grep token README.md"} {
		check(t, p, LeadRules{}, mode.Full, a(cmd), Allow)
	}
	check(t, p, LeadRules{}, mode.Full, a("grep -r token internal"), Ask)
}

func TestCoversState(t *testing.T) {
	state := t.TempDir()
	work := filepath.Join(state, "work")
	for _, tc := range []struct {
		cwd, target string
		want        bool
	}{
		{state, ".", true},
		{state, ".duolog", true},
		{state, "internal", false},
		{state, ".duolog-other", false},
		{work, "..", true},
		{work, ".", false},
		{"", ".", false},
		{work, state, true},
		{work, filepath.Join(state, ".duolog"), true},
	} {
		if got := coversState(work, tc.cwd, state, tc.target); got != tc.want {
			t.Errorf("cwd %q target %q: %v, want %v", tc.cwd, tc.target, got, tc.want)
		}
	}
	if coversState(work, state, "", ".") {
		t.Fatal("empty state must skip the check")
	}
	if !coversState(`C:\project\work`, `C:\project`, `c:\PROJECT`, ".") {
		t.Fatal("Windows ancestor must cover state")
	}
	if coversState(`C:\project`, `C:\project`, `D:\project`, ".") {
		t.Fatal("another drive must not cover state")
	}
}

func TestRecursiveStateSearchOptions(t *testing.T) {
	p := policy(t)
	state := t.TempDir()
	for _, command := range []string{
		"grep --recursive token .", "grep --dereference-recursive token .",
		"grep -d recurse token .", "grep --directories=recurse token",
		"grep --directories recurse token .", "rg -. token .",
		"rg -u token .", "rg -uuu token .", "rg --unrestricted token .",
		"rg --no-ignore-vcs token .", "rg --hidden token",
		"rg --hidden --regexp=token", "rg --hidden -f patterns.txt",
		"grep -r token internal .", "true && grep -r token .",
	} {
		d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement", Work: state, State: state})
		if d.Kind != Deny || d.Rule != RuleState {
			t.Errorf("%q: %+v", command, d)
		}
	}
}

func TestRecursiveStateUncertainRoots(t *testing.T) {
	p := policy(t)
	state := t.TempDir()
	for _, command := range []string{
		"grep -r token .*", "grep -r token {internal,.duolog}",
		"rg --hidden token .*", "rg --hidden token {internal,.duolog}",
		`grep -r token "$ROOT"`, "grep -r token $(pwd)",
		"rg --hidden token ${ROOT}", "rg --hidden token `pwd`",
	} {
		t.Run(command, func(t *testing.T) {
			d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement", Work: state, Cwd: state, State: state})
			if d.Kind != Deny || d.Rule != RuleState {
				t.Fatal(d)
			}
		})
	}
}

func TestRecursiveStateNestedRoots(t *testing.T) {
	p := policy(t)
	state := t.TempDir()
	for _, target := range []string{".duolog/journal", "sub/.duolog", "sub/.DUOLOG/journal", ".duolog/tasks/task-1"} {
		t.Run(target, func(t *testing.T) {
			if !coversState(state, state, state, target) {
				t.Error("state root must be covered")
			}
			d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "grep -r token " + target, Role: "implement", Work: state, Cwd: state, State: state})
			if d.Kind != Deny || d.Rule != RuleState {
				t.Fatal(d)
			}
		})
	}
}

func TestRgExplicitStateRoots(t *testing.T) {
	p := policy(t)
	state := t.TempDir()
	for _, target := range []string{".duolog", ".duolog/journal", "sub/.duolog", "sub/.DUOLOG/tasks"} {
		t.Run(target, func(t *testing.T) {
			d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "rg token " + target, Role: "implement", Work: state, Cwd: state, State: state})
			if d.Kind != Deny || d.Rule != RuleState {
				t.Fatal(d)
			}
		})
	}
	for _, target := range []string{".", "internal", "sub/.duolog-other", ".duolog/../internal"} {
		check(t, p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "rg token " + target, Role: "implement", Work: state, Cwd: state, State: state}, Allow)
	}
	work := filepath.Join(state, ".duolog")
	d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "rg token .", Role: "implement", Work: work, State: state})
	if d.Kind != Allow {
		t.Fatal("the work root's ancestors are not searched:", d)
	}
}

func TestRecursiveStatePolicyPrecedence(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{
		{Action: "exec", Match: "grep*", Decision: Allow},
		{Action: "read", Match: ".duolog/**", Decision: Allow},
	}, p.Rules...)
	state := t.TempDir()
	for _, lead := range []LeadRules{
		{},
		{Deny: []config.Rule{{Action: "exec", Match: "grep*", Decision: Deny}}},
	} {
		d := Evaluate(p, lead, mode.Full, Action{Kind: "exec", Target: "grep -r token .", Role: "implement", Work: state, Cwd: state, State: state})
		if d.Kind != Deny || d.Rule != RuleState {
			t.Fatal(d)
		}
	}
}

func TestStateReadAllowsTasksAndRules(t *testing.T) {
	for _, target := range []string{
		".duolog", ".duolog/task.md", ".duolog/brief.md", ".duolog/tasks/x.md",
		".duolog/tasks/task-1/task.md", ".duolog/config.yaml", ".duolog/config.local.yaml",
		".duolog/learned.yaml", ".duolog/skill-tests/a.plan.yaml", "internal/gate/gate.go",
	} {
		if StateRead(target) {
			t.Error(target)
		}
	}
}

// Calibration evaluator files are not Markdown, so the state deny covers them;
// only the cell and judge briefs stay readable.
func TestStateReadDeniesCalibrationEvaluatorFiles(t *testing.T) {
	for _, p := range []string{".duolog/calibration/project.yaml", ".duolog/calibration/runs/r/tasks/t/hidden.patch", ".duolog/calibration/runs/r/tasks/t/statement.txt"} {
		if !StateRead(p) {
			t.Fatal(p)
		}
	}
	if StateRead(".duolog/calibration/runs/r/cells/c/task.md") {
		t.Fatal("brief must stay readable")
	}
}

func TestEvaluateDeniesStateBeforePolicy(t *testing.T) {
	p := policy(t)
	p.Rules = append([]config.Rule{{Action: "read", Match: ".duolog/**", Decision: Allow}}, p.Rules...)
	d := Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "read", Target: ".duolog/decisions.ndjson", Role: "implement"})
	if d.Kind != Deny || d.Rule != RuleState {
		t.Fatal(d)
	}
	d = Evaluate(p, LeadRules{}, mode.Full, Action{Kind: "read", Target: "/elsewhere/main/.duolog/bus.ndjson", Role: "implement"})
	if d.Kind != Deny || d.Rule != RuleState {
		t.Fatal("outside state path must be denied, not asked:", d)
	}
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "exec", Target: "cat .duolog/decisions.ndjson", Role: "implement"}, Deny)
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "read", Target: ".duolog/tasks/x.md", Role: "implement"}, Allow)
}
