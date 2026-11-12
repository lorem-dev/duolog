package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	leadapp "github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/core/policy"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// mcpDelegatePoll is how often a waiting delegate call reads the record, and
// mcpDelegateMinute a minute of its timeout; tests make both short.
var (
	mcpDelegatePoll   = 500 * time.Millisecond
	mcpDelegateMinute = time.Minute
)

// mcpDelegateDefaultWait is the wait, in minutes, of a call that names none.
// It is also the most a call waits (mcp.MaxWaitMinutes): the same bound as
// `peer wait`, and the gate's own longest wait (600 seconds of a person's ask,
// with the hook's 30 seconds on top, install.GateTimeout). Neither the code
// nor the docs state the MCP client's tool timeout, so duolog keeps to the
// longest wait it already trusts. A client may still give up before the reply:
// the run goes on and the lead has no id, so a delegate call for the same task
// while its run is running returns that run (mcpRunningTwin), and status lists
// the runs.
const mcpDelegateDefaultWait = 10

// mcpDelegate is the delegate tool: action run starts `duolog delegate` as a
// detached child that writes its result record, and waits for it unless the
// call says wait false; action result reads a record back, and waits for it
// the same way while it runs.
func (d *cmdDeps) mcpDelegate(ctx context.Context, in mcp.DelegateInput) (mcp.DelegateOutput, error) {
	switch in.Action {
	case "", "run":
		return d.mcpDelegateRun(ctx, in)
	case "result":
		return mcpDelegateResult(ctx, in)
	default:
		return mcp.DelegateOutput{}, fmt.Errorf("delegate action %q is unknown; use run or result", in.Action)
	}
}

// mcpDelegateRun starts the delegation and waits for it when the call asks.
func (d *cmdDeps) mcpDelegateRun(ctx context.Context, in mcp.DelegateInput) (out mcp.DelegateOutput, err error) {
	if in.Task == nil && in.TaskPath != "" {
		// Codex leads pass a task file here; the tool reads no file.
		return mcp.DelegateOutput{}, errors.New("task_path is only for action=result, with the id of a running call; to start a run, pass task as an object with goal, read, write, limits and check, or run duolog delegate <role> <file> for a task file")
	}
	if in.Role == "" || in.Task == nil {
		return mcp.DelegateOutput{}, errors.New("delegate needs role and task; for an earlier run use action=result with its task_path")
	}
	stateDir, work, lead, err := mcpDelegateLead(in.Worktree, hintsFrom(ctx))
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	reminder, err := mcpSubmoduleReminder(work, lead)
	if err != nil {
		return out, err
	}
	defer func() {
		out.Answer = strings.TrimSpace(out.Answer + " " + reminder)
		if err != nil && reminder != "" {
			err = fmt.Errorf("%w; %s", err, reminder)
		}
	}()
	target := mcpDelegateTarget{stateDir: stateDir, work: work, lead: lead}
	path, _, err := func() (string, string, error) {
		// Retries serialize in the work project they run in.
		unlock, err := mcpDelegateLock(ctx, filepath.Join(stateDir, "mcp-delegate.lock"))
		if err != nil {
			return "", "", err
		}
		defer func() { _ = unlock() }()
		path, rel := mcpRunningTwin(in, target)
		if path != "" {
			return path, rel, nil
		}
		return d.mcpDelegateStart(in, target)
	}()
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	if in.Waits() {
		defer mcpWaitMark(work, lead, path)()
	}
	r, err := mcpDelegateAwait(ctx, path, in.TimeoutMinutes, !in.Waits())
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	return mcpDelegateOutput(path, r), nil
}

func mcpSubmoduleReminder(work, lead string) (string, error) {
	reg, err := session.Open(project.LeadHome(work))
	if err != nil {
		return "", err
	}
	return leadapp.ClaimSubmoduleReminder(reg, lead, work)
}

