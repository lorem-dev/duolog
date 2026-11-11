package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	corerunsnap "github.com/lorem-dev/duolog/internal/core/runsnap"
	"github.com/lorem-dev/duolog/internal/quota"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// pinnedStart is the start of the oldest record of the pinned outputs.
var pinnedStart = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// pinned is out with the temp root replaced by <root>, in its resolved
// spelling first, and the peer id, when given, by <peer>.
func pinned(t *testing.T, out, root, peerID string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
		out = strings.ReplaceAll(out, resolved, "<root>")
	}
	out = strings.ReplaceAll(out, root, "<root>")
	if peerID != "" {
		out = strings.ReplaceAll(out, peerID, "<peer>")
	}
	return out
}

// deadPID is the pid of a process that has ended.
func deadPID(t *testing.T) int {
	t.Helper()
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	return c.ProcessState.Pid()
}

// runsOutputs runs duolog with args and returns its stdout and stderr apart.
func runsOutputs(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRootCommand(testDeps(t))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

const pinnedMCPStatus = `{"peers":[{"id":"<peer>","role":"plan","agent":"claude","state":"ready","work_root":"<root>"}],` +
	`"open_asks":[{"ask":1,"from":"claude/worker","summary":"run go test ./..."}],` +
	`"delegations":[{"task_path":".duolog/tasks/d-bbbbbb/task.md","role":"review","state":"lost","answer_path":"<root>/.duolog/delegations/d-bbbbbb.json","work_root":"<root>"},` +
	`{"task_path":".duolog/tasks/d-aaaaaa/task.md","role":"implement","state":"done","answer_path":"<root>/.duolog/delegations/d-aaaaaa.json","work_root":"<root>"}],` +
	`"load":"duolog peers: claude 1 running (1 idle) of 3; codex 0 of 3; 5h 0%; idle: <peer> - 3 codex sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)",` +
	`"quota":"claude 5h 0% weekly 0%; codex 5h 0% weekly 0%"}`

// The MCP status tool's output, byte for byte: a later change to how it is
// gathered must not change it.
func TestMCPStatusOutputIsPinned(t *testing.T) {
	root, _ := peerProject(t, sequence(claudeIdleScreen(), claudeIdleScreen(), claudeIdleScreen()))
	opened, err := executeBusCommand(t, "peer", "open", "plan")
	if err != nil {
		t.Fatal(err)
	}
	peerID, _, _ := strings.Cut(strings.TrimSpace(opened), " ")
	writeStatusBus(t, root,
		statusAsk(1, statusLeadAddress, "run go test ./...", false),
		statusAsk(2, "codex/lead.other", "another lead's", true),
	)
	writeStatusRecord(t, root, delegate.Record{ID: "d-aaaaaa", Role: "implement", Lead: "lead-1", WorkRoot: root,
		TaskPath: ".duolog/tasks/d-aaaaaa/task.md", State: "done", Started: pinnedStart, Ended: pinnedStart.Add(time.Second)})
	writeStatusRecord(t, root, delegate.Record{ID: "d-bbbbbb", Role: "review", Lead: "lead-1", WorkRoot: root,
		TaskPath: ".duolog/tasks/d-bbbbbb/task.md", State: "running", PID: deadPID(t), Started: pinnedStart.Add(time.Minute)})
	writeStatusRecord(t, root, delegate.Record{ID: "d-cccccc", Role: "implement", Lead: "other", WorkRoot: root,
		TaskPath: ".duolog/tasks/d-cccccc/task.md", State: "done", Started: pinnedStart.Add(2 * time.Minute)})

	got, err := testDeps(t).mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if out := pinned(t, string(data), root, peerID); out != pinnedMCPStatus {
		t.Fatalf("status =\n%s\nwant\n%s", out, pinnedMCPStatus)
	}
}

// The MCP status tool's load line is the lead hooks' line.
func TestMCPStatusLoadLineIsPinned(t *testing.T) {
	d := loadLineProject(t)
	got, err := d.mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Load, "claude 1 running (1 idle) of 10") || !strings.Contains(got.Load, "5h 41%") {
		t.Fatalf("load %q, want the large tier's 10 and the admission's 41%%", got.Load)
	}
}

// A peer's session and a worker get no load line from the status tool, as
// from the lead hooks.
func TestMCPStatusHasNoLoadInAPeerOrWorker(t *testing.T) {
	for _, env := range [][2]string{{"DUOLOG_PEER", "peer-1"}, {"DUOLOG_DEPTH", "1"}} {
		t.Run(env[0], func(t *testing.T) {
			d := loadLineProject(t)
			t.Setenv(env[0], env[1])
			got, err := d.mcpStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got.Load != "" {
				t.Fatalf("load %q under %s=%s, want none", got.Load, env[0], env[1])
			}
		})
	}
}

