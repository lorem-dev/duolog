package delegate

import (
	"strings"
	"testing"
)

// A Read entry under .duolog/ that the worker's gate refuses stops the lead's
// task before a worker starts; Markdown and paths elsewhere pass, and a
// judge, calibration or replay task is not checked.
func TestStateReadsRefusesUnreadableState(t *testing.T) {
	if err := stateReads([]string{"internal/a.go", ".duolog/tasks/notes.md", "docs/x.diff", ".duolog/config.yaml"}, true); err != nil {
		t.Fatal(err)
	}
	refused := []string{"internal/a.go", ".duolog/tasks/s5.diff", "./.duolog/delegations/d-1.json"}
	err := stateReads(refused, true)
	if err == nil || !strings.Contains(err.Error(), ".duolog/tasks/s5.diff") || !strings.Contains(err.Error(), "d-1.json") || !strings.Contains(err.Error(), "move the file out of .duolog/") {
		t.Fatal(err)
	}
	if err := stateReads(refused, false); err != nil {
		t.Fatal(err)
	}
}

// A quoted Read item with words after its path is read as one path; the
// refusal says so rather than asking for a Markdown file it already names.
func TestStateReadsNamesAProseItem(t *testing.T) {
	err := stateReads([]string{".duolog/cards/plan.md and every file it lists"}, true)
	if err == nil || !strings.Contains(err.Error(), "read as one path") || strings.Contains(err.Error(), "save it as a .md file") {
		t.Fatal(err)
	}
}
