package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// cliLead registers the live lead lead-cli for the project at root and makes
// it the lead of the environment.
func cliLead(t *testing.T, root string) *session.Registry {
	t.Helper()
	t.Setenv("DUOLOG_LEAD", "lead-cli")
	reg, err := session.Open(project.LeadHome(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-cli", Target: config.Target{Agent: "claude"}, PID: os.Getpid(), Root: root}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func recordFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCLIDelegateWritesARecord(t *testing.T) {
	root := runsProject(t)
	cliLead(t, root)
	dir := delegate.RecordDir(filepath.Join(root, ".duolog"))
	var during delegate.Record
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun("lead-cli", "tasks/task-1")
		if files := recordFiles(t, dir); len(files) == 1 {
			during, _ = delegate.ReadRecord(files[0], nil)
		}
		return delegate.Outcome{Line: "done=true"}, nil
	}
	out, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md")
	if err != nil || out != "done=true\n" {
		t.Fatal(out, err)
	}
	if during.State != "running" || during.Lead != "lead-cli" {
		t.Fatalf("record during the run: %+v", during)
	}
	files := recordFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("records: %v", files)
	}
	r, err := delegate.ReadRecord(files[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^d-[a-z2-7]{6}$`).MatchString(r.ID) || r.Role != "implement" || r.Lead != "lead-cli" || r.TaskPath != "task.md" || r.PID != os.Getpid() || r.TaskDir != "tasks/task-1" || r.State != "done" || r.Line != "done=true" || r.Ended.IsZero() || checkoutKey(r.WorkRoot) != checkoutKey(root) {
		t.Fatalf("%+v", r)
	}
	out, err = executeBusCommand(t, "runs")
	if err != nil || out != r.ID+" done\n" {
		t.Fatal(out, err)
	}
}

func TestCLIDelegateInSubmoduleIsIndexed(t *testing.T) {
	isolatedRouteConfig(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	reg := cliLead(t, f.Superproject)
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun("lead-cli", "tasks/task-1")
		return delegate.Outcome{Line: "done=true"}, nil
	}
	chdir(t, f.Submodule)
	if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md"); err != nil {
		t.Fatal(err)
	}
	locations, err := reg.Delegations("lead-cli")
	if err != nil || len(locations) != 1 || checkoutKey(locations[0].WorkRoot) != checkoutKey(f.Submodule) {
		t.Fatalf("locations: %+v (%v)", locations, err)
	}
	chdir(t, f.Superproject)
	out, err := executeBusCommand(t, "runs")
	if err != nil || out != locations[0].ID+" done\n" {
		t.Fatal(out, err)
	}
}

func TestPrunedSubmoduleRecordLeavesTheIndex(t *testing.T) {
	isolatedRouteConfig(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	reg := cliLead(t, f.Superproject)
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun("lead-cli", "tasks/task-1")
		return delegate.Outcome{Line: "done=true"}, nil
	}
	chdir(t, f.Submodule)
	if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md"); err != nil {
		t.Fatal(err)
	}
	first, err := reg.Delegations("")
	if err != nil || len(first) != 1 {
		t.Fatalf("first run: %+v (%v)", first, err)
	}
	for range 51 {
		if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md"); err != nil {
			t.Fatal(err)
		}
	}
	all, err := reg.Delegations("")
	if err != nil || len(all) != 51 {
		t.Fatalf("entries: %d (%v); want 51", len(all), err)
	}
	for _, l := range all {
		if l.ID == first[0].ID {
			t.Fatalf("the first run %s is still listed", l.ID)
		}
	}
}

func TestResultFileKeepsTheMCPRecord(t *testing.T) {
	root := runsProject(t)
	cliLead(t, root)
	dir := delegate.RecordDir(filepath.Join(root, ".duolog"))
	path := filepath.Join(dir, "d-mcp001.json")
	if err := delegate.WriteRecord(path, delegate.Record{ID: "d-mcp001", Role: "implement", Lead: "lead-cli", TaskPath: "task.md", WorkRoot: root, State: "running", Started: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun("lead-cli", "tasks/task-1")
		return delegate.Outcome{Line: "done=true"}, nil
	}
	if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md", "--result-file", path); err != nil {
		t.Fatal(err)
	}
	if files := recordFiles(t, dir); len(files) != 1 {
		t.Fatalf("records: %v", files)
	}
	r, err := delegate.ReadRecord(path, nil)
	if err != nil || r.ID != "d-mcp001" || r.TaskDir != "tasks/task-1" || r.State != "done" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestWorkerDelegateWritesNoRecord(t *testing.T) {
	root := runsProject(t)
	cliLead(t, root)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, _ = executeBusCommand(t, "delegate", "implement", "--task", "task.md")
	if _, err := os.Stat(delegate.RecordDir(filepath.Join(root, ".duolog"))); !os.IsNotExist(err) {
		t.Fatalf("delegations folder: %v", err)
	}
}

func TestMCPTwinSkipsCLIRecords(t *testing.T) {
	root := runsProject(t)
	cliLead(t, root)
	in := mcp.DelegateInput{Role: "implement", Task: testTask}
	data, err := taskspec.Render(taskspec.Spec(*testTask))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cli-task.md"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(root, ".duolog")
	rec := delegate.Record{ID: "d-cli001", Role: "implement", Lead: "lead-cli", TaskPath: "cli-task.md", WorkRoot: root, State: "running", PID: os.Getpid(), Started: time.Now().UTC()}
	if err := delegate.WriteRecord(filepath.Join(delegate.RecordDir(stateDir), rec.ID+".json"), rec); err != nil {
		t.Fatal(err)
	}
	if path, _ := mcpRunningTwin(in, mcpDelegateTarget{stateDir: stateDir, work: root, lead: "lead-cli"}); path != "" {
		t.Fatalf("twin = %q", path)
	}
}

func TestKilledCLIDelegateReadsLost(t *testing.T) {
	root := runsProject(t)
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Skip(err)
	}
	rec := delegate.Record{ID: "d-dead01", Role: "implement", Lead: "lead-cli", TaskPath: "task.md", WorkRoot: root, State: "running", PID: cmd.Process.Pid, TaskDir: "tasks/task-1", Started: time.Now().UTC()}
	if err := delegate.WriteRecord(filepath.Join(delegate.RecordDir(filepath.Join(root, ".duolog")), rec.ID+".json"), rec); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "runs")
	if err != nil || out != "d-dead01 lost\n" {
		t.Fatal(out, err)
	}
}

func TestCLIDelegateFromLinkedWorktreeRecordsThatCheckout(t *testing.T) {
	isolatedRouteConfig(t)
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	cliLead(t, f.Superproject)
	testDeps(t).RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun("lead-cli", "tasks/task-1")
		return delegate.Outcome{Line: "done=true"}, nil
	}
	chdir(t, f.SuperLinked)
	if _, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md"); err != nil {
		t.Fatal(err)
	}
	files := recordFiles(t, delegate.RecordDir(filepath.Join(f.Superproject, ".duolog")))
	if len(files) != 1 {
		t.Fatalf("records: %v", files)
	}
	r, err := delegate.ReadRecord(files[0], nil)
	if err != nil || checkoutKey(r.WorkRoot) != checkoutKey(f.SuperLinked) || r.TaskPath != "task.md" {
		t.Fatalf("%+v %v", r, err)
	}
}
