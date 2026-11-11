package delegate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestSubmoduleDecisionAccounting(t *testing.T) {
	f := submodule.New(t)
	for _, root := range []string{f.Superproject, f.Submodule} {
		put(t, filepath.Join(root, ".duolog", "config.yaml"), "{}\n")
	}
	s := &store.Store{Dir: project.LeadHome(f.Linked)}
	action := policy.Action{Kind: "exec", Target: "go test", Role: "implement", Work: f.Linked}
	decision := policy.Decision{Kind: policy.Ask}
	if err := gate.Log(s, action, decision); err != nil {
		t.Fatal(err)
	}
	if err := gate.LogRequest(s, action, decision); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"decisions.ndjson", "requests.ndjson"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(s.Dir, name))
			if err != nil {
				t.Fatal(err)
			}
			var record struct {
				Action struct {
					Work string `json:"work"`
				} `json:"action"`
			}
			if err := json.Unmarshal(data, &record); err != nil {
				t.Fatal(err)
			}
			if record.Action.Work != f.Linked {
				t.Fatalf("logged work root = %q, want worker checkout %q", record.Action.Work, f.Linked)
			}
		})
	}
	lr := &learnRun{stateDir: filepath.Join(f.Submodule, ".duolog"), role: "implement"}
	if err := lr.start(f.Linked); err != nil {
		t.Fatal(err)
	}
	var observed Attempt
	att := newAttempts(Options{WorkRoot: f.Linked, Observe: func(a Attempt) { observed = a }}, filepath.Join(f.Submodule, ".duolog"), "implement", nil)
	started := att.begin()
	for _, row := range []struct{ role, work, kind string }{
		{"implement", f.Linked, policy.Deny},
		{"implement", pathutil.Resolved(f.Linked), policy.Ask},
		{"implement", f.Superproject, policy.Deny},
		{"review", f.Linked, policy.Ask},
		{"implement", "", policy.Deny},
	} {
		a := policy.Action{Role: row.role, Work: row.work}
		if err := gate.Log(s, a, policy.Decision{Kind: row.kind}); err != nil {
			t.Fatal(err)
		}
	}
	if denials, asks, err := lr.gateCounts(); err != nil || denials != 1 || asks != 1 {
		t.Fatalf("%d denies, %d asks: %v", denials, asks, err)
	}
	att.worker(started, coreroute.Route{}, false, run.Result{}, nil)
	if observed.Denials != 1 || observed.Asks != 1 || observed.ErrorCode != run.CodeGate {
		t.Fatalf("attempt: %+v", observed)
	}
	if _, err := os.Stat(filepath.Join(f.Submodule, ".duolog", "decisions.ndjson")); !os.IsNotExist(err) {
		t.Fatal("work-local decisions", err)
	}
}
