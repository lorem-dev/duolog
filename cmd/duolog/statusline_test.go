package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// runningSnapshot is a snapshot of lead-1 with one running run.
func runningSnapshot() runsnap.Snapshot {
	s := runsnap.Snapshot{Version: 1, Lead: &runsnap.Lead{ID: "lead-1", Agent: "claude"}}
	s.Runs = []runsnap.Run{{ID: "r1", State: "running"}}
	s.LineText = s.Line()
	return s
}

func statuslineOut(t *testing.T, input string, environ []string, build func(context.Context, statuslineInput) (runsnap.Snapshot, error)) string {
	t.Helper()
	var out bytes.Buffer
	runStatusline(context.Background(), strings.NewReader(input), &out, environ, time.Second, build)
	return out.String()
}

func TestStatuslinePrintsLine(t *testing.T) {
	snap := runningSnapshot()
	var got statuslineInput
	out := statuslineOut(t, `{"session_id":"s1","workspace":{"project_dir":"/p"}}`, nil,
		func(_ context.Context, in statuslineInput) (runsnap.Snapshot, error) {
			got = in
			return snap, nil
		})
	if snap.Line() == "" || out != snap.Line()+"\n" {
		t.Fatalf("output %q, want %q", out, snap.Line()+"\n")
	}
	if got.SessionID != "s1" || got.Workspace.ProjectDir != "/p" {
		t.Fatalf("build got %+v", got)
	}
}

func TestStatuslineSilent(t *testing.T) {
	ok := func(context.Context, statuslineInput) (runsnap.Snapshot, error) { return runningSnapshot(), nil }
	valid := `{"session_id":"s1","workspace":{"project_dir":"/p"}}`
	for _, c := range []struct {
		name, input string
		environ     []string
		build       func(context.Context, statuslineInput) (runsnap.Snapshot, error)
	}{
		{name: "not json", input: "not json", build: ok},
		{name: "no session id", input: "{}", build: ok},
		{name: "build error", input: valid, build: func(context.Context, statuslineInput) (runsnap.Snapshot, error) {
			return runningSnapshot(), errors.New("boom")
		}},
		{name: "no lead", input: valid, build: func(context.Context, statuslineInput) (runsnap.Snapshot, error) {
			return runsnap.Snapshot{Version: 1}, nil
		}},
		{name: "empty line", input: valid, build: func(context.Context, statuslineInput) (runsnap.Snapshot, error) {
			return runsnap.Snapshot{Version: 1, Lead: &runsnap.Lead{ID: "lead-1"}}, nil
		}},
		{name: "worker depth", input: valid, environ: []string{"DUOLOG_DEPTH=1"}, build: ok},
		{name: "peer", input: valid, environ: []string{"DUOLOG_PEER=p1"}, build: ok},
		{name: "input over 1 MiB", input: `{"session_id":"s1","pad":"` + strings.Repeat("x", 1<<20) + `"}`, build: ok},
	} {
		t.Run(c.name, func(t *testing.T) {
			if out := statuslineOut(t, c.input, c.environ, c.build); out != "" {
				t.Fatalf("output %q", out)
			}
		})
	}
	t.Run("project without .duolog", func(t *testing.T) {
		isolatedRouteConfig(t)
		t.Setenv("DUOLOG_DEPTH", "")
		t.Setenv("DUOLOG_PEER", "")
		t.Setenv("DUOLOG_LEAD", "")
		t.Setenv("REX_SESSION", "")
		dir := t.TempDir()
		chdir(t, dir)
		input := `{"session_id":"s1","workspace":{"project_dir":"` + filepath.ToSlash(dir) + `"}}`
		if out := statuslineOut(t, input, nil, testDeps(t).statuslineSnapshot); out != "" {
			t.Fatalf("output %q", out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".duolog")); !os.IsNotExist(err) {
			t.Fatalf(".duolog after the status line: %v", err)
		}
	})
}

func TestStatuslineInSubmodule(t *testing.T) {
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_PEER", "")
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("REX_SESSION", "")
	f := submodule.New(t)
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", "learning:\n  enabled: false\n")
	chdir(t, f.Superproject)
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-sub", Target: config.Target{Agent: "claude"}, PID: os.Getpid(), Root: f.Superproject}); err != nil {
		t.Fatal(err)
	}
	rec := runRec("d-subrun", "running", time.Minute)
	rec.Lead, rec.WorkRoot = "lead-sub", f.Submodule
	if err := delegate.WriteRecord(filepath.Join(delegate.ProjectRecordDir(f.Submodule), rec.ID+".json"), rec); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordDelegation(session.DelegationLocation{ID: rec.ID, Lead: "lead-sub", WorkRoot: f.Submodule}); err != nil {
		t.Fatal(err)
	}
	d := testDeps(t)
	line := func(dir string) string {
		return statuslineOut(t, `{"session_id":"lead-sub","workspace":{"project_dir":"`+filepath.ToSlash(dir)+`"}}`, nil, d.statuslineSnapshot)
	}
	want := line(f.Superproject)
	if want != "duolog 1 run\n" {
		t.Fatalf("superproject line %q", want)
	}
	if got := line(f.Submodule); got != want {
		t.Fatalf("submodule line %q, want %q", got, want)
	}
}

func TestStatuslineBudget(t *testing.T) {
	start := time.Now()
	var out bytes.Buffer
	runStatusline(context.Background(), strings.NewReader(`{"session_id":"s1"}`), &out, nil, 50*time.Millisecond,
		func(ctx context.Context, _ statuslineInput) (runsnap.Snapshot, error) {
			<-ctx.Done()
			return runningSnapshot(), nil
		})
	if out.Len() != 0 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("output %q after %v", out.String(), time.Since(start))
	}
}

// Claude Code's process folder may be outside the project: the status line
// reads the peers of project_dir's lead and creates no .duolog where it runs.
func TestStatuslineReadsProjectDirNotCwd(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{SleepMS: 60000})
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-fast", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", WorkRoot: root}); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	chdir(t, elsewhere)
	t.Setenv("DUOLOG_LEAD", "")
	input := `{"session_id":"lead-1","cwd":"` + filepath.ToSlash(elsewhere) + `","workspace":{"project_dir":"` + filepath.ToSlash(root) + `"}}`
	if out := statuslineOut(t, input, nil, testDeps(t).statuslineSnapshot); out != "duolog 1 peer\n" {
		t.Fatalf("output %q", out)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, ".duolog")); !os.IsNotExist(err) {
		t.Fatalf(".duolog where the status line ran: %v", err)
	}
}

// The command takes any flags and arguments, and a panic in the snapshot gives
// exit 0, nothing on stdout and nothing on stderr.
func TestStatuslineCommandNeverFails(t *testing.T) {
	for _, args := range [][]string{{"--x"}, {"a", "--y=1", "-z"}} {
		cmd := statuslineCommand(func(context.Context, statuslineInput) (runsnap.Snapshot, error) {
			panic("boom")
		})
		var out, errOut bytes.Buffer
		cmd.SetIn(strings.NewReader(`{"session_id":"s1"}`))
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil || out.Len() != 0 || errOut.Len() != 0 {
			t.Fatalf("%v: err %v, stdout %q, stderr %q", args, err, out.String(), errOut.String())
		}
	}
}
