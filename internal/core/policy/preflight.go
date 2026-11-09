package policy

import (
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// PreflightInput is what a worker's gate would judge a command with: the
// policy, the lead's rules, the mode and role the worker runs in, the folder
// it works in and the project root that holds the state.
type PreflightInput struct {
	Policy         config.Policy
	Lead           LeadRules
	Mode           mode.Mode
	Role           string
	Work, State    string
	ProtectedRoots []string
	// Reads are the absolute entries of the task's Read section that the
	// worker's gate gets as TaskReadsEnv; Shell is "powershell" or "windows"
	// where the worker's command runs in that shell, and "" for a POSIX one.
	Reads []string
	Shell string
}

// Blocked is a command that the gate would not allow a worker to run.
type Blocked struct {
	Command  string `json:"command"`
	Decision string `json:"decision"` // ask or deny
	Reason   string `json:"reason,omitempty"`
	Rule     string `json:"rule,omitempty"`
}

// Preflight judges each command with Evaluate, as the gate does for a worker
// of in.Role in in.Work, and returns those answered ask or deny, in order. It
// only reports: nothing is allowed or remembered.
func Preflight(in PreflightInput, commands []string) []Blocked {
	var blocked []Blocked
	for _, command := range commands {
		d := Evaluate(in.Policy, in.Lead, in.Mode, Action{
			Kind: "exec", Target: command, Role: in.Role,
			Work: in.Work, Cwd: in.Work, State: in.State, ProtectedRoots: in.ProtectedRoots,
			Reads: in.Reads, Shell: in.Shell,
		})
		if d.Kind != Allow {
			blocked = append(blocked, Blocked{Command: command, Decision: d.Kind, Reason: d.Reason, Rule: d.Rule})
		}
	}
	return blocked
}
