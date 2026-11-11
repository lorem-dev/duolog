package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/claude"
	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/delegate/delegatetest"
	"github.com/lorem-dev/duolog/internal/app/hook"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/app/peer/peertest"
	"github.com/lorem-dev/duolog/internal/bus"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testutil/submodule"
)

// This uses the real command, app, registry and bus boundaries. Only agents,
// account reads, time and the pane backend are fake; Git repositories are real.
func TestSubmoduleLeadEndToEnd(t *testing.T) {
	d, f, named, _ := submoduleDelegateProject(t)
	f.Linked = named
	reg, worker := submoduleE2ESetup(t, d, f)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	task := mcpTaskFixture(t, f.Linked)
	rel, err := writeTaskFile(f.Linked, "cli-e2e", *task)
	if err != nil {
		t.Fatal(err)
	}
	chdir(t, f.Linked)
	cli, err := executeBusCommand(t, "delegate", "implement", "--task", rel)
	if err != nil || !strings.Contains(cli, "done=true") {
		t.Fatal(cli, err)
	}
	chdir(t, f.Superproject)
	childOutput := submoduleE2EDetached(t, d, f.Linked)
	started, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: task, Worktree: f.Linked, Wait: &no})
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: started.ID})
	if err != nil || result.Summary != "submodule result" || result.Done == nil || !*result.Done {
		t.Fatal(result, err)
	}
	if !underDir(t, result.AnswerPath, filepath.Join(f.Submodule, ".duolog", "delegations")) {
		t.Fatal(result.AnswerPath)
	}
	submoduleE2EWorkState(t, worker, f)
	peerNotice := submoduleE2EPeers(t, ctx, d, f, task)
	if count := strings.Count(cli+started.Answer+*childOutput+peerNotice, "Submodules with their own duolog rules:"); count != 1 {
		t.Fatalf("reminder count = %d", count)
	}
	submoduleE2EBus(t, ctx, d, f)
	submoduleE2EGate(t, f)
	submoduleE2EBoundaries(t, ctx, d, worker, f)
	status, err := d.mcpStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row := delegationRow(t, status.Delegations, result.AnswerPath); row.WorkRoot != f.Linked || row.State != "done" {
		t.Fatal(row)
	}
	submoduleE2ENoLocalLeadState(t, f)
	// A fresh worker made no resumable entry. Its result remains discoverable
	// by ID after the lead ends and its worktree is removed.
	if err := reg.Close("lead-mcp"); err != nil {
		t.Fatal(err)
	}
	submodule.Git(t, f.Submodule, "worktree", "remove", "--force", f.Linked)
	result, err = d.mcpDelegate(ctx, mcp.DelegateInput{Action: "result", TaskPath: started.ID})
	if err != nil || result.Summary != "submodule result" {
		t.Fatal(result, err)
	}
}

func submoduleE2ESetup(t *testing.T, d *cmdDeps, f submodule.Fixture) (*session.Registry, *delegatetest.Fake) {
	t.Helper()
	t.Setenv("DUOLOG_LEAD", "lead-mcp")
	t.Setenv("DUOLOG_MODE", "")
	t.Setenv("DUOLOG_ROLE", "")
	t.Setenv("REX_SESSION", "session:lead")
	common := "routing:\n  small_task_files: 0\n  balance: off\nlearning:\n  enabled: true\n"
	super := common + "limits:\n  max_live_sessions: 1\nroles:\n  implement:\n    use: {agent: codex, model: sol}\n    balance: false\n  plan:\n    use: {agent: claude, model: haiku}\n    balance: false\n"
	sub := common + "limits:\n  max_live_sessions: 8\nroles:\n  implement:\n    use: {agent: claude, model: haiku}\n    session: fresh\n    balance: false\npolicy:\n  rules:\n    - {action: exec, match: 'e2e-submodule-denied *', decision: deny}\n"
	submoduleFile(t, f.Superproject, ".duolog/config.yaml", super)
	submoduleFile(t, f.Submodule, ".duolog/config.yaml", sub)
	submoduleFile(t, f.Unrelated, ".duolog/config.yaml", super)
	submoduleFile(t, f.Superproject, ".claude/settings.local.json", `{"permissions":{"deny":["Bash(e2e-lead-denied:*)"]}}`)
	reg, err := session.Open(project.LeadHome(f.Superproject))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "lead-mcp", Target: config.Target{Agent: "claude", Model: "opus"}, Root: f.Superproject, PID: os.Getpid(), Mode: mode.Full}); err != nil {
		t.Fatal(err)
	}
	fake := delegatetest.New(t)
	fake.Result.Summary = "submodule result"
	d.Delegate = fake.Deps()
	for name, agent := range d.Delegate.Agents {
		agent.Trust = &delegatetest.Trust{State: coreagent.TrustState{Trusted: true, Installed: true}, FolderTrusted: true}
		d.Delegate.Agents[name] = agent
	}
	return reg, fake
}

