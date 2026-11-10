package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

// mcpPeerWaitUnit is one timeout_minutes of a peer wait; tests shorten it.
var mcpPeerWaitUnit = time.Minute

// The report a wait returns is at most this many lines and bytes of the
// report file's start.
const (
	mcpReportLines = 40
	mcpReportBytes = 4096
)

// mcpPeer is the peer tool. Each action calls what its `duolog peer`
// subcommand calls, with the same peer context, and returns the line the CLI
// prints together with the fields a lead acts on.
func (d *cmdDeps) mcpPeer(ctx context.Context, in mcp.PeerInput) (mcp.PeerOutput, error) {
	if runtime.GOOS == "windows" {
		return mcp.PeerOutput{Line: peer.WindowsNotice}, nil
	}
	if err := checkPeerInput(in); err != nil {
		return mcp.PeerOutput{}, err
	}
	var timeout time.Duration
	if in.Action == "wait" {
		timeout = mcpPeerTimeout(in.TimeoutMinutes)
	}
	pctx, done, err := d.peerCtx(ctx, "", false, timeout)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	defer done()
	switch in.Action {
	case "open":
		return mcpPeerOpen(pctx, in)
	case "send":
		return mcpPeerSend(pctx, in)
	case "wait":
		return mcpPeerWait(pctx, in.ID, timeout)
	case "answer":
		if err := peer.AnswerWithReason(pctx, in.ID, in.Option, in.Reason); err != nil {
			return mcp.PeerOutput{}, err
		}
		return mcpPeerStatus(pctx, in.ID)
	case "status":
		return mcpPeerStatus(pctx, in.ID)
	case "close":
		line, err := d.peerClose(pctx, nil, []string{in.ID})
		return mcp.PeerOutput{ID: in.ID, Line: line}, err
	default: // list; checkPeerInput refused every other action
		return mcpPeerList(pctx)
	}
}

// mcpPeerOpen is `duolog peer open <role>`, in the named worktree when the
// input has one.
func mcpPeerOpen(ctx context.Context, in mcp.PeerInput) (mcp.PeerOutput, error) {
	if in.Worktree != "" {
		work, err := mcpPeerWorktree(in.Worktree)
		if err != nil {
			return mcp.PeerOutput{}, err
		}
		ctx = peer.WithWorkDir(ctx, work)
	}
	e, err := peer.Open(ctx, in.Role)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	return entryOutput(e), nil
}

