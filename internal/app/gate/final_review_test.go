package gate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func TestReviewRoleExecBeforeEveryMode(t *testing.T) {
	p := testPolicy(t)
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
		lead := LeadRules{RoleSandbox: "read-only"}
		for _, command := range []string{"rm foo.txt", "sed -i x foo", "git commit -am x", "rm -r -f build", "git -C . push origin HEAD", "make run", "npm run test", "uv run app", "go fmt ./...", "/tmp/evil/make test", "git diff --output=out.txt", "find . -delete", "rg --pre=evil x"} {
			check(t, p, lead, m, Action{Kind: "exec", Target: command, Role: "review_final"}, Deny)
		}
		for _, command := range []string{"git status", "git diff", "git log -1", "git show HEAD", "cat file", "rg pattern src", "ls"} {
			check(t, p, lead, m, Action{Kind: "exec", Target: command, Role: "review_final"}, Allow)
		}
		check(t, p, lead, m, Action{Kind: "write", Role: "review_final"}, Deny)
		broad := p
		broad.Rules = []config.Rule{{Action: "exec", Match: "*", Decision: Allow}}
		check(t, broad, lead, m, Action{Kind: "exec", Target: "git status", Role: "review_final"}, Deny)
	}
}

func TestEquivalentDenyCommands(t *testing.T) {
	p := testPolicy(t)
	for _, command := range []string{`git push "origin" main`, `git -C . push`, `git --no-pager push`, `git -c a=b --git-dir=.git push`, `git -C. push`, `rm -r -f x`, `rm -fr x`, `rm -R --force x`, `rm --recursive --force x`, `git "push" origin main`} {
		for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
			check(t, p, LeadRules{}, m, Action{Kind: "exec", Target: command, Role: "implement"}, Deny)
		}
	}
}

func TestOutsidePathsBeatCatchAllAndFull(t *testing.T) {
	p := testPolicy(t)
	for _, m := range []mode.Mode{mode.Plan, mode.Ask, mode.Edits, mode.Full} {
		for _, kind := range []string{"read", "write"} {
			want := Ask
			if kind == "write" && m == mode.Plan {
				want = Deny // plan's limit comes before the outside ask
			}
			check(t, p, LeadRules{}, m, Action{Kind: kind, Target: "../home/.zshrc", Role: "implement"}, want)
		}
		check(t, p, LeadRules{}, m, Action{Kind: "read", Target: "apps/web/.env", Role: "review_final"}, Deny)
		check(t, p, LeadRules{}, m, Action{Kind: "read", Target: "apps/web/.env.local", Role: "review_final"}, Deny)
	}
	p.Rules = append(p.Rules, config.Rule{Action: "read", Match: "../shared/**", Decision: Allow})
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "read", Target: "../shared/a", Role: "review_final"}, Allow)
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "read", Target: "../other/a", Role: "review_final"}, Ask)
	p.Rules = append(p.Rules, config.Rule{Action: "read", Match: "{../shared/**,**}", Decision: Allow})
	check(t, p, LeadRules{}, mode.Full, Action{Kind: "read", Target: "../other/a", Role: "review_final"}, Ask)
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		check(t, p, LeadRules{}, mode.Full, Action{Kind: "write", Target: ".GIT/hooks/pre-commit", Role: "implement"}, Deny)
	}
}

func TestOrdinaryToolsAndPatchTargets(t *testing.T) {
	p := testPolicy(t)
	for _, name := range []string{"Grep", "Glob", "LS", "TodoWrite", "Skill", "NotebookEdit"} {
		a, err := Normalize("claude", []byte(`{"tool_name":"`+name+`","tool_input":{"path":"src","notebook_path":"src/notebook.ipynb"}}`))
		if err != nil {
			t.Fatal(err)
		}
		a.Role = "implement"
		if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind != Allow {
			t.Fatalf("%s: %+v %+v", name, a, d)
		}
	}
	input := []byte(`{"tool_name":"apply_patch","tool_input":{"patch":"*** Begin Patch\n*** Update File: src/a\n*** Move to: .git/hooks/x\n*** Add File: src/b\n*** Delete File: src/c\n*** End Patch"}}`)
	actions, err := NormalizeAll("codex", input)
	if err != nil || len(actions) != 4 {
		t.Fatal(actions, err)
	}
	actions[1].Role = "implement"
	if d := Evaluate(p, LeadRules{}, mode.Full, actions[1]); d.Kind != Deny {
		t.Fatal(d)
	}
}

