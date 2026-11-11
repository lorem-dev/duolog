package delegate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestDelegateSubmoduleWorkState(t *testing.T) {
	h := newHarness(t)
	f := submodule.New(t)
	put(t, filepath.Join(f.Superproject, ".duolog", "config.yaml"), "roles:\n  implement:\n    use: {agent: claude, model: sonnet}\n")
	put(t, filepath.Join(f.Submodule, ".duolog", "config.yaml"), "learning:\n  enabled: true\nrouting:\n  small_task_files: 0\nroles:\n  implement:\n    use: {agent: codex, model: sol}\n    session: resume\n    balance: false\n")
	var err error
	h.root, h.path = f.Linked, filepath.Join(f.Linked, "task.md")
	t.Chdir(f.Linked)
	h.cfg, err = configfile.Load(f.Submodule)
	if err != nil {
		t.Fatal(err)
	}
	h.s, err = store.Open(f.Submodule)
	if err != nil {
		t.Fatal(err)
	}
	h.reg, err = session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	h.register(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex", Model: "lead-model"}, Mode: "ask"})
	h.trust(true)
	h.task("- a.go")
	put(t, filepath.Join(f.Linked, "a.go"), strings.Repeat("line\n", 50))
	if err := h.reg.Record(session.Entry{ID: "foreign", Lead: "lead-123456", Agent: "codex", Role: "implement", Thread: "topic", ThreadID: "foreign-thread", WorkRoot: f.Superproject}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := h.run("implement")
		if err != nil {
			t.Fatal(out, err)
		}
		if len(h.calls) != i+1 {
			t.Fatalf("worker did not start: %+v", out)
		}
	}
	h.requireCalls(2)
	for i, spec := range h.calls {
		if spec.Cwd != f.Linked || spec.Route.Agent != "codex" {
			t.Fatalf("spec: %+v", spec)
		}
		wantThread := ""
		if i == 1 {
			wantThread = "worker-id"
		}
		if spec.ThreadID != wantThread {
			t.Fatalf("resumed %q, want %q", spec.ThreadID, wantThread)
		}
	}
	copies, err := filepath.Glob(filepath.Join(h.s.Dir, "tasks", "*", "task.md"))
	if err != nil || len(copies) != 2 {
		t.Fatalf("task copies: %v %v", copies, err)
	}
	if len(h.records()) != 2 {
		t.Fatal("missing work journal")
	}
	if !strings.Contains(read(t, filepath.Join(h.s.Dir, "usage.ndjson")), "codex") {
		t.Fatal("missing work usage")
	}
	e, found, err := h.reg.LookupWork("lead-123456", "codex", "implement", "topic", f.Linked)
	if err != nil || !found || e.WorkRoot != f.Linked {
		t.Fatalf("entry: %+v %v %v", e, found, err)
	}
	for _, name := range []string{"sessions.json", "bus.ndjson"} {
		if _, err := os.Stat(filepath.Join(h.s.Dir, name)); !os.IsNotExist(err) {
			t.Fatal("work-local lead state", name, err)
		}
	}
	for _, name := range []string{"tasks", "journal", "usage.ndjson"} {
		if _, err := os.Stat(filepath.Join(f.Superproject, ".duolog", name)); !os.IsNotExist(err) {
			t.Fatal("lead-home work state", name, err)
		}
	}
}
