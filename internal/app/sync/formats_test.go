package agentsync

import "github.com/lorem-dev/duolog/internal/app/sync/synctest"

// realFormats are the formats of the two real agents. The tests of this package
// check the files that sync writes, so they run on them; synctest does not
// import this package, which keeps the tests free of an import cycle.
func realFormats() Formats { return synctest.Real() }

// The functions below convert one agent file, in the way a run of sync does.
// They return the content of the agent: the names kept for the lock are not
// part of the file.

func parseClaudeAgent(path string, src []byte, o AgentOpts) (Agent, error) {
	o.To = "codex"
	a, err := realFormats()["claude"].ParseAgent(path, src, o)
	return a.Content(), err
}

func parseCodexAgent(path string, src []byte, o AgentOpts) (Agent, error) {
	o.To = "claude"
	a, err := realFormats()["codex"].ParseAgent(path, src, o)
	return a.Content(), err
}

func renderCodexAgent(a Agent) ([]byte, error) { return realFormats()["codex"].RenderAgent(a) }

func renderClaudeAgent(a Agent) ([]byte, error) { return realFormats()["claude"].RenderAgent(a) }
