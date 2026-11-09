package claude

import (
	"errors"
	"path/filepath"
	"strconv"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// Lead finds and reads a Claude Code lead session.
type Lead struct{}

// Session reads the session id that Claude Code exports to its shell.
func (Lead) Session(environ []string) (string, bool) {
	id := trusthome.Getenv(environ, Facts.SessionEnv[0])
	return id, id != ""
}

// Interactive reports whether the session is Claude Code in a terminal.
func (Lead) Interactive(environ []string, _ string) bool {
	return trusthome.Getenv(environ, "CLAUDE_CODE_ENTRYPOINT") == "cli"
}

// RefuseShell refuses a shell that Claude Code in a terminal does not run,
// from its environment alone.
func (l Lead) RefuseShell(environ []string) error {
	if !l.Interactive(environ, "") {
		return errors.New("only an interactive Claude Code session in a terminal can be the lead")
	}
	return nil
}

// RefuseTranscript refuses nothing: the shell check decides for Claude.
func (Lead) RefuseTranscript(string, string) error { return nil }

// Transcript finds projects/<folder>/<id>.jsonl under Claude's home, the
// newest one when several folders hold the id.
func (Lead) Transcript(home, id string) (string, bool) {
	if !session.ValidSessionID(id) {
		return "", false
	}
	matches, _ := filepath.Glob(filepath.Join(trusthome.Claude(home), "projects", "*", id+".jsonl"))
	path := session.Newest(matches)
	return path, path != ""
}

// Name is the display name Claude keeps for the session of process pid, the
// name field of its own record, <claude home>/sessions/<pid>.json. It is empty
// when there is no such record, the field is missing or the file cannot be
// read: the caller falls back to the session id.
func (Lead) Name(environ []string, pid int) string {
	if pid <= 0 {
		return ""
	}
	home, err := trusthome.HomeIn(environ)
	if err != nil && trusthome.Getenv(environ, "CLAUDE_CONFIG_DIR") == "" {
		return ""
	}
	var record struct {
		Name string `json:"name"`
	}
	if !kit.ReadRecordJSON(filepath.Join(trusthome.ClaudeIn(home, environ), "sessions", strconv.Itoa(pid)+".json"), &record) {
		return ""
	}
	return record.Name
}

// State reads the model and effort of the last reply and the permission mode
// of the last prompt.
func (Lead) State(transcript string) (corelead.LeadState, error) {
	state, err := session.ReadLeadState(Facts.Name, transcript)
	if err != nil {
		return state, err
	}
	state.Mode = session.ClaudeMode(transcript)
	return state, nil
}
