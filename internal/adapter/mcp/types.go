// Package mcp is duolog's stdio MCP server for a Claude lead. It speaks the
// protocol and nothing else: every tool calls App, which cmd/duolog
// implements with the same functions as the CLI commands.
package mcp

import "context"

// ServerName is the server's name in Claude's config, and the tool names it
// serves.
const (
	ServerName    = "duolog"
	ToolDelegate  = "delegate"
	ToolPeer      = "peer"
	ToolAnswerAsk = "answer_ask"
	ToolStatus    = "status"
)

// AllowedTools are the Claude permission entries install adds. answer_ask is
// never among them: the lead's guard or the person decides it.
var AllowedTools = []string{"mcp__duolog__delegate", "mcp__duolog__peer", "mcp__duolog__status"}

// AnswerAskTool is the Claude name of answer_ask, as the lead's guard sees it.
const AnswerAskTool = "mcp__duolog__answer_ask"

// Task is a task file as data: the five sections in their fixed order.
type Task struct {
	Goal   string   `json:"goal"`
	Read   []string `json:"read,omitempty"`
	Write  []string `json:"write,omitempty"`
	Limits []string `json:"limits,omitempty"`
	Check  []string `json:"check,omitempty"`
}

// DelegateInput is the delegate tool's input.
type DelegateInput struct {
	Action         string   `json:"action,omitempty"` // "run" (default) or "result"
	Role           string   `json:"role,omitempty"`
	Task           *Task    `json:"task,omitempty"`
	TaskPath       string   `json:"task_path,omitempty"` // for result
	Use            string   `json:"use,omitempty"`
	Needs          []string `json:"needs,omitempty"`
	Wait           bool     `json:"wait,omitempty"`
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"`
}

// RunHere is a run here answer as data. Mode is "self" or "subagent".
type RunHere struct {
	Mode   string `json:"mode"`
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	Task   string `json:"task"`
}

// DelegateOutput is the delegate tool's result.
// DelegateOutput.State is running, done, needs, run_here, failed or lost.
type DelegateOutput struct {
	State      string   `json:"state"`
	TaskPath   string   `json:"task_path"`
	Answer     string   `json:"answer,omitempty"`      // the CLI's result line
	AnswerPath string   `json:"answer_path,omitempty"` // the result record
	RunHere    *RunHere `json:"run_here,omitempty"`
	Needs      []string `json:"needs,omitempty"`
}

// PeerInput is the peer tool's input.
type PeerInput struct {
	Action         string `json:"action"` // open|send|wait|answer|status|close|list
	ID             string `json:"id,omitempty"`
	Role           string `json:"role,omitempty"`
	Task           *Task  `json:"task,omitempty"`
	Option         string `json:"option,omitempty"`
	Reason         string `json:"reason,omitempty"`
	TimeoutMinutes int    `json:"timeout_minutes,omitempty"`
}

// Ask is an open permission ask, with the number duolog allow/deny takes.
type Ask struct {
	N          int64  `json:"ask"`
	From       string `json:"from"`
	Summary    string `json:"summary"`
	PersonOnly bool   `json:"person_only,omitempty"`
}

// PeerRow is one live peer.
type PeerRow struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Agent string `json:"agent"`
	State string `json:"state"`
	Task  string `json:"task,omitempty"`
}

// PeerOutput is the peer tool's result.
type PeerOutput struct {
	ID         string    `json:"id,omitempty"`
	State      string    `json:"state,omitempty"`
	Line       string    `json:"line,omitempty"` // the CLI's line
	Question   string    `json:"question,omitempty"`
	OpenAsks   []Ask     `json:"open_asks,omitempty"`
	Report     string    `json:"report,omitempty"` // first lines of the report file
	ReportPath string    `json:"report_path,omitempty"`
	RunHere    *RunHere  `json:"run_here,omitempty"`
	TaskPath   string    `json:"task_path,omitempty"`
	Peers      []PeerRow `json:"peers,omitempty"` // list
}

// AnswerAskInput is the answer_ask tool's input.
type AnswerAskInput struct {
	Ask      int64  `json:"ask"`
	Decision string `json:"decision"` // allow or deny
	Reason   string `json:"reason,omitempty"`
}

// AnswerAskOutput is the answer_ask tool's result.
type AnswerAskOutput struct {
	Line string `json:"line"`
}

// Delegation is one detached delegation, running or finished.
type Delegation struct {
	TaskPath   string `json:"task_path"`
	Role       string `json:"role"`
	State      string `json:"state"`
	AnswerPath string `json:"answer_path"`
}

// StatusOutput is the status tool's result.
type StatusOutput struct {
	Peers       []PeerRow    `json:"peers"`
	OpenAsks    []Ask        `json:"open_asks"`
	Delegations []Delegation `json:"delegations"`
	Load        string       `json:"load,omitempty"`
	Quota       string       `json:"quota,omitempty"`
}

// App is what the tools call. An error is shown to the lead as the tool's
// isError text: it names the cause and the next step.
type App interface {
	Delegate(ctx context.Context, in DelegateInput) (DelegateOutput, error)
	Peer(ctx context.Context, in PeerInput) (PeerOutput, error)
	AnswerAsk(ctx context.Context, in AnswerAskInput) (AnswerAskOutput, error)
	Status(ctx context.Context) (StatusOutput, error)
}

// SchemaBudgetBytes caps the compact JSON of all four tool definitions.
const SchemaBudgetBytes = 2400

// MaxWaitMinutes caps timeout_minutes for delegate and peer wait.
const MaxWaitMinutes = 10