// A panic in the load calculation leaves the load out: the status tool still
// answers, and the MCP server, which has no recover of its own, keeps running.
func TestMCPStatusSurvivesAPanicInTheLoad(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	d := testDeps(t)
	open, calls := d.Delegate.PaidGate, 0
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		// The first call is the load's; the quota's comes after it.
		if calls++; calls == 1 {
			panic("load")
		}
		return open(cfg)
	}
	got, err := d.mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 || got.Load != "" || len(got.Peers) != 1 {
		t.Fatalf("%d gate opens, load %q, peers %+v", calls, got.Load, got.Peers)
	}
}

// runs shows no load, so it computes none: it never opens the paid-runs
// state that the load reads, even with peers listed.
func TestRunsJSONComputesNoLoad(t *testing.T) {
	runsPinnedProject(t)
	d := testDeps(t)
	open, calls := d.Delegate.PaidGate, 0
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		calls++
		return open(cfg)
	}
	out, _, err := runsOutputs(t, "runs", "--json")
	if err != nil || !strings.Contains(out, `"id":"peer-list"`) || calls != 0 {
		t.Fatalf("%d gate opens, %q %v", calls, out, err)
	}
}

// watch with no id picks from the records runs lists: a run whose record
// names the lead by its bus address is the lead's.
func TestWatchWithoutAnIdTakesARunOfTheLeadsAddress(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	asTerminal(t, true)
	withAgentAncestor(t, "")
	t.Setenv("REX_SESSION", "")
	mine := runRec("d-mine01", "running", 2*time.Minute)
	mine.Lead, mine.WorkRoot = statusLeadAddress, root
	theirs := runRec("d-other1", "running", time.Minute)
	theirs.Lead, theirs.WorkRoot = "claude/lead.other1", root
	writeStatusRecord(t, root, mine)
	writeStatusRecord(t, root, theirs)
	out, err := executeBusCommand(t, "watch", "--tab")
	if err != nil || strings.TrimSpace(out) != "no Rex here; run in your own terminal: duolog watch d-mine01" {
		t.Fatalf("%q %v", out, err)
	}
}

// loadLineProject is the project of TestLeadLoadUsesAdmissionsTierAndWindow:
// one idle claude peer, the large tier and a 41% window from admission.
func loadLineProject(t *testing.T) *cmdDeps {
	t.Helper()
	clearModelMarkers(t)
	root, _ := peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	recordListPeer(t, root)
	d := testDeps(t)
	open := d.Delegate.PaidGate
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		g, err := open(cfg)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		err = g.Store.Update(context.Background(), func(st *paidruns.State) error {
			st.Tiers["claude"] = paidrules.TierResult{Tier: paidrules.TierLarge, Limit: 10}
			st.Reservations["r-1"] = paidrules.Reservation{ID: "r-1", Agent: "claude", Created: now.Add(-time.Minute),
				Estimate: paidrules.Estimate{Windows: []paidrules.WindowCost{{Key: paidrules.WindowKey{Kind: "session", Reset: now.Add(time.Hour)}, Used: 0.41}}}}
			return nil
		})
		return g, err
	}
	return d
}