// mcpRunningTwin finds a run of this lead and role that is still running for a
// task file with the same text as task renders to, and returns its record's
// path and task path; path is empty when there is none. A client that gave up
// before the reply calls again with the same task, and gets the run it has, not
// a second worker. Any error means no twin: starting reports it.
func mcpRunningTwin(in mcp.DelegateInput, target mcpDelegateTarget) (recPath, rel string) {
	role := in.Role
	data, err := taskspec.Render(taskspec.Spec(*in.Task))
	if err != nil {
		return "", ""
	}
	work, lead := target.work, target.lead
	dir := delegate.RecordDir(target.stateDir)
	records, err := delegate.ListRecords(dir, session.ProcessAlive)
	if err != nil {
		return "", ""
	}
	for _, r := range records {
		if r.State != "running" || r.Role != role || r.Lead != lead || checkoutKey(r.WorkRoot) != checkoutKey(work) || r.TaskPath != path.Join(".duolog", "tasks", r.ID, "task.md") {
			continue
		}
		if r.Use != in.Use || r.Force != in.Force || r.SkipPreflight != in.SkipPreflight || !sameNeeds(r.AskedNeeds, in.Needs) {
			continue
		}
		if got, err := os.ReadFile(filepath.Join(r.WorkRoot, filepath.FromSlash(r.TaskPath))); err == nil && bytes.Equal(got, data) {
			return filepath.Join(dir, r.ID+".json"), r.TaskPath
		}
	}
	return "", ""
}

