package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/store"
)

// mcpDelegatePoll is how often a waiting delegate call reads the record, and
// mcpDelegateMinute a minute of its timeout; tests make both short.
var (
	mcpDelegatePoll   = 500 * time.Millisecond
	mcpDelegateMinute = time.Minute
)

// mcpDelegateDefaultWait is the wait, in minutes, of a call that names none.
const mcpDelegateDefaultWait = 10

// mcpDelegate is the delegate tool: action run starts `duolog delegate` as a
// detached child that writes its result record, and action result reads a
// record back.
func (d *cmdDeps) mcpDelegate(ctx context.Context, in mcp.DelegateInput) (mcp.DelegateOutput, error) {
	switch in.Action {
	case "", "run":
		return d.mcpDelegateRun(ctx, in)
	case "result":
		return mcpDelegateResult(in.TaskPath)
	default:
		return mcp.DelegateOutput{}, fmt.Errorf("delegate action %q is unknown; use run or result", in.Action)
	}
}

// mcpDelegateRun starts the delegation and waits for it when the call asks.
func (d *cmdDeps) mcpDelegateRun(ctx context.Context, in mcp.DelegateInput) (mcp.DelegateOutput, error) {
	if in.Role == "" || in.Task == nil {
		return mcp.DelegateOutput{}, errors.New("delegate needs role and task; for an earlier run use action=result with its task_path")
	}
	path, rel, err := d.mcpDelegateStart(in)
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	if !in.Wait {
		return mcp.DelegateOutput{State: "running", TaskPath: rel, AnswerPath: path}, nil
	}
	r, err := mcpDelegateWait(ctx, path, in.TimeoutMinutes)
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	return mcpDelegateOutput(path, r), nil
}

// mcpDelegateStart writes the task file and the running record, and starts
// `duolog delegate` as a detached child with the lead resolved now. It
// returns the record's path and the task file's path in the work root.
func (d *cmdDeps) mcpDelegateStart(in mcp.DelegateInput) (path, rel string, err error) {
	stateDir, work, lead, err := mcpDelegateLead()
	if err != nil {
		return "", "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	id := "d-" + strings.ToLower(rand.Text()[:6])
	if rel, err = writeTaskFile(work, id, *in.Task); err != nil {
		return "", "", err
	}
	dir := delegate.RecordDir(stateDir)
	path = filepath.Join(dir, id+".json")
	base := delegate.Record{ID: id, Role: in.Role, Lead: lead, TaskPath: rel, WorkRoot: work, State: "running", Started: time.Now().UTC()}
	if err := delegate.WriteRecord(path, base); err != nil {
		return "", "", err
	}
	argv := []string{exe, "delegate", in.Role, "--task", rel, "--lead", lead, "--result-file", path}
	if in.Use != "" {
		argv = append(argv, "--use", in.Use)
	}
	if len(in.Needs) > 0 {
		argv = append(argv, "--needs", strings.Join(in.Needs, ","))
	}
	// From here on only the child writes the record: its PID when it starts,
	// and its outcome at its end. A write here could land on its final record.
	if err := d.startDetached(argv, work, filepath.Join(dir, id+".log")); err != nil {
		err = fmt.Errorf("cannot start duolog delegate: %w; run duolog delegate %s --task %s from the shell", err, in.Role, rel)
		_ = delegate.WriteRecord(path, delegate.RecordFor(base, delegate.Outcome{}, err, time.Now().UTC()))
		return "", "", err
	}
	return path, rel, nil
}

// mcpDelegateLead resolves the project's state dir, the work root and the
// lead's ID as duolog delegate would. The server is the lead's child, so the
// ancestor walk finds the lead.
func mcpDelegateLead() (stateDir, work, lead string, err error) {
	root, err := projectRoot()
	if err != nil {
		return "", "", "", err
	}
	s, err := store.Open(root)
	if err != nil {
		return "", "", "", err
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		return "", "", "", err
	}
	l, err := reg.ResolveLead("")
	if err != nil {
		return "", "", "", err
	}
	return s.Dir, workRoot(root), l.ID, nil
}

// mcpDelegateWait reads the record at path until it is no longer running,
// the timeout passes or ctx ends, and returns the last one it read.
func mcpDelegateWait(ctx context.Context, path string, minutes int) (delegate.Record, error) {
	deadline := time.NewTimer(mcpDelegateTimeout(minutes))
	defer deadline.Stop()
	for {
		r, err := delegate.ReadRecord(path, session.ProcessAlive)
		if err != nil || r.State != "running" {
			return r, err
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

// mcpDelegateResult returns the newest record whose task file or id is key.
func mcpDelegateResult(key string) (mcp.DelegateOutput, error) {
	if key == "" {
		return mcp.DelegateOutput{}, errors.New("delegate result needs task_path; status lists them")
	}
	root, err := projectRoot()
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	s, err := store.Open(root)
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	dir := delegate.RecordDir(s.Dir)
	records, err := delegate.ListRecords(dir, session.ProcessAlive)
	if err != nil {
		return mcp.DelegateOutput{}, err
	}
	for _, r := range records {
		if r.ID == key || r.TaskPath == key || filepath.Join(r.WorkRoot, filepath.FromSlash(r.TaskPath)) == filepath.Clean(key) {
			return mcpDelegateOutput(filepath.Join(dir, r.ID+".json"), r), nil
		}
	}
	return mcp.DelegateOutput{}, fmt.Errorf("no delegation with task_path %s; status lists them", key)
}

// mcpDelegateOutput is record r, read from path, as the tool's result.
func mcpDelegateOutput(path string, r delegate.Record) mcp.DelegateOutput {
	out := mcp.DelegateOutput{State: r.State, TaskPath: r.TaskPath, Answer: r.Line, AnswerPath: path, Needs: slices.Clone(r.Needs)}
	if p := r.RunHere; p != nil {
		out.RunHere = &mcp.RunHere{Mode: p.Where, Model: p.Model, Effort: p.Effort, Task: p.Task}
	}
	return out
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