func TestTaskStopAllowed(t *testing.T) {
	testSessionTool(t, "TaskStop", `{"task_id":"b1"}`)
}

func TestTaskOutputAllowed(t *testing.T) {
	testSessionTool(t, "TaskOutput", `{"task_id":"b1","block":false}`)
}

func testSessionTool(t *testing.T, name, input string) {
	t.Helper()
	p := testPolicy(t)
	a, err := Normalize("claude", []byte(`{"tool_name":"`+name+`","tool_input":`+input+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "internal" {
		t.Fatalf("%s: %+v", name, a)
	}
	a.Role = "implement"
	if d := Evaluate(p, LeadRules{}, mode.Edits, a); d.Kind != Allow {
		t.Fatalf("%s: %+v %+v", name, a, d)
	}
}

func TestBareToolDenyDoesNotBreakOtherTools(t *testing.T) {
	root := t.TempDir()
	testPolicy(t)
	path := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions":{"deny":["mcp__github__create_issue","Grep","Skill","Glob"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	lead, err := ImportLead(root, "claude")
	if err != nil {
		t.Fatal(err)
	}
	p := config.Policy{OnUnmatched: Ask, Rules: []config.Rule{{Action: "read", Match: "**", Decision: Allow}}}
	check(t, p, lead, mode.Full, Action{Kind: "read", Target: "src/a", Role: "review_final"}, Allow)
	for _, tool := range []string{"Grep", "Skill", "Glob"} {
		check(t, p, lead, mode.Full, Action{Kind: "unknown", Target: tool}, Deny)
	}
	// The hook names an MCP tool mcp__server__tool; the lead's deny holds.
	check(t, p, lead, mode.Full, Action{Kind: "mcp", Target: "github/create_issue"}, Deny)
}

func TestAskSummaryAndForegroundWait(t *testing.T) {
	text := "echo " + strings.Repeat("a", 500) + "; rm -r -f /important"
	summary := askSummary(strings.Join(strings.Fields(text), " "), 200)
	if len(summary) > 200 || !utf8.ValidString(summary) || !strings.Contains(summary, "[cut; duolog decisions]") || !strings.HasSuffix(summary, "/important") {
		t.Fatal(summary)
	}
	b, reg, _ := askSetup(t)
	if err := reg.SetWaiting("first-123456", "delegate", os.Getpid()); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	d := Escalate(context.Background(), b, "first-123456", Action{Kind: "exec", Target: "unmatched"}, time.Minute, false)
	// Denied at once, not after the minute's wait; 10 s gives a slow runner room.
	if took := time.Since(started); d.Kind != Deny || !strings.Contains(d.Reason, "lead is waiting") || took > 10*time.Second {
		t.Fatal(d, took)
	}
	if err := reg.SetWaiting("first-123456", "delegate", 0); err != nil {
		t.Fatal(err)
	}
	if waiting, err := reg.IsWaiting("first-123456"); err != nil || waiting {
		t.Fatal(waiting, err)
	}
	if AskWait(900, false, LegacyHookBudget) != MaxAskWait || AskWait(2, false, LegacyHookBudget) != 2*time.Second {
		t.Fatal("hook wait exceeds budget")
	}
	// The summary stays valid when non-ASCII actions are supplied.
	summary = askSummary(strings.Repeat("\u00e9", 200)+" tail", 200)
	if !json.Valid([]byte(`"`+summary+`"`)) || !utf8.ValidString(summary) {
		t.Fatal(summary)
	}
}
