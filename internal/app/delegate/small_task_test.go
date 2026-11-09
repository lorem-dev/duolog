package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTask puts a task whose Write section is body in the harness.
func (h *harness) writeTask(body string) {
	h.t.Helper()
	put(h.t, h.path, "# Task: example\n\n## Goal\nDo work.\n\n## Read\n- a.go\n\n## Write\n"+body+"\n\n## Limits\nStay focused.\n\n## Check\ngo test ./...\n")
}

func TestSmallTaskRunsHere(t *testing.T) {
	for _, role := range []string{"implement", "mechanical", "tests", "docs", "planner"} {
		h := newHarness(t)
		h.cfg.Routing.SmallTaskFiles = 2
		h.writeTask("- a.go\n- b.go")
		o, err := h.run(role)
		h.requireCalls(0)
		if err != nil || o.RunHere == nil || o.RunHere.Where != "self" || o.RunHere.Task != h.path {
			t.Fatalf("%s: %+v, %v", role, o, err)
		}
		if !strings.Contains(o.Line, "run here: self task="+h.path) || !strings.Contains(o.Line, "routing.small_task_files") || !strings.Contains(o.Line, "--force") || strings.Contains(o.Line, "\n") {
			t.Fatalf("%s: line %q", role, o.Line)
		}
	}
}

func TestSmallTaskLeavesImplementComplexEscalationAndLearning(t *testing.T) {
	for name, run := range map[string]func(h *harness) (Outcome, error){
		"implement_complex": func(h *harness) (Outcome, error) { return h.run("implement_complex") },
		"escalation": func(h *harness) (Outcome, error) {
			return h.runWith("implement", Options{EscalateFrom: "task-1", EscalateReason: "quality"})
		},
		"learning": func(h *harness) (Outcome, error) {
			h.enableLearning()
			h.cfg.Routing.SmallTaskFiles = 2
			return h.run("implement")
		},
	} {
		h := newHarness(t)
		h.cfg.Routing.SmallTaskFiles = 2
		h.writeTask("- a.go")
		if o, _ := run(h); o.RunHere != nil && o.RunHere.Where == "self" && strings.Contains(o.Line, "routing.small_task_files") {
			t.Errorf("%s: the small-task rule applied: %q", name, o.Line)
		}
	}
}

func TestSmallTaskOverLimitStartsWorker(t *testing.T) {
	h := newHarness(t)
	h.cfg.Routing.SmallTaskFiles = 2
	h.writeTask("- a.go\n- b.go\n- c.go")
	if o, err := h.run("implement"); err != nil || o.RunHere != nil {
		t.Fatal(o, err)
	}
	h.requireCalls(1)
}

func TestSmallTaskOffStartsWorker(t *testing.T) {
	h := newHarness(t)
	h.cfg.Routing.SmallTaskFiles = 0
	if o, err := h.run("implement"); err != nil || o.RunHere != nil {
		t.Fatal(o, err)
	}
	h.requireCalls(1)
}

func TestSmallTaskNeverAffectsReviews(t *testing.T) {
	for _, role := range []string{"review_final", "debug_hard"} {
		h := newHarness(t)
		h.cfg.Routing.SmallTaskFiles = 2
		if o, err := h.run(role); err != nil || o.RunHere != nil {
			t.Fatalf("%s: %+v, %v", role, o, err)
		}
		h.requireCalls(1)
	}
}

func TestSmallTaskNeedsNamedFiles(t *testing.T) {
	for name, body := range map[string]string{
		"no entries": "Change the parser.",
		"glob":       "- internal/*.go",
		"deep glob":  "- `a/**/b.go`",
		"folder":     "- `src`",
		"folder dot": "- src/",
		"new folder": "- `not/yet/`",
	} {
		h := newHarness(t)
		h.cfg.Routing.SmallTaskFiles = 2
		if err := os.MkdirAll(filepath.Join(h.root, "src"), 0o755); err != nil {
			t.Fatal(err)
		}
		h.writeTask(body)
		if o, err := h.run("implement"); err != nil || o.RunHere != nil {
			t.Fatalf("%s: %+v, %v", name, o, err)
		}
		h.requireCalls(1)
	}
}

func TestSmallTaskForceStartsWorker(t *testing.T) {
	h := newHarness(t)
	h.cfg.Routing.SmallTaskFiles = 2
	h.writeTask("- a.go")
	o, err := h.runWith("implement", Options{Force: true})
	if err != nil || o.RunHere != nil {
		t.Fatal(o, err)
	}
	h.requireCalls(1)
}