// runsPinnedProject is the project of TestRunsJSONHasOneShapeForPeersAndDelegateRuns
// with fixed starts, plus six finished runs.
func runsPinnedProject(t *testing.T) (root string) {
	t.Helper()
	root, _ = peerProject(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	asTerminal(t, true)
	withAgentAncestor(t, "")
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", Task: ".duolog/task.md", WorkRoot: root}); err != nil {
		t.Fatal(err)
	}
	dir := delegate.RecordDir(filepath.Join(root, ".duolog"))
	rec := runRec("d-newrun", "running", 0)
	rec.Started = pinnedStart.Add(time.Hour)
	rec.WorkRoot, rec.Lead = filepath.Join(root, ".worktrees", "feature"), "lead-1"
	records := []delegate.Record{rec}
	for i, id := range []string{"d-done00", "d-done01", "d-done02", "d-done03", "d-done04", "d-done05"} {
		r := runRec(id, "done", 0)
		r.Started = pinnedStart.Add(time.Duration(i) * time.Minute)
		r.Ended = r.Started.Add(time.Second)
		r.WorkRoot, r.Lead = root, "lead-1"
		records = append(records, r)
	}
	for _, r := range records {
		if err := delegate.WriteRecord(filepath.Join(dir, r.ID+".json"), r); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const pinnedRunsDelegates = `{"id":"d-newrun","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"running","started":"2026-01-02T04:04:05Z","worktree":"<root>/.worktrees/feature","task_dir":"<root>/.worktrees/feature/.duolog/tasks/task-d-newrun"},` +
	`{"id":"d-done05","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"done","started":"2026-01-02T03:09:05Z","worktree":"<root>","task_dir":"<root>/.duolog/tasks/task-d-done05"},` +
	`{"id":"d-done04","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"done","started":"2026-01-02T03:08:05Z","worktree":"<root>","task_dir":"<root>/.duolog/tasks/task-d-done04"},` +
	`{"id":"d-done03","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"done","started":"2026-01-02T03:07:05Z","worktree":"<root>","task_dir":"<root>/.duolog/tasks/task-d-done03"},` +
	`{"id":"d-done02","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"done","started":"2026-01-02T03:06:05Z","worktree":"<root>","task_dir":"<root>/.duolog/tasks/task-d-done02"},` +
	`{"id":"d-done01","kind":"delegate","role":"implement","agent":"claude","model":"sonnet","state":"done","started":"2026-01-02T03:05:05Z","worktree":"<root>","task_dir":"<root>/.duolog/tasks/task-d-done01"}]` + "\n"

const pinnedRunsJSON = `[{"id":"peer-list","kind":"peer","role":"plan","agent":"claude","model":"","state":"ready","started":null,"worktree":"<root>","task_dir":"<root>/.duolog"},` +
	pinnedRunsDelegates

// runs --json, byte for byte: the peer, the running run, then the newest
// five finished.
func TestRunsJSONOutputIsPinned(t *testing.T) {
	root := runsPinnedProject(t)
	out, errOut, err := runsOutputs(t, "runs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if got := pinned(t, out, root, ""); got != pinnedRunsJSON || errOut != "" {
		t.Fatalf("runs --json =\n%s\nwant\n%s\nstderr %q", got, pinnedRunsJSON, errOut)
	}
}

// Without Rex, runs --json is the delegate runs only and says nothing on
// stderr.
func TestRunsJSONWithoutRexIsPinned(t *testing.T) {
	root := runsPinnedProject(t)
	t.Setenv("REX_SESSION", "")
	out, errOut, err := runsOutputs(t, "runs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if want := "[" + pinnedRunsDelegates; pinned(t, out, root, "") != want || errOut != "" {
		t.Fatalf("runs --json =\n%s\nwant\n%s\nstderr %q", pinned(t, out, root, ""), want, errOut)
	}
}

// snapshotOf runs duolog with args and reads its stdout as one snapshot line.
func snapshotOf(t *testing.T, args ...string) corerunsnap.Snapshot {
	t.Helper()
	out, errOut, err := runsOutputs(t, args...)
	if err != nil {
		t.Fatalf("%v: %v, stderr %q", args, err, errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("not one line: %q", out)
	}
	var s corerunsnap.Snapshot
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	return s
}

func TestRunsSnapshotIsOneJSONLine(t *testing.T) {
	runsPinnedProject(t)
	s := snapshotOf(t, "runs", "--snapshot")
	if s.Version != 1 || s.Lead == nil || s.Lead.ID != "lead-1" || !s.Rex.Available {
		t.Fatalf("snapshot %+v", s)
	}
	if len(s.Peers) != 1 || s.Peers[0].ID != "peer-list" || len(s.Runs) != 6 || s.Runs[0].ID != "d-newrun" || s.Runs[0].State != "running" {
		t.Fatalf("peers %+v, runs %+v", s.Peers, s.Runs)
	}
	if s.Runs[0].Worktree != ".worktrees/feature" || s.Runs[1].Worktree != "." {
		t.Fatalf("worktrees %q %q", s.Runs[0].Worktree, s.Runs[1].Worktree)
	}
	if _, err := time.Parse(time.RFC3339, s.GeneratedAt); err != nil {
		t.Fatalf("generated_at %q: %v", s.GeneratedAt, err)
	}
	if s.LineText != s.Line() || s.LineText == "" {
		t.Fatalf("line %q, Line() %q", s.LineText, s.Line())
	}
}

func TestRunsSnapshotWithoutRexOrLead(t *testing.T) {
	peerProjectWithoutLead(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	t.Setenv("REX_SESSION", "")
	s := snapshotOf(t, "runs", "--snapshot")
	if s.Lead != nil || s.Rex.Available || s.Peers == nil || len(s.Peers) != 0 || s.Error != "" {
		t.Fatalf("snapshot %+v", s)
	}
}

// A lead that was asked for and does not resolve lists nothing: the
// snapshot never widens to every lead's runs.
func TestRunsSnapshotLeadThatDoesNotResolve(t *testing.T) {
	runsPinnedProject(t)
	s := snapshotOf(t, "runs", "--snapshot", "--lead", "lead-nothere")
	if s.Lead != nil || len(s.Peers) != 0 || len(s.Runs) != 0 || !strings.HasPrefix(s.Error, "lead: ") {
		t.Fatalf("snapshot %+v", s)
	}
}

// The snapshot's load comes from the cache: it reads no quota, though it
// counts the listed peers.
func TestRunsSnapshotReadsNoQuota(t *testing.T) {
	runsPinnedProject(t)
	d := testDeps(t)
	reads, opens := 0, 0
	open := d.Delegate.PaidGate
	d.Delegate.PaidGate = func(cfg *config.Config) (*paidruns.Gate, error) {
		opens++
		g, err := open(cfg)
		if err != nil {
			return nil, err
		}
		read := g.Cache.Read
		g.Cache.Read = func(ctx context.Context, p workerenv.Process) (quota.Snapshot, error) {
			reads++
			return read(ctx, p)
		}
		return g, nil
	}
	snapshotOf(t, "runs", "--snapshot")
	if reads != 0 || opens == 0 {
		t.Fatalf("%d quota reads, %d gate opens", reads, opens)
	}
}

func TestRunsSnapshotRefusals(t *testing.T) {
	runsPinnedProject(t)
	for _, other := range []string{"--long", "--json"} {
		if _, _, err := runsOutputs(t, "runs", "--snapshot", other); err == nil || err.Error() != "--snapshot cannot be used with --long or --json" {
			t.Fatalf("%s: %v", other, err)
		}
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, _, err := runsOutputs(t, "runs", "--snapshot"); err == nil || err.Error() != "workers cannot list or watch delegate runs" {
		t.Fatalf("worker: %v", err)
	}
}

// In a folder with no .duolog and no Git, the snapshot is empty and creates
// nothing: a status line that polls it leaves no folders behind.
func TestRunsSnapshotCreatesNoState(t *testing.T) {
	clearModelMarkers(t)
	isolatedRouteConfig(t)
	t.Setenv("DUOLOG_DEPTH", "")
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("REX_SESSION", "")
	dir := t.TempDir()
	chdir(t, dir)
	s := snapshotOf(t, "runs", "--snapshot")
	if s.Version != 1 || s.Lead != nil || len(s.Peers) != 0 || len(s.Runs) != 0 || s.Error != "" {
		t.Fatalf("snapshot %+v", s)
	}
	// A lead asked for is named as not live, as with a lead home.
	s = snapshotOf(t, "runs", "--snapshot", "--lead", "lead-x")
	if s.Lead != nil || s.Error != "lead: lead-x is not a live lead" {
		t.Fatalf("snapshot %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, ".duolog")); !os.IsNotExist(err) {
		t.Fatalf(".duolog after the snapshot: %v", err)
	}
}

// With two live leads and none named, no lead resolves: the snapshot shows
// no peer and no run of either lead, and its line is empty.
func TestRunsSnapshotWithTwoLeadsShowsNothing(t *testing.T) {
	root, _ := peerProjectWithoutLead(t, testbin.Scenario{Stdout: claudeIdleScreen()})
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"lead-1", "lead-2"} {
		if err := reg.RegisterLead(session.Lead{ID: id, Target: config.Target{Agent: "claude"}, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Record(session.Entry{ID: "peer-list", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", Task: ".duolog/task.md", WorkRoot: root}); err != nil {
		t.Fatal(err)
	}
	for _, lead := range []string{"lead-1", "lead-2"} {
		r := runRec("d-"+lead, "running", time.Minute)
		r.Lead, r.WorkRoot = lead, root
		writeStatusRecord(t, root, r)
	}
	s := snapshotOf(t, "runs", "--snapshot")
	if s.Lead != nil || s.Peers == nil || len(s.Peers) != 0 || s.Runs == nil || len(s.Runs) != 0 || s.LineText != "" || s.Error != "" {
		t.Fatalf("snapshot %+v", s)
	}
}

// runs --snapshot --fast reads no pane screen: with a Rex whose capture
// hangs, the peers are still listed, as unknown, and the command ends.
func TestRunsSnapshotFast(t *testing.T) {
	root, _ := peerProject(t, testbin.Scenario{SleepMS: 60000})
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Record(session.Entry{ID: "peer-fast", Lead: "lead-1", Agent: "claude", Role: "plan", Block: "block:peer", RexSession: "session:old", WorkRoot: root}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := newRootCommand(testDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"runs", "--snapshot", "--fast"})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("%v", err)
	}
	var s corerunsnap.Snapshot
	if err := json.Unmarshal(out.Bytes(), &s); err != nil {
		t.Fatalf("%q: %v", out.String(), err)
	}
	if len(s.Peers) != 1 || s.Peers[0].State != "unknown" || s.Peers[0].Working {
		t.Fatalf("peers %+v", s.Peers)
	}
}

func TestRunsFastNeedsSnapshot(t *testing.T) {
	runsPinnedProject(t)
	if _, _, err := runsOutputs(t, "runs", "--fast"); err == nil || err.Error() != "--fast needs --snapshot" {
		t.Fatalf("%v", err)
	}
}