// Run the detached argv through Cobra in the requested checkout, then restore
// the server's cwd before the MCP handler resumes. No result is fabricated.
func submoduleE2EDetached(t *testing.T, d *cmdDeps, work string) *string {
	t.Helper()
	var output string
	d.startDetached = func(argv []string, dir, logPath string) error {
		if !sameDir(t, dir, work) {
			t.Fatalf("detached cwd = %s, want %s", dir, work)
		}
		cwd := mustGetwd(t)
		if err := os.Chdir(dir); err != nil {
			return err
		}
		var log bytes.Buffer
		cmd := newRootCommand(d)
		cmd.SetOut(&log)
		cmd.SetErr(&log)
		cmd.SetArgs(argv[1:])
		err := cmd.Execute()
		if restoreErr := os.Chdir(cwd); restoreErr != nil {
			t.Fatal(restoreErr)
		}
		output += log.String()
		if err != nil {
			t.Fatalf("detached delegate: %s: %v", log.String(), err)
		}
		return os.WriteFile(logPath, log.Bytes(), 0o600)
	}
	return &output
}

func submoduleE2EWorkState(t *testing.T, worker *delegatetest.Fake, f submodule.Fixture) {
	t.Helper()
	if len(worker.Calls) != 2 {
		t.Fatalf("worker calls = %d, want CLI and MCP", len(worker.Calls))
	}
	for _, spec := range worker.Calls {
		if spec.Route.Agent != "claude" || spec.Route.Model != "haiku" || !sameDir(t, spec.Cwd, f.Linked) || spec.ThreadID != "" {
			t.Fatalf("wrong submodule worker launch: %+v", spec)
		}
	}
	reg, err := session.Open(project.LeadHome(f.Linked))
	if err != nil {
		t.Fatal(err)
	}
	if entries, err := reg.Entries(); err != nil || len(entries) != 0 {
		t.Fatal("fresh delegation retained a reusable entry", entries, err)
	}
	j := &journal.Journal{Dir: filepath.Join(f.Submodule, ".duolog", "journal")}
	records, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
	if err != nil || len(records) != 2 {
		t.Fatalf("work journal: %+v %v", records, err)
	}
	if data, err := os.ReadFile(filepath.Join(f.Submodule, ".duolog", "usage.ndjson")); err != nil || !bytes.Contains(data, []byte("haiku")) {
		t.Fatalf("work usage: %s %v", data, err)
	}
	for _, name := range []string{"journal", "usage.ndjson", "tasks"} {
		if _, err := os.Stat(filepath.Join(f.Superproject, ".duolog", name)); !os.IsNotExist(err) {
			t.Fatal("work state moved to lead home", name, err)
		}
	}
}

// Advancing peer time avoids wall-clock waits while retaining the watch loop.
type submoduleE2EClock struct{ now time.Time }

func (c *submoduleE2EClock) Now() time.Time { return c.now }
func (c *submoduleE2EClock) Sleep(ctx context.Context, d time.Duration) error {
	c.now = c.now.Add(d)
	return ctx.Err()
}

func submoduleE2EPane(t *testing.T, d *cmdDeps) *peertest.FakePane {
	t.Helper()
	pane := &peertest.FakePane{LeadSession: "session:lead"}
	script := peertest.ContractScript()
	script["capture"] = peertest.Reply{Text: claudeIdleScreen()}
	pane.Reset(script)
	build := d.PeerDeps
	d.PeerDeps = func(dd delegate.Deps) peer.Deps {
		p := build(dd)
		p.Pane = pane
		p.Clock = &submoduleE2EClock{now: time.Now()}
		for name, agent := range dd.Agents {
			p.Models[name] = agent.Models
		}
		p.OnEnvFile = func(path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	return pane
}

func submoduleE2EPeers(t *testing.T, ctx context.Context, d *cmdDeps, f submodule.Fixture, task *mcp.Task) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		out, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "open", Role: "implement", Worktree: f.Linked})
		if err != nil || out.Line != peer.WindowsNotice {
			t.Fatal(out, err)
		}
		return ""
	}
	pane := submoduleE2EPane(t, d)
	call := func(in mcp.PeerInput) mcp.PeerOutput {
		t.Helper()
		out, err := d.mcpPeer(ctx, in)
		if err != nil {
			t.Fatalf("peer %s: %v", in.Action, err)
		}
		return out
	}
	opened := call(mcp.PeerInput{Action: "open", Role: "implement", Worktree: f.Linked})
	byName := call(mcp.PeerInput{Action: "open", Role: "implement", Worktree: "feature"})
	if opened.ID == "" || byName.ID != opened.ID {
		t.Fatal(opened, byName)
	}
	submoduleE2EPeerLaunch(t, pane, f.Linked)
	submoduleE2ECap(t, ctx, d, pane)
	line := "Read .duolog/tasks/" + opened.ID + "-send-1/task.md and do it. Do not call rex. Write the answer to " + policy.ReportPath(opened.ID) + ". Reply \"done\"."
	script := peertest.ContractScript()
	script["capture"] = peertest.Reply{Sequence: []peertest.Reply{{Text: claudeIdleScreen()}, {Text: claudeIdleScreen()}, {Text: claudeHolding(line)}, {Text: claudeAtWork()}}}
	pane.Reset(script)
	sent := call(mcp.PeerInput{Action: "send", ID: opened.ID, Task: task})
	if _, err := os.Stat(filepath.Join(f.Linked, filepath.FromSlash(sent.TaskPath))); err != nil {
		t.Fatal("task not in target checkout", err)
	}
	submoduleFile(t, f.Linked, policy.ReportPath(opened.ID), "submodule peer report\n")
	script["capture"] = peertest.Reply{Text: claudeIdleScreen()}
	pane.Reset(script)
	waited := call(mcp.PeerInput{Action: "wait", ID: opened.ID})
	if waited.Report != "submodule peer report\n" || !underDir(t, waited.ReportPath, f.Linked) {
		t.Fatal(waited)
	}
	submoduleE2EPeerViews(t, ctx, d, pane, f, opened.ID)
	call(mcp.PeerInput{Action: "close", ID: opened.ID})
	return opened.Line + byName.Line
}

