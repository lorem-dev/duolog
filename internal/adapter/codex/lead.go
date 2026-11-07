package codex

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// Lead finds and reads a Codex lead session.
type Lead struct{}

// Session reads the thread id that Codex exports to its shell.
func (Lead) Session(environ []string) (string, bool) {
	id := trusthome.Getenv(environ, Facts.SessionEnv[0])
	return id, id != ""
}

// Interactive reports whether the rollout was started by the Codex terminal
// UI; a thread without a rollout is not an interactive lead.
func (Lead) Interactive(_ []string, transcript string) bool {
	return session.Originator(transcript) == "codex-tui"
}

// RefuseShell refuses nothing: Codex's environment does not tell an
// interactive thread, its rollout does.
func (Lead) RefuseShell([]string) error { return nil }

// RefuseTranscript refuses thread id when its rollout is missing or was not
// started by the Codex terminal UI.
func (l Lead) RefuseTranscript(id, transcript string) error {
	if !l.Interactive(nil, transcript) {
		return fmt.Errorf("cannot find the transcript of interactive Codex thread %s under CODEX_HOME/sessions; only an interactive Codex session can be the lead", id)
	}
	return nil
}

// Transcript finds sessions/.../rollout-*-<id>.jsonl under Codex's home, the
// newest one when several hold the id.
func (Lead) Transcript(home, id string) (string, bool) {
	if !session.ValidSessionID(id) {
		return "", false
	}
	var matches []string
	_ = filepath.WalkDir(filepath.Join(trusthome.Codex(home), "sessions"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-") && strings.HasSuffix(d.Name(), "-"+id+".jsonl") {
			matches = append(matches, path)
		}
		return nil
	})
	path := session.Newest(matches)
	return path, path != ""
}

// State reads the model, effort and mode of the last turn context.
func (Lead) State(transcript string) (corelead.LeadState, error) {
	return session.ReadLeadState(Facts.Name, transcript)
}
