// Package leadtest holds the fake Lead of the app tests and the contract that
// every Lead must meet.
package leadtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/lead"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
)

// Fake is a Lead that records its calls and returns scripted results. A field
// left empty falls back to a small file format of its own: the session id in
// FAKE_SESSION_ID, an interactive session when FAKE_INTERACTIVE=1, and the
// transcript at <home>/<id>.jsonl holding a JSON LeadState.
type Fake struct {
	ID          string // the session id Session reads
	IsInteract  bool   // Interactive is true
	Path        string // the transcript Transcript finds
	LeadState   corelead.LeadState
	StateErr    error
	StateCalls  []string // the transcripts State was asked for
	InteractArg []string // the transcripts Interactive was asked about
}

// Lead is the port under test, named here so that an adapter's test need not
// import the app package.
type Lead = lead.Lead

var _ Lead = (*Fake)(nil)

// Session returns the scripted id, or reads FAKE_SESSION_ID.
func (f *Fake) Session(environ []string) (string, bool) {
	id := f.ID
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "FAKE_SESSION_ID="); ok && f.ID == "" {
			id = v
		}
	}
	return id, id != ""
}

// Interactive returns the scripted answer, or true for FAKE_INTERACTIVE=1.
func (f *Fake) Interactive(environ []string, transcript string) bool {
	f.InteractArg = append(f.InteractArg, transcript)
	return f.IsInteract || slices.Contains(environ, "FAKE_INTERACTIVE=1")
}

// Transcript returns the scripted path, or <home>/<id>.jsonl when it exists.
func (f *Fake) Transcript(home, id string) (string, bool) {
	if f.Path != "" {
		return f.Path, true
	}
	if id == "" || strings.ContainsAny(id, `/\.`) {
		return "", false
	}
	path := filepath.Join(home, id+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return "", false
	}
	return path, true
}

// State returns the scripted state, or reads the JSON of the transcript.
func (f *Fake) State(transcript string) (corelead.LeadState, error) {
	f.StateCalls = append(f.StateCalls, transcript)
	if f.StateErr != nil || f.LeadState != (corelead.LeadState{}) {
		return f.LeadState, f.StateErr
	}
	var state corelead.LeadState
	if data, err := os.ReadFile(transcript); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state, nil
}

// Case describes what a Lead under test reads in its own files: the variable
// that carries the session id, an environment of an interactive session, a
// transcript to write under a home folder and what the Lead must read from it.
type Case struct {
	// IDEnv is the variable that carries the session id.
	IDEnv string
	// InteractiveEnv is an environment in which the session is interactive
	// given the transcript the Lead finds.
	InteractiveEnv []string
	// Write places a transcript for id under home and returns its path.
	Write func(t *testing.T, home, id string) string
	// Want is what State must read from the written transcript.
	Want corelead.LeadState
}

// LeadContract runs the cases that every Lead meets: a session id read from
// the agent's own variable, a non-interactive environment gives false, a
// missing transcript gives ("", false), and a transcript gives its model and
// mode.
func LeadContract(t *testing.T, newLead func(t *testing.T) Lead, c Case) {
	t.Helper()
	t.Run("session id", func(t *testing.T) { sessionID(t, newLead(t), c) })
	t.Run("missing transcript", func(t *testing.T) { missingTranscript(t, newLead(t)) })
	t.Run("interactive", func(t *testing.T) { interactive(t, newLead(t), c) })
	t.Run("transcript and state", func(t *testing.T) { transcriptAndState(t, newLead(t), c) })
}

const contractID = "contract-session-1"

func sessionID(t *testing.T, l Lead, c Case) {
	t.Helper()
	if got, ok := l.Session([]string{"PATH=/bin", c.IDEnv + "=" + contractID}); !ok || got != contractID {
		t.Errorf("Session = %q, %v; want %q, true", got, ok, contractID)
	}
	if got, ok := l.Session([]string{"PATH=/bin"}); ok || got != "" {
		t.Errorf("Session without the variable = %q, %v; want none", got, ok)
	}
}

func missingTranscript(t *testing.T, l Lead) {
	t.Helper()
	if got, ok := l.Transcript(t.TempDir(), contractID); ok || got != "" {
		t.Errorf("Transcript = %q, %v; want none", got, ok)
	}
	if got, ok := l.Transcript(t.TempDir(), "../escape"); ok || got != "" {
		t.Errorf("Transcript of an invalid id = %q, %v; want none", got, ok)
	}
}

func interactive(t *testing.T, l Lead, c Case) {
	t.Helper()
	home := t.TempDir()
	path := c.Write(t, home, contractID)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if !l.Interactive(c.InteractiveEnv, path) {
		t.Error("Interactive = false for an interactive session")
	}
	if l.Interactive([]string{"PATH=/bin"}, filepath.Join(home, "none.jsonl")) {
		t.Error("Interactive = true for a bare environment and no transcript")
	}
}

func transcriptAndState(t *testing.T, l Lead, c Case) {
	t.Helper()
	home := t.TempDir()
	want := c.Write(t, home, contractID)
	got, ok := l.Transcript(home, contractID)
	if !ok || got != want {
		t.Fatalf("Transcript = %q, %v; want %q, true", got, ok, want)
	}
	state, err := l.State(got)
	if err != nil {
		t.Fatal(err)
	}
	if state.Model != c.Want.Model || state.Mode != c.Want.Mode {
		t.Errorf("State = %+v; want model %q and mode %q", state, c.Want.Model, c.Want.Mode)
	}
}
