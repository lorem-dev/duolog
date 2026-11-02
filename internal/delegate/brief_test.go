package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.Symlink(secret, brief); err != nil {
		t.Fatal(err)
	}
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
