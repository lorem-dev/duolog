package delegate

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func delegateCapConfig(limit int) string {
	return fmt.Sprintf("limits:\n  max_live_sessions: %d\nroles:\n  implement:\n    use: {agent: codex, model: sol}\n    session: resume\n    balance: false\n", limit)
}

// submoduleHarness runs delegate in the linked submodule checkout, with its
// lead in the superproject's lead home.
func submoduleHarness(t *testing.T, leadConfig, subConfig string) (*harness, submodule.Fixture) {
	t.Helper()
	h := newHarness(t)
	f := submodule.New(t)
	put(t, filepath.Join(f.Superproject, ".duolog", "config.yaml"), leadConfig)
	put(t, filepath.Join(f.Submodule, ".duolog", "config.yaml"), subConfig)
	var err error
	h.root, h.path = f.Linked, filepath.Join(f.Linked, "task.md")
	t.Chdir(f.Linked)
	if h.cfg, err = configfile.Load(f.Submodule); err != nil {
		t.Fatal(err)
	}
	// The task writes one file; small_task_files has its own tests.
	h.cfg.Routing.SmallTaskFiles = 0
	if h.s, err = store.Open(f.Submodule); err != nil {
		t.Fatal(err)
	}
	if h.reg, err = session.Open(project.LeadHome(f.Linked)); err != nil {
		t.Fatal(err)
	}
	h.register(session.Lead{ID: "lead-123456", Target: config.Target{Agent: "codex", Model: "lead-model"}, Mode: "ask"})
	h.trust(true)
	h.task("- a.go")
	put(t, filepath.Join(f.Linked, "a.go"), strings.Repeat("line\n", 50))
	return h, f
}

// A resumed thread in the submodule is recorded under the lead home's cap: a
// cap of 1 there evicts the lead's older thread in the superproject, whatever
// the submodule sets, and a cap of 8 keeps it.
func TestDelegateSharedLeadCapConfig(t *testing.T) {
	for _, tc := range []struct{ lead, sub int }{{1, 8}, {8, 1}} {
		t.Run(fmt.Sprintf("lead %d submodule %d", tc.lead, tc.sub), func(t *testing.T) {
			h, f := submoduleHarness(t, delegateCapConfig(tc.lead), delegateCapConfig(tc.sub))
			old := session.Entry{ID: "old", Lead: "lead-123456", Agent: "codex", Role: "implement", Thread: "other", ThreadID: "old-thread", WorkRoot: f.Superproject, UpdatedAt: time.Now().Add(-time.Hour)}
			if err := h.reg.Record(old); err != nil {
				t.Fatal(err)
			}
			if out, err := h.run("implement"); err != nil || len(h.calls) != 1 {
				t.Fatal("worker did not run", out, err)
			}
			entries, err := h.reg.Entries()
			if err != nil {
				t.Fatal(err)
			}
			evicted := false
			for _, e := range entries {
				if e.ID == "old" {
					evicted = e.Closed
				}
			}
			if evicted != (tc.lead == 1) {
				t.Fatalf("old thread closed=%v under lead cap %d: %+v", evicted, tc.lead, entries)
			}
		})
	}
}

// An unreadable lead home config stops the run before any worker starts.
func TestDelegateSharedLeadCapInvalidConfig(t *testing.T) {
	h, _ := submoduleHarness(t, "limits: [\n", delegateCapConfig(8))
	if _, err := h.run("implement"); err == nil {
		t.Fatal("ran with an invalid lead config")
	}
	h.requireCalls(0)
}