// mcpPeerWorktree resolves a plain name under the server checkout's .worktrees
// folder first, then as a path from the server's cwd. Only a checkout of this
// repository qualifies: a linked worktree or the main checkout, at its root,
// so a peer cannot land in a subfolder and edit the wrong tree.
func mcpPeerWorktree(ref string) (string, error) {
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	dir := ref
	if ref != "." && ref != ".." && !strings.ContainsAny(ref, `/\`) {
		named := filepath.Join(workRoot(root), ".worktrees", ref)
		if info, err := os.Stat(named); err == nil && info.IsDir() {
			dir = named
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("worktree %q is not a folder", ref)
	}
	if !project.SameProject(root, abs) {
		return "", fmt.Errorf("worktree %q is outside the project and its linked worktrees", ref)
	}
	if !repositoryCheckout(root, abs) {
		return "", fmt.Errorf("worktree %q is not a checkout of this repository", ref)
	}
	return abs, nil
}

// repositoryCheckout reports whether dir is the root of a checkout that shares
// the state of root's repository.
func repositoryCheckout(root, dir string) bool {
	same := func(a, b string) bool {
		return pathutil.Clean(pathutil.Resolved(a)) == pathutil.Clean(pathutil.Resolved(b))
	}
	top := project.WorkRoot(dir, "")
	return top != "" && same(top, dir) && same(project.StateRoot(dir), project.StateRoot(root))
}

// mcpPeerList is `duolog peer list --json` as rows.
func mcpPeerList(ctx context.Context) (mcp.PeerOutput, error) {
	peers, err := peer.List(ctx)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	out := mcp.PeerOutput{Peers: []mcp.PeerRow{}}
	for _, p := range peers {
		out.Peers = append(out.Peers, mcp.PeerRow{ID: p.ID, Role: p.Role, Agent: p.Agent, State: string(p.State), Task: p.Task})
	}
	return out, nil
}

// checkPeerInput refuses an unknown action, or one without the fields its
// CLI subcommand takes as arguments, before anything is read.
func checkPeerInput(in mcp.PeerInput) error {
	need := func(field, value string) error {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("peer %s needs %s", in.Action, field)
		}
		return nil
	}
	switch in.Action {
	case "open":
		return need("role", in.Role)
	case "send":
		if in.Task == nil {
			return fmt.Errorf("peer send needs id and task; the task is the object delegate takes")
		}
		return need("id", in.ID)
	case "answer":
		return errors.Join(need("id", in.ID), need("option", in.Option))
	case "wait", "status", "close":
		return need("id", in.ID)
	case "list":
		return nil
	}
	return fmt.Errorf("unknown peer action %q; use open, send, wait, answer, status, close or list", in.Action)
}

// mcpPeerTimeout is a wait's timeout: timeout_minutes, or 10 when it is not
// set, and never more than mcp.MaxWaitMinutes.
func mcpPeerTimeout(minutes int) time.Duration {
	if minutes <= 0 || minutes > mcp.MaxWaitMinutes {
		minutes = mcp.MaxWaitMinutes
	}
	return time.Duration(minutes) * mcpPeerWaitUnit
}

// nextTaskName claims the first free <id>-send-N folder, so every sent task
// keeps its own immutable file and concurrent sends never share one.
func nextTaskName(work, id string) (string, error) {
	tasks := filepath.Join(work, ".duolog", "tasks")
	if err := os.MkdirAll(tasks, 0o700); err != nil {
		return "", err
	}
	for n := 1; ; n++ {
		name := fmt.Sprintf("%s-send-%d", id, n)
		err := os.Mkdir(filepath.Join(tasks, name), 0o700)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
}

// mcpPeerSend writes the task file under the peer's own task folder and sends
// its path, as `duolog peer send <id> <path>` does.
func mcpPeerSend(ctx context.Context, in mcp.PeerInput) (mcp.PeerOutput, error) {
	work, err := peer.WorkRoot(ctx, in.ID)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	ctx = peer.WithWorkDir(ctx, work)
	name, err := nextTaskName(work, in.ID)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	rel, err := writeTaskFile(work, name, *in.Task)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	// The report keeps its stable peer path while each sent task is immutable.
	if err := os.MkdirAll(filepath.Join(work, ".duolog", "tasks", in.ID), 0700); err != nil {
		return mcp.PeerOutput{}, err
	}
	out := mcp.PeerOutput{ID: in.ID, TaskPath: rel}
	err = peer.Send(ctx, in.ID, rel)
	var here *peer.RunHereError
	if errors.As(err, &here) {
		out.Line, out.RunHere = here.Line, runHereField(here.Line)
		return out, nil
	}
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	out.Line = in.ID + " sent " + rel
	return out, nil
}

// mcpPeerWait is `duolog peer wait --timeout <timeout>`, plus the open asks
// to this lead and the start of the peer's report.
func mcpPeerWait(ctx context.Context, id string, timeout time.Duration) (mcp.PeerOutput, error) {
	e, err := peer.Wait(ctx, id)
	if errors.Is(err, context.DeadlineExceeded) && e.ID != "" {
		// Report where the session stands; the pane stays open.
		e.Note, err = fmt.Sprintf("still %s after waiting %s", e.State, timeout), nil
	}
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	out := entryOutput(e)
	root, err := projectRoot()
	if err != nil {
		return out, err
	}
	if out.OpenAsks, err = leadAsks(root); err != nil {
		return out, err
	}
	if e.Report != "" {
		path := e.Report
		if !filepath.IsAbs(path) {
			path = filepath.Join(workRoot(root), filepath.FromSlash(path))
		}
		if out.Report, err = reportHead(path); err != nil {
			return out, err
		}
		out.ReportPath = e.Report
	}
	return out, nil
}

func mcpPeerStatus(ctx context.Context, id string) (mcp.PeerOutput, error) {
	st, err := peer.Status(ctx, id)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	return mcp.PeerOutput{ID: id, State: string(st.State), Line: st.Line}, nil
}

// entryOutput is what open and wait return: the CLI's line, and a run here
// note as a field.
func entryOutput(e peer.Entry) mcp.PeerOutput {
	return mcp.PeerOutput{ID: e.ID, State: string(e.State), Question: e.Question, Line: entryLine(e), RunHere: runHereField(e.Note)}
}

// runHereField is a run here line as data, or nil for any other line.
func runHereField(line string) *mcp.RunHere {
	p, ok := delegate.ParsePlacement(line)
	if !ok {
		return nil
	}
	return &mcp.RunHere{Mode: p.Where, Model: p.Model, Effort: p.Effort, Task: p.Task}
}

// leadAsks are the open permission asks to the lead this process runs under,
// with the numbers `duolog allow` and `deny` take.
func leadAsks(root string) ([]mcp.Ask, error) {
	s, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		return nil, err
	}
	lead, err := reg.ResolveLead("")
	if err != nil {
		return nil, err
	}
	msgs, err := gate.OpenAsks(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var asks []mcp.Ask
	for _, m := range msgs {
		if m.To == session.Address(lead) {
			asks = append(asks, mcp.Ask{N: m.N, From: m.From, Summary: m.S, PersonOnly: m.P})
		}
	}
	return asks, nil
}

// reportHead is the start of a report file: its first mcpReportLines lines,
// and no more than mcpReportBytes.
func reportHead(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, mcpReportBytes))
	if err != nil {
		return "", err
	}
	end := 0
	for range mcpReportLines {
		i := bytes.IndexByte(data[end:], '\n')
		if i < 0 {
			return string(data), nil
		}
		end += i + 1
	}
	return string(data[:end]), nil
}
