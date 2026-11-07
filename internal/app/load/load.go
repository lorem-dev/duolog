// Package load builds the load line a lead reads: its live peers, the session
// limit of each agent and the 5-hour window, so the number of peers follows
// what the account allows.
package load

import (
	"fmt"
	"strings"
)

// Agent is the load of one agent. Running is the live peers of this lead and
// Idle the ones among them that finished their turn. Limit and Live are the
// numbers `duolog quota` prints as "<agent> sessions Live of Limit": Live
// counts the sessions of every lead.
type Agent struct {
	Name                       string
	Running, Idle, Limit, Live int
}

// Summary is what the line says. Idle names the idle peers of this lead, and
// Queued counts the items that wait in its ready queue.
type Summary struct {
	Agents      []Agent
	Idle        []string
	Queued      int
	Window      float64
	WindowKnown bool
}

// windowCeiling is the 5-hour use, in percent, from which the line stops
// advising more work.
const windowCeiling = 70

// Line says the summary in one line, or "" when there is nothing to say.
// While items wait in the queue and a session is free, it advises a queue run;
// with an empty queue, half of an agent's sessions free and the window known
// and under its ceiling, it advises more work.
func (s Summary) Line() string {
	var parts []string
	for _, a := range s.Agents {
		if a.Limit > 0 {
			parts = append(parts, a.part())
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if s.Queued > 0 {
		parts = append(parts, fmt.Sprintf("queue %d", s.Queued))
	}
	if s.WindowKnown {
		parts = append(parts, fmt.Sprintf("5h %g%%", s.Window))
	}
	if len(s.Idle) > 0 {
		parts = append(parts, "idle: "+strings.Join(s.Idle, ", "))
	}
	return "duolog peers: " + strings.Join(parts, "; ") + s.advice()
}

// part says one agent's peers: "claude 3 running (1 idle) of 12".
func (a Agent) part() string {
	part := fmt.Sprintf("%s %d", a.Name, a.Running)
	if a.Running > 0 {
		part += " running"
		if a.Idle > 0 {
			part += fmt.Sprintf(" (%d idle)", a.Idle)
		}
	}
	return part + fmt.Sprintf(" of %d", a.Limit)
}

// advice is the end of the line that tells the lead what to do with free
// sessions, or "". It names the agent with the most free sessions.
func (s Summary) advice() string {
	if s.Queued > 0 {
		if name, free := s.mostFree(false); name != "" {
			return fmt.Sprintf(" - %d %s sessions free: duolog peer queue run", free, name)
		}
		return ""
	}
	// A window not known may be near its end: advise no more work then.
	if !s.WindowKnown || s.Window >= windowCeiling {
		return ""
	}
	if name, free := s.mostFree(true); name != "" {
		return fmt.Sprintf(" - %d %s sessions free: start independent work or stack it on open pull request branches (duolog peer queue add)", free, name)
	}
	return ""
}

// Free is how many sessions of agent are free: its limit less its live
// sessions, none for an agent the summary has no limit for. With agent "", it
// is the most free of any agent.
func (s Summary) Free(agent string) int {
	most := 0
	for _, a := range s.Agents {
		if a.Limit > 0 && (agent == "" || a.Name == agent) {
			most = max(most, a.Limit-a.Live)
		}
	}
	return most
}

// mostFree is the limited agent with the most free sessions, at least one;
// with half, only agents with at least half of their sessions free count.
func (s Summary) mostFree(half bool) (name string, most int) {
	for _, a := range s.Agents {
		if a.Limit <= 0 {
			continue
		}
		if free := a.Limit - a.Live; free > most && (!half || free*2 >= a.Limit) {
			name, most = a.Name, free
		}
	}
	return name, most
}
