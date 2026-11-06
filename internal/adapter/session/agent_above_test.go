package session

import "testing"

// chain is a fake process table: each pid's parent and program name.
type chain map[int]struct {
	next int
	name string
}

func (c chain) parent(pid int) (int, string) {
	p, ok := c[pid]
	if !ok {
		return 0, ""
	}
	return p.next, p.name
}

// Re-review 5, W-1: a delegate worker runs below its lead, so the walk from
// duolog allow reaches the lead through the worker's own Claude or Codex
// process. An agent process met before the lead's own makes the caller a
// worker or peer, which answers nothing; shells and launchers between, such
// as codex-linux-sandbox, do not count, nor does the lead's own process.
func TestAncestorLeadRefusesAnAgentBelowTheLead(t *testing.T) {
	leads := []Lead{{ID: "lead-1", PID: 104}}
	for name, tc := range map[string]struct {
		procs   chain
		found   bool
		refused bool
	}{
		"lead's own shell":     {chain{100: {101, "duolog"}, 101: {104, "zsh"}, 104: {1, "claude"}}, true, false},
		"codex lead's sandbox": {chain{100: {101, "duolog"}, 101: {102, "bash"}, 102: {104, "codex-linux-sandbox"}, 104: {1, "codex-x86_64-unknown-linux-musl"}}, true, false},
		"claude worker below":  {chain{100: {101, "duolog"}, 101: {102, "sh"}, 102: {103, "claude"}, 103: {104, "duolog"}, 104: {1, "claude"}}, false, true},
		"codex worker below":   {chain{100: {101, "duolog"}, 101: {102, "sh"}, 102: {103, "/opt/codex/bin/codex"}, 103: {104, "zsh"}, 104: {1, "claude"}}, false, true},
		"codex triple below":   {chain{100: {101, "duolog"}, 101: {102, "sh"}, 102: {104, "codex-aarch64-apple-darwin"}, 104: {1, "codex"}}, false, true},
		"no lead above":        {chain{100: {101, "duolog"}, 101: {1, "zsh"}}, false, false},
		"agent above, no lead": {chain{100: {101, "duolog"}, 101: {102, "sh"}, 102: {1, "claude"}}, false, false},
	} {
		lead, found, err := ancestorLeadFrom(100, tc.procs.parent, leads)
		if found != tc.found || (err != nil) != tc.refused {
			t.Errorf("%s: %+v found %v err %v; want found %v refused %v", name, lead, found, err, tc.found, tc.refused)
		}
	}
}

// Re-review 5, N5: AgentAbove finds the agent as AgentAncestor does, so the
// npm Codex binary, named codex-<target triple> and cut to that by macOS,
// counts as an agent above the caller, as do claude and codex by any path.
// Codex sets no environment mark, so the walk is all there is for it.
func TestAgentAboveFindsTheAgentAsAgentAncestorDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		environ []string
		procs   chain
		want    bool
	}{
		"codex target triple": {nil, chain{10: {11, "sh"}, 11: {12, "codex-aarch64-apple-darwin"}, 12: {1, "launchd"}}, true},
		"codex by path":       {nil, chain{10: {11, "/bin/zsh"}, 11: {1, "/usr/local/bin/codex"}}, true},
		"claude.exe":          {nil, chain{10: {11, `C:\Program Files\Claude\claude.exe`}, 11: {1, "explorer.exe"}}, true},
		"claude env mark":     {[]string{"CLAUDECODE=1"}, chain{10: {1, "zsh"}}, true},
		"person's terminal":   {nil, chain{10: {11, "zsh"}, 11: {12, "login"}, 12: {1, "launchd"}}, false},
		"codex in a word":     {nil, chain{10: {11, "zsh"}, 11: {1, "mycodex"}}, false},
	} {
		if got := agentAbove(tc.environ, 10, tc.procs.parent); got != tc.want {
			t.Errorf("%s: agentAbove %v, want %v", name, got, tc.want)
		}
		if tc.environ == nil {
			if got := agentAncestor(10, tc.procs.parent) != ""; got != tc.want {
				t.Errorf("%s: agentAncestor %v, want %v: the two checks disagree", name, got, tc.want)
			}
		}
	}
}
