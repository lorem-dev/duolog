// Package run holds the types of one worker launch: the spec runner.Run
// starts, the MCP servers bridged into it, its token usage and its error
// codes. It starts nothing; internal/runner runs a Spec.
package run

import (
	"errors"
	"time"

	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// ErrTurnCap is Run's error for a worker that reached Spec.MaxTurns.
var ErrTurnCap = errors.New("worker reached its turn cap")

// Spec is one worker launch. Process is the built worker process of the
// route's agent: its environment, its argv prefix and its settings, the same
// process its login check and quota read used. There is no other environment
// and no fallback to duolog's: a launch without a Process is refused, and
// bridged MCP env must already be in it. Started, when set, receives the
// child's process ID once it started and before Run waits for it; an error
// stops the child.
type Spec struct {
	Route                          coreroute.Route
	Mode                           mode.Mode
	Prompt, TaskDir, Cwd, ThreadID string
	Process                        *launch.Process
	Started                        func(pid int) error
	Stdin                          []byte
	Persistent                     bool
	// KeepMCP keeps the agent's MCP servers for a task that needs one.
	KeepMCP bool
	// KeepSkills keeps the skill list of a minimal worker whose task needs a skill.
	KeepSkills bool
	Timeout    time.Duration
	// MaxTurns caps a Claude worker's turns with --max-turns; 0 sets none.
	// Codex has no such flag and ignores it.
	MaxTurns int
	// SkillTest asks for raw situation answers without tools or a worker schema.
	SkillTest bool
	// MCP holds servers bridged from the lead's agent for this run, or nil.
	MCP *MCP
	// ProtectedRoots are the telemetry and balance roots. The caller builds
	// them into Process, whose Claude settings deny them to an enabled sandbox.
	ProtectedRoots []string
	// Native is the resolved native permission name; empty means mode.Args.
	// It is used only when it is known, not above Mode, and fits the sandbox.
	Native string
	// Network and WritableRoots are Codex extras for a workspace-write
	// worker at level edits or full. Claude ignores them.
	Network       bool
	WritableRoots []string
}

// MCP is what a worker needs to start servers bridged from the lead's agent.
// The config parts refer to values by variable name only; the values are in
// Env, which the caller builds into the worker's Process and nothing else.
type MCP struct {
	// Names are the bridged servers.
	Names []string
	// CodexArgs are -c overrides for a Codex worker.
	CodexArgs []string
	// ClaudeConfig is the --mcp-config JSON for a Claude worker. Run writes it to
	// a 0600 file in the task folder and removes the file after the run.
	ClaudeConfig []byte
	// AllowedTools and DisallowedTools are Claude tool names, such as
	// mcp__tracker__get_issue.
	AllowedTools, DisallowedTools []string
	// Env holds NAME=value pairs for the worker process.
	Env []string
	// ConfigPath is where Run wrote ClaudeConfig. It is set by runner.Run;
	// callers leave it empty.
	ConfigPath string
}

// Usage is total input with its cache reads and writes, and output.
type Usage = usage.Tokens

// Error codes say why a run failed. Run sets one where the run failed: the
// process, the timeout, the logs it classified, the answer it parsed; never
// from an error's text. The gate code is the caller's, which sees the gate's
// decisions.
const (
	CodeNone    = "none"
	CodeQuota   = "quota"
	CodeAuth    = "auth"
	CodeTimeout = "timeout"
	CodeExit    = "exit"
	CodeParse   = "parse"
	CodeGate    = "gate"
)
