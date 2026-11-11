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
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/peer"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
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
	pctx, done, err := d.mcpPeerContext(ctx, in, timeout)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	defer done()
	switch in.Action {
	case "open":
		in.Worktree = ""
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

func (d *cmdDeps) mcpPeerContext(ctx context.Context, in mcp.PeerInput, timeout time.Duration) (context.Context, func(), error) {
	if isWorker() {
		return nil, nil, errors.New("workers cannot open or control peer sessions")
	}
	if in.Action == "open" && in.Worktree != "" {
		work, err := mcpPeerWorktree(in.Worktree)
		if err != nil {
			return nil, nil, err
		}
		return d.peerCtxAt(ctx, "", false, timeout, work)
	}
	if in.ID != "" {
		return d.peerCtxForID(ctx, "", false, timeout, in.ID)
	}
	return d.peerCtx(ctx, "", false, timeout)
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

// mcpWorktreeTimeout bounds the Git calls of one worktree resolution.
const mcpWorktreeTimeout = 30 * time.Second

// mcpPeerWorktree resolves the worktree of a peer open or a delegate. A plain
// name is .worktrees/<name> in the server's checkout or in a checkout of a
// duolog submodule; two such checkouts are an error naming both, and with
// none the name is a path from the server's cwd, as any other ref is. Only a
// checkout root that Git lists qualifies: one of this repository, or of a
// duolog submodule that shares this lead home. A peer so never lands in a
// subfolder, where it would edit the wrong tree.
func mcpPeerWorktree(ref string) (string, error) {
	root, err := projectRoot()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpWorktreeTimeout)
	defer cancel()
	own, subs, err := mcpCheckouts(ctx, root)
	if err != nil {
		return "", err
	}
	checkouts := slices.Concat(own, subs)
	dir, err := mcpNamedWorktree(ref, slices.Concat([]string{workRoot(root)}, subs), checkouts)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("worktree %q is not a folder", ref)
	}
	if !containsCheckout(checkouts, abs) {
		return "", fmt.Errorf("worktree %q is not a checkout of this repository or of a duolog submodule", ref)
	}
	return abs, nil
}

// mcpNamedWorktree returns the checkout a plain name means: .worktrees/<ref>
// under one of bases that is a listed checkout. With no such checkout it is
// the first such folder, which the caller then refuses, or else ref itself.
func mcpNamedWorktree(ref string, bases, checkouts []string) (string, error) {
	if ref == "." || ref == ".." || strings.ContainsAny(ref, `/\`) {
		return ref, nil
	}
	fallback, seen := ref, map[string]bool{}
	var matches []string
	for _, base := range bases {
		named := filepath.Join(base, ".worktrees", ref)
		if info, err := os.Stat(named); err != nil || !info.IsDir() {
			continue
		}
		if fallback == ref {
			fallback = named
		}
		key := checkoutKey(named)
		if !seen[key] && containsCheckout(checkouts, named) {
			seen[key] = true
			matches = append(matches, named)
		}
	}
	switch len(matches) {
	case 0:
		return fallback, nil
	case 1:
		return matches[0], nil
	}
	return "", fmt.Errorf("worktree %q names more than one checkout: %s; pass its path instead", ref, strings.Join(matches, ", "))
}

// mcpCheckouts lists the checkouts of root's repository and those of its
// duolog submodules, as Git records them.
func mcpCheckouts(ctx context.Context, root string) (own, subs []string, err error) {
	if own, err = project.Checkouts(ctx, root); err != nil {
		return nil, nil, err
	}
	if subs, err = project.DuologSubmodules(ctx, root); err != nil {
		return nil, nil, err
	}
	return own, subs, nil
}

// repositoryCheckout reports whether dir is the root of a checkout of root's
// repository or of one of its duolog submodules. Membership comes from the
// lists of root's repository and its modules, never from what dir says about
// its own .git; a failed Git call refuses.
func repositoryCheckout(root, dir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), mcpWorktreeTimeout)
	defer cancel()
	own, subs, err := mcpCheckouts(ctx, root)
	return err == nil && containsCheckout(slices.Concat(own, subs), dir)
}

func containsCheckout(checkouts []string, dir string) bool {
	key := checkoutKey(dir)
	return slices.ContainsFunc(checkouts, func(checkout string) bool { return checkoutKey(checkout) == key })
}

func checkoutKey(dir string) string {
	return pathutil.Clean(pathutil.Resolved(dir))
}

// mcpPeerList is `duolog peer list --json` as rows.
func mcpPeerList(ctx context.Context) (mcp.PeerOutput, error) {
	peers, err := peer.List(ctx)
	if err != nil {
		return mcp.PeerOutput{}, err
	}
	out := mcp.PeerOutput{Peers: []mcp.PeerRow{}}
	for _, p := range peers {
		out.Peers = append(out.Peers, mcp.PeerRow{ID: p.ID, Role: p.Role, Agent: p.Agent, State: string(p.State), Task: p.Task, WorkRoot: p.WorkRoot})
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
	if out.OpenAsks, err = leadAsks(ctx, root); err != nil {
		return out, err
	}
	if e.Report != "" {
		report, err := mcpPeerReportPath(ctx, id, e.Report, root)
		if err != nil {
			return out, err
		}
		path := report
		if !filepath.IsAbs(path) {
			path = filepath.Join(workRoot(root), filepath.FromSlash(path))
		}
		if out.Report, err = reportHead(path); err != nil {
			return out, err
		}
		out.ReportPath = report
	}
	return out, nil
}

func mcpPeerReportPath(ctx context.Context, id, report, root string) (string, error) {
	if filepath.IsAbs(report) {
		return report, nil
	}
	work, err := peer.WorkRoot(ctx, id)
	if err != nil {
		return "", err
	}
	if pathutil.Clean(pathutil.Resolved(work)) == pathutil.Clean(pathutil.Resolved(workRoot(root))) {
		return report, nil
	}
	return filepath.Join(work, filepath.FromSlash(report)), nil
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
// or that the tool call's lead hints in ctx name, with the numbers `duolog
// allow` and `deny` take.
func leadAsks(ctx context.Context, root string) ([]mcp.Ask, error) {
	dir := project.LeadHome(root)
	reg, err := session.Open(dir)
	if err != nil {
		return nil, err
	}
	lead, err := reg.ResolveLead("", hintsFrom(ctx)...)
	if err != nil {
		return nil, err
	}
	msgs, err := gate.OpenAsks(dir)
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
