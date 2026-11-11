package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

func TestRunsAndWatchFindSubmoduleRuns(t *testing.T) {
	isolatedRouteConfig(t)
	clearModelMarkers(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("DUOLOG_PEER", "")
	t.Setenv("REX_SESSION", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-a", Target: config.Target{Agent: "claude"}, PID: os.Getpid(), Root: f.Superproject}); err != nil {
		t.Fatal(err)
	}
	super := runRec("d-super1", "done", time.Hour)
	super.Lead, super.WorkRoot = "lead-a", f.Superproject
	sub := runRec("d-sub0001", "running", time.Minute)
	sub.Lead, sub.WorkRoot = "lead-a", f.Submodule
	for _, r := range []delegate.Record{super, sub} {
		log := filepath.Join(r.WorkRoot, filepath.FromSlash(r.Log))
		if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(log, []byte(runLogText), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := delegate.WriteRecord(filepath.Join(delegate.ProjectRecordDir(r.WorkRoot), r.ID+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.RecordDelegation(session.DelegationLocation{ID: "d-sub0001", Lead: "lead-a", WorkRoot: f.Submodule}); err != nil {
		t.Fatal(err)
	}
	chdir(t, f.Superproject)

	out, err := executeBusCommand(t, "runs")
	if err != nil || !strings.Contains(out, "d-sub0001 running\n") || !strings.Contains(out, "d-super1 done\n") {
		t.Fatalf("runs: %q (%v)", out, err)
	}
	out, err = executeBusCommand(t, "runs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []runRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.ID == "d-sub0001" {
			found = true
			if r.Worktree != f.Submodule {
				t.Fatalf("worktree %q, want %q", r.Worktree, f.Submodule)
			}
		}
	}
	if !found {
		t.Fatalf("no d-sub0001 in %s", out)
	}
	out, err = executeBusCommand(t, "watch", "d-sub0001", "--tab")
	if want := "no Rex here; run in your own terminal: duolog watch d-sub0001\n"; err != nil || out != want {
		t.Fatalf("watch --tab: %q (%v)", out, err)
	}
	finishRecord(t, filepath.Join(delegate.ProjectRecordDir(f.Submodule), "d-sub0001.json"), delegate.Outcome{})
	out, err = executeBusCommand(t, "watch", "d-sub0001")
	if err != nil || !strings.HasSuffix(out, "ended: done\n") {
		t.Fatalf("watch: %q (%v)", out, err)
	}
	chdir(t, f.Submodule)
	out, err = executeBusCommand(t, "runs")
	if err != nil || !strings.Contains(out, "d-super1") {
		t.Fatalf("runs from submodule: %q (%v)", out, err)
	}
}