func submoduleE2EPeerLaunch(t *testing.T, pane *peertest.FakePane, work string) {
	t.Helper()
	opens := 0
	for _, call := range pane.Calls() {
		if call[0] != "open-session" && call[0] != "open-tab" {
			continue
		}
		opens++
		if !sameDir(t, call[3], work) || !strings.Contains(strings.Join(call[5:], " "), "--model haiku") {
			t.Fatal("wrong peer cwd or model", call)
		}
	}
	if opens != 1 {
		t.Fatalf("path and bare name opened %d panes", opens)
	}
}

func submoduleE2ECap(t *testing.T, ctx context.Context, d *cmdDeps, pane *peertest.FakePane) {
	t.Helper()
	before := len(pane.Calls())
	_, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "open", Role: "plan"})
	if err == nil || !strings.Contains(err.Error(), "max_live_sessions is 1") {
		t.Fatalf("superproject bypassed shared cap: %v", err)
	}
	for _, call := range pane.Calls()[before:] {
		if call[0] == "open-session" || call[0] == "open-tab" {
			t.Fatal("cap refusal started a pane", call)
		}
	}
}

func submoduleE2EPeerViews(t *testing.T, ctx context.Context, d *cmdDeps, pane *peertest.FakePane, f submodule.Fixture, id string) {
	t.Helper()
	question := startupFixture(t, "claude-model-switch-question.txt")
	script := peertest.ContractScript()
	script["capture"] = peertest.Reply{Text: question}
	pane.Reset(script)
	if out, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "status", ID: id}); err != nil || out.State != "question" {
		t.Fatal(out, err)
	}
	script["capture"] = peertest.Reply{Sequence: []peertest.Reply{{Text: question}, {Text: claudeIdleScreen()}}}
	pane.Reset(script)
	if out, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "answer", ID: id, Option: "Yes"}); err != nil || out.State != "ready" {
		t.Fatal(out, err)
	}
	listed, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "list"})
	if err != nil || len(listed.Peers) != 1 || listed.Peers[0].ID != id || listed.Peers[0].Agent != "claude" || !sameDir(t, listed.Peers[0].WorkRoot, f.Linked) {
		t.Fatal(listed, err)
	}
	status, err := d.mcpStatus(ctx)
	if err != nil || len(status.Peers) != 1 || !sameDir(t, status.Peers[0].WorkRoot, f.Linked) {
		t.Fatal(status, err)
	}
}

