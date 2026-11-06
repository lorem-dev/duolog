// Package lead holds the data types of leads, their observations and the
// registry entries of reusable workers and peers.
package lead

import (
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// Lead is a registered lead session.
type Lead struct {
	ID string
	config.Target
	Mode mode.Mode
	// Native is the raw native permission mode name the lead reported with
	// Mode. Empty when not observed.
	Native         string
	TranscriptPath string
	PID            int
}

// LeadObservation contains only fields actually read by a caller. Nil means
// unobserved; a pointer to an empty string records an explicitly unknown value.
// Callers capture the time before reading stdin, the registry or a transcript.
type LeadObservation struct {
	ID, Agent                     string
	Model, Effort, TranscriptPath *string
	Mode                          *mode.Mode
	// Native travels with Mode: an observation that sets Mode replaces Native
	// with *Native, or with empty when Native is nil.
	Native *string
	PID    *int
	Ended  bool
}

// Entry is one reusable worker thread or live peer session.
type Entry struct {
	ID, Lead, Agent, Role, Thread, ThreadID string
	// Block is the Rex pane of a live peer session; it is empty for threads
	// that only exist in the agent. Doctor and peer commands join on it.
	Block string
	// WorkRoot keys live peers by checkout, while state is shared across worktrees.
	WorkRoot   string
	RexSession string
	Run        string // the pane's DUOLOG_RUN, a duolog run id; never a vendor id
	PID        int
	Input      int64
	// Task is the task file a live peer was sent and has not finished. Base is
	// the working tree before that task, as path to Git status.
	Task string
	Base map[string]string
	// Mode is the lead mode the peer was started under. Asked is the last
	// question already sent to the lead, so one question makes one message.
	Mode             mode.Mode
	Asked            string
	QuestionSince    time.Time
	QuestionNotified bool
	UpdatedAt        time.Time
	Closed           bool
	// AccountSessionID is the account reservation of a peer's pane slot, in
	// the user's account state; AdmissionID is the reservation of its latest
	// admitted task, the open's own until a send takes it over; and
	// TaskGeneration is the reservation of the sent task not yet settled, ""
	// when none is. Clearing TaskGeneration claims the settlement, so a task
	// settles once however many commands see it end.
	AccountSessionID, AdmissionID, TaskGeneration string
	// StopNotified is the task, by path and generation, whose unfinished
	// stop the peer's Stop hook already told the lead of: once per task.
	StopNotified string `json:",omitempty"`
	// IdleTurn is the turn, by transcript length, whose end the peer's Stop
	// hook already told the lead of with a peer-idle message.
	IdleTurn string `json:",omitempty"`
	// ExitNotified is set once the lead was told the peer's process exited.
	ExitNotified bool `json:",omitempty"`
}

// Unfinished is the rule of duolog peer status: a peer's task is unfinished
// while the peer is open and the task it was sent is still recorded. A wait
// that sees the peer go idle after the task clears it.
func (e Entry) Unfinished() bool { return !e.Closed && e.Task != "" }

// LeadState is the most recent native transcript observation. Empty fields are
// unknown; in particular, a missing effort must not cap a worker's effort.
type LeadState struct {
	Model, Effort, ApprovalPolicy string
	Mode                          mode.Mode
}

// LeadRoute is one observation of how the lead itself was started: its
// command line and its environment, as the lead's own process tree holds
// them. Only a caller that saw the lead may make one; duolog's own
// environment never stands for the lead. It feeds reports only.
type LeadRoute struct {
	Agent   string
	Argv    []string
	Environ []string
}