// sameNeeds reports whether a and b name the same needs, in any order.
func sameNeeds(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// mcpDelegateTarget is where a delegation runs: the work project's state
// folder, the checkout, and the lead the server resolved.
type mcpDelegateTarget struct {
	stateDir, work, lead string
}

// mcpDelegateStart writes the task file and the running record, records the
// run's work project in the lead's registry, and starts `duolog delegate` as
// a detached child in the checkout, bound to the lead resolved now. It
// returns the record's path and the task file's path in the work root.
func (d *cmdDeps) mcpDelegateStart(in mcp.DelegateInput, target mcpDelegateTarget) (path, rel string, err error) {
	work, lead := target.work, target.lead
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	id := delegate.NewRecordID()
	if rel, err = writeTaskFile(work, id, *in.Task); err != nil {
		return "", "", err
	}
	dir := delegate.RecordDir(target.stateDir)
	base := delegate.Record{ID: id, Role: in.Role, Lead: lead, TaskPath: rel, WorkRoot: work, State: "running", Started: time.Now().UTC(), Use: in.Use, AskedNeeds: slices.Clone(in.Needs), Force: in.Force, SkipPreflight: in.SkipPreflight}
	if path, err = delegate.CreateRecord(dir, base, session.ProcessAlive); err != nil {
		return "", "", err
	}
	if err := delegate.IndexRecord(target.stateDir, lead, id); err != nil {
		_ = delegate.WriteRecord(path, delegate.RecordFor(base, delegate.Outcome{}, err, time.Now().UTC()))
		return "", "", err
	}
	argv := mcpDelegateArgv(exe, in, rel, lead, path)
	// From here on only the child writes the record: its PID when it starts,
	// and its outcome at its end. A write here could land on its final record.
	if err := d.startDetached(argv, work, filepath.Join(dir, id+".log")); err != nil {
		err = fmt.Errorf("cannot start duolog delegate: %w; run duolog delegate %s --task %s from the shell", err, in.Role, rel)
		_ = delegate.WriteRecord(path, delegate.RecordFor(base, delegate.Outcome{}, err, time.Now().UTC()))
		return "", "", err
	}
	return path, rel, nil
}

// mcpDelegateArgv is the detached `duolog delegate` command of a run.
func mcpDelegateArgv(exe string, in mcp.DelegateInput, rel, lead, path string) []string {
	argv := []string{exe, "delegate", in.Role, "--task", rel, "--lead", lead, "--result-file", path}
	if in.Use != "" {
		argv = append(argv, "--use", in.Use)
	}
	if in.SkipPreflight {
		argv = append(argv, "--skip-preflight")
	}
	if in.Force {
		argv = append(argv, "--force")
	}
	if len(in.Needs) > 0 {
		argv = append(argv, "--needs", strings.Join(in.Needs, ","))
	}
	return argv
}

// mcpDelegateLead resolves where a delegation runs: the checkout worktree
// names (with the peer tool's rules), or the server's own; the state folder of
// that checkout's work project; and the server's lead in its lead home, as
// duolog delegate would find it. hints are the call's lead hints: a Codex
// app-server runs the threads of several leads, so the ancestor walk alone
// cannot tell its lead.
func mcpDelegateLead(worktree string, hints []string) (stateDir, work, lead string, err error) {
	root, err := projectRoot()
	if err != nil {
		return "", "", "", err
	}
	work, workProject := workRoot(root), root
	if worktree != "" {
		if work, err = mcpPeerWorktree(worktree); err != nil {
			return "", "", "", err
		}
		workProject = project.StateRoot(work)
	}
	s, err := store.Open(workProject)
	if err != nil {
		return "", "", "", err
	}
	reg, err := session.Open(project.LeadHome(root))
	if err != nil {
		return "", "", "", err
	}
	l, err := reg.ResolveLead("", hints...)
	if err != nil {
		return "", "", "", leadapp.SubmoduleLookupError(err, work)
	}
	return s.Dir, work, l.ID, nil
}

// mcpWaitMark marks lead, in the registry of the work project work, as
// waiting on a foreground delegate while a delegate call blocks it on the run
// whose record is at path, and returns what clears the mark. The detached run
// marks nothing itself: a call with wait false leaves the lead free, and a
// worker's ask then reaches it. The mark carries this server's PID, so a
// server that dies leaves no wait behind. Any error marks nothing.
func mcpWaitMark(work, lead, path string) func() {
	if work == "" || lead == "" {
		return func() {}
	}
	reg, err := session.Open(project.LeadHome(work))
	if err != nil {
		return func() {}
	}
	token := "mcp-" + strings.TrimSuffix(filepath.Base(path), ".json")
	if reg.SetWaiting(lead, token, os.Getpid()) != nil {
		return func() {}
	}
	return func() { _ = reg.SetWaiting(lead, token, 0) }
}

// mcpDelegateWait reads the record at path until it is no longer running,
// the timeout passes or ctx ends, and returns the last one it read.
func mcpDelegateWait(ctx context.Context, path string, minutes int) (delegate.Record, error) {
	return mcpDelegateAwait(ctx, path, minutes, false)
}

// mcpDelegateAwait waits for an outcome or, with admissionOnly, a real wait
// or worker start. Merely launching the supervisor is not admission.
func mcpDelegateAwait(ctx context.Context, path string, minutes int, admissionOnly bool) (delegate.Record, error) {
	deadline := time.NewTimer(mcpDelegateTimeout(minutes))
	defer deadline.Stop()
	for {
		r, err := delegate.ReadRecord(path, session.ProcessAlive)
		if err != nil || r.State != "running" {
			return r, err
		}
		if admissionOnly && (r.Agent != "" || r.WaitReason != "") {
			return r, nil
		}
		select {
		case <-ctx.Done():
			return r, nil
		case <-deadline.C:
			return r, nil
		case <-time.After(mcpDelegatePoll):
		}
	}
}

// mcpDelegateTimeout is the wait of a call that asks for minutes: ten when it
// names none, and never more than mcp.MaxWaitMinutes.
func mcpDelegateTimeout(minutes int) time.Duration {
	if minutes <= 0 {
		minutes = mcpDelegateDefaultWait
	}
	return time.Duration(min(minutes, mcp.MaxWaitMinutes)) * mcpDelegateMinute
}

// mcpDelegateResult returns the run whose id or task file is task_path, in
// the work project that ran it, waiting for it first when it is running and
// the call does not say wait false.
func mcpDelegateResult(ctx context.Context, in mcp.DelegateInput) (mcp.DelegateOutput, error) {
	if in.TaskPath == "" {
		return mcp.DelegateOutput{}, errors.New("delegate result needs task_path; status lists them")
	}
	root, err := projectRoot()
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	reg, err := session.Open(project.LeadHome(root))
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	found, err := mcpFindResult(ctx, reg, root, in)
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	r := found.Record
	if r.State == "running" && in.Waits() {
		defer mcpWaitMark(r.WorkRoot, r.Lead, found.Path)()
		if r, err = mcpDelegateWait(ctx, found.Path, in.TimeoutMinutes); err != nil {
			return mcp.DelegateOutput{}, err
		}
	}
	return mcpDelegateOutput(found.Path, r), nil
}

// mcpDelegateOutput is record r, read from path, as the tool's result.
func mcpDelegateOutput(path string, r delegate.Record) mcp.DelegateOutput {
	out := mcp.DelegateOutput{State: r.State, ID: r.ID, TaskPath: r.TaskPath, Answer: r.Line, AnswerPath: path, Needs: slices.Clone(r.Needs)}
	out.WaitReason = r.WaitReason
	for _, b := range r.Blocked {
		c := mcp.BlockedCheck{Command: b.Command, Decision: b.Decision, Reason: b.Reason}
		if b.Decision == policy.Ask {
			c.Remember = delegate.RememberHint(b.Command)
		}
		out.Blocked = append(out.Blocked, c)
	}
	out.SkippedChecks = slices.Clone(r.Skipped)
	if p := r.RunHere; p != nil {
		out.RunHere = &mcp.RunHere{Mode: p.Where, Model: p.Model, Effort: p.Effort, Task: p.Task}
	}
	// A worker's answer exists once the run ended in done or needs; a run
	// that failed, was lost or is running has none.
	if r.State == "done" || r.State == "needs" {
		done := r.Done
		out.Done, out.Summary = &done, r.Summary
		out.Decisions, out.Files = slices.Clone(r.Decisions), slices.Clone(r.Files)
		out.Diffstat = mcpDiffstat(r.WorkRoot, r.Files)
	}
	return out
}

// mcpDiffstat is git's shortstat of files against HEAD in the work root, plus
// the files that are new to git, as "; new: a, b". It is empty when there are
// no files, the folder is no repository or git fails: the lead then reads the
// diff itself. It changes nothing.
func mcpDiffstat(work string, files []string) string {
	if work == "" || len(files) == 0 {
		return ""
	}
	git := func(args ...string) string {
		argv := append([]string{"--literal-pathspecs", "-C", work}, args...)
		argv = append(argv, "--")
		out, err := exec.Command("git", append(argv, files...)...).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	stat := git("diff", "--shortstat", "HEAD")
	if fresh := strings.Fields(git("ls-files", "--others", "--exclude-standard")); len(fresh) > 0 {
		if stat != "" {
			stat += "; "
		}
		stat += "new: " + strings.Join(fresh, ", ")
	}
	return stat
}

// startDetachedLogged starts argv in dir as a process of its own
// (kit.Detach) and appends its output to the file at logPath. The process
// writes its own PID to its record.
func startDetachedLogged(argv []string, dir, logPath string) error {
	if len(argv) == 0 {
		return errors.New("nothing to start")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	kit.Detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reaped when it ends, while the server lives on.
	go func() { _ = cmd.Wait() }()
	return nil
}

// An uncontended retry can still return its run after its caller cancelled;
// contention is bounded by the caller's context.
func mcpDelegateLock(ctx context.Context, path string) (func() error, error) {
	unlock, ok, err := store.TryLockFile(path)
	if err != nil || ok {
		return unlock, err
	}
	return store.LockFileContext(ctx, path, 10*time.Millisecond)
}
