package screen

import "testing"

// The task line names the task from the pane's folder and, when given, the
// exact report path; without a report path it names none.
func TestTaskLineNamesTheReport(t *testing.T) {
	root := t.TempDir()
	path := root + "/task.md"
	report := ".duolog/tasks/peer-abc123/report.md"
	want := "Read task.md and do it. Do not call rex. Write the answer to " + report + ". Reply \"done\"."
	if got := TaskLine(root, path, report); got != want {
		t.Fatalf("%q, want %q", got, want)
	}
	if got, want := TaskLine(root, path, ""), "Read task.md and do it. Do not call rex. Reply \"done\"."; got != want {
		t.Fatalf("%q, want %q", got, want)
	}
}