func submoduleE2EBus(t *testing.T, ctx context.Context, d *cmdDeps, f submodule.Fixture) {
	t.Helper()
	address := session.Address(session.Lead{ID: "lead-mcp", Target: config.Target{Agent: "claude"}})
	chdir(t, f.Linked)
	number, err := executeBusCommand(t, "send", "--from", "claude/ask.e2e", "--to", address, "--kind", "ask", "submodule permission")
	if err != nil {
		t.Fatal(number, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	chdir(t, f.Superproject)
	var stderr bytes.Buffer
	e := hook.Env{Root: f.Superproject, WorkRoot: f.Superproject, Agent: "claude", Events: claude.Events{}, Lead: claude.Lead{}, Input: []byte(`{"session_id":"lead-mcp"}`), Environ: []string{"CLAUDE_CODE_ENTRYPOINT=cli"}, Stderr: &stderr}
	if code := hook.Wake(ctx, e); code != 2 || !strings.Contains(stderr.String(), "submodule permission") {
		t.Fatal("disconnected wake", code, stderr.String())
	}
	if _, err := d.mcpAnswerAsk(ctx, mcp.AnswerAskInput{Ask: n, Decision: "allow"}); err != nil {
		t.Fatal(err)
	}
	chdir(t, f.Linked)
	b, err := openBus()
	if err != nil {
		t.Fatal(err)
	}
	mail, err := b.Inbox("claude/ask.e2e")
	if err != nil || len(mail) != 1 {
		t.Fatal(mail, err)
	}
	var answer bus.Message
	if err := json.Unmarshal(mail[0], &answer); err != nil || answer.K != "allow" || answer.Ref != strconv.FormatInt(n, 10) {
		t.Fatal(answer, err)
	}
	if _, err := executeBusCommand(t, "send", "--from", "claude/worker", "--to", address, "--kind", "status", "submodule finished"); err != nil {
		t.Fatal(err)
	}
	chdir(t, f.Superproject)
	if out := hook.Prompt(ctx, e); !bytes.Contains(out.Stdout, []byte("submodule finished")) {
		t.Fatal(string(out.Stdout))
	}
}

func submoduleE2EGate(t *testing.T, f submodule.Fixture) {
	t.Helper()
	chdir(t, f.Linked)
	t.Setenv("DUOLOG_DEPTH", "1")
	t.Setenv("DUOLOG_ROLE", "implement")
	for _, command := range []string{"e2e-lead-denied x", "e2e-submodule-denied x"} {
		if out := runGate(t, command); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Fatal("lost deny", command, out)
		}
	}
	if out := runGate(t, "touch a.go"); out != "" {
		t.Fatal(out)
	}
	t.Setenv("DUOLOG_DEPTH", "")
	chdir(t, f.Superproject)
	if out, err := executeBusCommand(t, "mode", "plan"); err != nil {
		t.Fatal(out, err)
	}
	chdir(t, f.Linked)
	t.Setenv("DUOLOG_DEPTH", "1")
	if out := runGate(t, "touch a.go"); !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Fatal("lowered mode not applied", out)
	}
	t.Setenv("DUOLOG_DEPTH", "")
	chdir(t, f.Superproject)
}

func submoduleE2EBoundaries(t *testing.T, ctx context.Context, d *cmdDeps, worker *delegatetest.Fake, f submodule.Fixture) {
	t.Helper()
	t.Setenv("DUOLOG_DEPTH", "1")
	if _, err := executeBusCommand(t, "mode", "full"); err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal("worker mode control was not refused", err)
	}
	if _, err := d.mcpAnswerAsk(ctx, mcp.AnswerAskInput{Ask: 1, Decision: "allow"}); err == nil || !strings.Contains(err.Error(), "workers cannot") {
		t.Fatal("worker ask control was not refused", err)
	}
	if runtime.GOOS != "windows" {
		if _, err := d.mcpPeer(ctx, mcp.PeerInput{Action: "open", Role: "implement", Worktree: f.Linked}); err == nil || !strings.Contains(err.Error(), "workers cannot") {
			t.Fatal("worker peer control was not refused", err)
		}
	}
	t.Setenv("DUOLOG_DEPTH", "")
	if _, err := d.mcpDelegate(ctx, mcp.DelegateInput{Role: "implement", Task: testTask, Worktree: f.Unrelated, Wait: &no}); err == nil || !strings.Contains(err.Error(), "not a checkout") {
		t.Fatal("unrelated repository was not refused by MCP", err)
	}
	chdir(t, f.Unrelated)
	submoduleFile(t, f.Unrelated, "task.md", "## Goal\nCheck.\n\n## Read\nNone.\n\n## Write\nNone.\n\n## Limits\nNo changes.\n\n## Check\nReview.\n")
	if out, err := executeBusCommand(t, "delegate", "implement", "--task", "task.md"); err == nil || !strings.Contains(err.Error(), "not a live lead") {
		t.Fatal("unrelated lead lookup was not refused", out, err)
	}
	if len(worker.Calls) != 2 {
		t.Fatal("boundary refusal launched worker")
	}
	chdir(t, f.Superproject)
}

func submoduleE2ENoLocalLeadState(t *testing.T, f submodule.Fixture) {
	t.Helper()
	for _, root := range []string{f.Submodule, f.Linked} {
		for _, name := range []string{"sessions.json", "bus.ndjson", "decisions.ndjson", "peer-queue.ndjson"} {
			if _, err := os.Stat(filepath.Join(root, ".duolog", name)); !os.IsNotExist(err) {
				t.Fatal("created work-local lead state", name, err)
			}
		}
	}
}
