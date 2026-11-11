// Package peer opens and drives a second agent session in a Rex pane. The lead
// answers unresolved questions; configured safe answers and command gate
// decisions settle routine prompts without involving the lead.
package peer

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	corescreen "github.com/lorem-dev/duolog/internal/core/screen"
)

// Thread marks the registry entries of live panes, apart from stored threads.
const peerThread = "duolog-peer"

// ErrStuck means the task text is still in the input line of an idle session.
var ErrStuck = errors.New("task text is stuck in the input line")

// Entry is what Open and Wait report. Question is set for a question, and Note
// is one line of context: a timeout, a run here line, or a closed session.
type Entry struct {
	ID       string
	State    State
	Question string
	Note     string
	// Report is the path of the peer's report file, set only when it exists.
	Report string
	// Excerpt is the start of that report: a few lines, cut at a size limit.
	Excerpt string
}

// Report is what Status returns. Go cannot have a type and a function both
// named Status, so the function keeps the name that callers use.
type Report struct {
	State        State
	Task         string
	ChangedFiles int
	Line         string
}

// Options is what the commands cannot take from the environment.
type Options struct {
	// Deps is what peer reaches outside the package; the wiring package builds
	// the real one.
	Deps   Deps
	Root   string
	Config *config.Config
	Lead   string
	// LeadHints are the session IDs that ResolveLead tries after Lead, as an
	// MCP tool call names its thread; nil for a CLI command.
	LeadHints        []string
	AllowManualModel bool
	// ProtectedRoots are the telemetry and balance roots the command resolved;
	// they reach a Claude peer's launch settings.
	ProtectedRoots []string
	// Observe receives every open, send and close, failed ones included.
	Observe OperationObserver
	// ObserveAttempt receives each task a peer was sent, once it has ended, as
	// a run placed on the peer.
	ObserveAttempt delegate.Observer
	// WorkDir, when set, is the folder whose checkout a peer works in, as if
	// the command ran there; the queue opens each item in its own. Empty is
	// the command's working folder.
	WorkDir string
	// FreeOnly admits a new pane on the free variant only, never the paid
	// one, even where paid runs are allowed: the queue's runs, which nobody
	// starts by hand, set it.
	FreeOnly bool
	// NewOnly opens a new pane even where the role has a live one in the
	// checkout, so that the free-variant rule always applies and a queued
	// task never lands in another task's conversation. The queue's runs set it
	// with FreeOnly.
	NewOnly bool
}

// Operation is one open, send or close as the library ran it: its agent, how
// it ended and how long it took. It carries no screen, answer, peer id or path.
// Run is the duolog run id of the peer's launch, the pane's DUOLOG_RUN; it is
// empty when the operation failed before a peer was known.
// Outcome is done, needs (a question), lead (run here), interrupted or failed.
type Operation struct {
	Op, Agent, Run, Outcome string
	Duration                time.Duration
}

// OperationObserver cannot change an operation: it returns nothing, and the
// library neither waits for nor reads what it does.
type OperationObserver func(Operation)

// observeOperation reports an operation that has ended. Agent is empty when
// it failed before the agent was known, and so is run before the peer's launch
// drew one.
func observeOperation(ctx context.Context, op, agent, run string, started time.Time, state State, err error) {
	opts, _ := ctx.Value(optionsKey{}).(Options)
	if opts.Observe == nil {
		return
	}
	opts.Observe(Operation{Op: op, Agent: agent, Run: run, Outcome: corescreen.OperationOutcome(state, err), Duration: time.Since(started)})
}

type optionsKey struct{}

func WithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

// WithWorkDir selects a checkout for this operation without changing the
// process's working folder or the other peer options.
func WithWorkDir(ctx context.Context, dir string) context.Context {
	opts, _ := ctx.Value(optionsKey{}).(Options)
	opts.WorkDir = dir
	return WithOptions(ctx, opts)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// interruptedNote tells the lead that the peer stopped, for example after a
// denied permission prompt, and waits for a follow-up.
func interruptedNote(entry session.Entry) string {
	note := "it stopped and waits for what to do instead"
	if entry.Task != "" {
		note += "; task " + entry.Task + " is unfinished"
	}
	return note + "; send a follow-up with duolog peer send " + entry.ID + " <task>"
}

// WindowsNotice explains the supported alternative without invoking Rex.
const WindowsNotice = "Live sessions are not available on Windows; use duolog delegate."

// sameWorkRoot handles filesystem aliases without reusing legacy peers whose
// working directory was never recorded.
func sameWorkRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	left, le := os.Stat(a)
	right, re := os.Stat(b)
	return le == nil && re == nil && os.SameFile(left, right)
}
