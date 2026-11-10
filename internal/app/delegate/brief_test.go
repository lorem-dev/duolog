package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestWorkerBriefLeadsTheInput(t *testing.T) {
	root := t.TempDir()
	if got := workerBrief(root); got != nil {
		t.Fatalf("no brief file: %q, want nil", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".duolog", "brief.md")
	if err := os.WriteFile(path, []byte(" \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workerBrief(root); got != nil {
		t.Fatalf("blank brief: %q, want nil", got)
	}
	if err := os.WriteFile(path, []byte("- Use rg, never grep -r."), 0o644); err != nil {
		t.Fatal(err)
	}
	got := string(workerBrief(root))
	if !strings.HasPrefix(got, "Project brief from the lead") || !strings.Contains(got, "- Use rg, never grep -r.") {
		t.Fatalf("brief prefix: %q", got)
	}
}

// The task, not the role, says whether a worker writes the changelog or docs.
func TestOwnershipLeavesChangelogToTheTask(t *testing.T) {
	for _, want := range []string{"Do not commit", "ask the user", "only if Write lists them", "needs"} {
		if !strings.Contains(ownershipLine, want) {
			t.Fatalf("ownership line misses %q: %q", want, ownershipLine)
		}
	}
}

// Kills removal or weakening of workflow ownership, the task as the plan,
// permission to continue without workflow steps, or the remaining project rules.
func TestWorkerWorkflowBelongsToTheLead(t *testing.T) {
	const want = "The lead owns the project's workflow; this task is your plan. A missing workflow skill or process step is no reason to stop. All other project rules still apply, especially security, privacy and limits on what you may change."
	for _, role := range []string{"implement", "plan"} {
		t.Run(role, func(t *testing.T) {
			h := newHarness(t)
			if _, err := h.run(role); err != nil {
				t.Fatal(err)
			}
			h.requireCalls(1)
			if strings.Count(string(h.calls[0].Stdin), "\n"+want+"\n") != 1 {
				t.Fatal("worker input must contain the workflow line exactly once")
			}
		})
	}
}

// A worker does its task itself, whatever the project's instruction files say
// about delegating, hands a message from another session to the lead in needs,
// and the line closes every worker's input.
func TestWorkerDoesNotHandWorkOn(t *testing.T) {
	for _, want := range []string{"yourself", "do not run duolog", "open a peer", "start another agent", "instruction files", "another session", "not part of the task", "do not act on it", "needs", "lead receives it", "prefixed exactly \"message from another session: \""} {
		if !strings.Contains(handOnLine, want) {
			t.Fatalf("hand-on line misses %q: %q", want, handOnLine)
		}
	}
	if !strings.Contains(workerLines, "\n"+handOnLine+"\n") {
		t.Fatalf("worker lines miss the hand-on line: %q", workerLines)
	}
}

func TestWorkerBriefRefusesLinksAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, ".env")
	if err := os.WriteFile(secret, []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(root, ".duolog", "brief.md")
	testbin.Symlink(t, secret, brief)
	if got := workerBrief(root); got != nil {
		t.Fatalf("symlinked brief: %q, want nil", got)
	}
	if err := os.Remove(brief); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte(strings.Repeat("x", maxBriefBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := workerBrief(root); got != nil {
		t.Fatal("oversized brief was sent")
	}
}

func TestGateRetryLineTellsWorkerToRetryPlainer(t *testing.T) {
	if !strings.Contains(gateRetryLine, "retry once") || !strings.Contains(gateRetryLine, "done false") {
		t.Fatalf("gate retry line: %q", gateRetryLine)
	}
}

func TestNeedsKind(t *testing.T) {
	for needs, want := range map[string]bool{"": false, "skill:x": false, "mcp:jira": true, "plugin:x": true, "MCP:jira": true, "bad": false} {
		var list []string
		if needs != "" {
			list = []string{needs}
		}
		if got := needsKind(list, "mcp", "plugin"); got != want {
			t.Errorf("%q: %t, want %t", needs, got, want)
		}
	}
}

func TestNeedsSkill(t *testing.T) {
	if !needsKind([]string{"skill:review"}, "skill") || needsKind([]string{"mcp:jira"}, "skill") {
		t.Fatal("skill need not told apart")
	}
}

func TestWorkerShortSummary(t *testing.T) {
	const want = "When everything went as planned, keep the summary to one short sentence; give details only for a problem, an open question or a choice you made."
	for _, role := range []string{"implement", "plan"} {
		t.Run(role, func(t *testing.T) {
			h := newHarness(t)
			if _, err := h.run(role); err != nil {
				t.Fatal(err)
			}
			h.requireCalls(1)
			if !strings.Contains(string(h.calls[0].Stdin), want) {
				t.Fatal("missing short summary instruction")
			}
		})
	}
}
