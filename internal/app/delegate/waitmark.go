package delegate

import "github.com/lorem-dev/duolog/internal/adapter/session"

// waitMark returns what marks the lead as waiting on the run with token, with
// the PID of the process that waits, and clears the mark with PID 0. While
// the mark stands, the gate denies a worker's ask at once: the lead cannot
// answer during a foreground wait. A detached run, which the MCP server
// started as a child of its own, marks nothing: the server marks the lead
// itself only while a call blocks on the result, so a call with wait false
// leaves the lead free and a worker's ask reaches it.
func waitMark(reg *session.Registry, lead, token string, detached bool) func(pid int) error {
	return func(pid int) error {
		if detached {
			return nil
		}
		return reg.SetWaiting(lead, token, pid)
	}
}
