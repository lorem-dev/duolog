package delegate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	corepolicy "github.com/lorem-dev/duolog/internal/core/policy"
)

// envValue is the value of name in env, the last entry winning, and whether
// it is set.
func envValue(env []string, name string) (string, bool) {
	value, set := "", false
	for _, pair := range env {
		if v, ok := strings.CutPrefix(pair, name+"="); ok {
			value, set = v, true
		}
	}
	return value, set
}

// The absolute and ~ entries of the task's Read reach the worker's gate, as
// absolute paths; relative entries and prose do not, nor does an entry too
// wide to hand over: a volume root, the home folder, or a folder of
// credentials or agent settings.
func TestTaskReadsReachWorkerGate(t *testing.T) {
	h := newHarness(t)
	h.path = filepath.Join(h.root, ".duolog", "task.md")
	outside := t.TempDir()
	h.task("- `" + filepath.ToSlash(filepath.Join(outside, "docs")) + "/*.md`: the docs\n- a.go\n- ~/notes/plan.md:3-9\n- the review notes\n" +
		"- `/`\n- `~`\n- ~/*.md\n- ~/.ssh/config\n- ~/.claude/settings.json\n- ~/.codex/")
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	value, _ := envValue(h.calls[0].Process.Env, corepolicy.TaskReadsEnv)
	got := corepolicy.TaskReads(value)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(outside, "docs", "*.md"), filepath.Join(home, "notes", "plan.md")}
	same := len(got) == len(want)
	for i := 0; same && i < len(want); i++ {
		same = got[i] == want[i] || runtime.GOOS == "windows" && strings.EqualFold(got[i], want[i])
	}
	if !same {
		t.Fatalf("%s = %q, want %q", corepolicy.TaskReadsEnv, got, want)
	}
}

// A task file outside .duolog/, such as one a cloned repository ships, cannot
// open paths outside the project: delegate ignores its outside Read entries
// and says so on stderr.
func TestTaskReadsOnlyFromStateTasks(t *testing.T) {
	h := newHarness(t)
	outside := filepath.ToSlash(t.TempDir())
	h.task("- `" + outside + "/*.md`\n- a.go")
	var stderr strings.Builder
	if _, err := h.runWith("implement", Options{Stderr: &stderr}); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if value, set := envValue(h.calls[0].Process.Env, corepolicy.TaskReadsEnv); set {
		t.Fatalf("%s=%s set for a task outside .duolog/", corepolicy.TaskReadsEnv, value)
	}
	// The entry is printed with the system's separator.
	if !strings.Contains(stderr.String(), "not under .duolog/") || !strings.Contains(stderr.String(), string(filepath.Separator)+"*.md") {
		t.Fatalf("stderr %q does not name the ignored entries", stderr.String())
	}
}

// A task whose Read names only project files sets nothing.
func TestTaskReadsUnsetWithoutOutsideEntries(t *testing.T) {
	h := newHarness(t)
	if _, err := h.run("implement"); err != nil {
		t.Fatal(err)
	}
	h.requireCalls(1)
	if value, set := envValue(h.calls[0].Process.Env, corepolicy.TaskReadsEnv); set {
		t.Fatalf("%s=%s set for a task that reads only project files", corepolicy.TaskReadsEnv, value)
	}
}
