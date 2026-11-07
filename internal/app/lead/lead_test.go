package lead_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/lead"
	"github.com/lorem-dev/duolog/internal/app/lead/leadtest"
	"github.com/lorem-dev/duolog/internal/core/agent"
	corelead "github.com/lorem-dev/duolog/internal/core/lead"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

func deps(t *testing.T, fake *leadtest.Fake) lead.Deps {
	t.Helper()
	return lead.Deps{
		Agents:   map[string]lead.Lead{"fake": fake},
		Facts:    []agent.Descriptor{{Name: "fake", Product: "Claude Code", SessionEnv: []string{"FAKE_SESSION_ID"}}},
		Registry: session.Open,
		PID:      os.Getpid,
		Home:     func() (string, error) { return t.TempDir(), nil },
		Now:      time.Now,
	}
}

func TestLeadRegisterRefusesNonInteractive(t *testing.T) {
	const want = "only an interactive Claude Code session in a terminal can be the lead"
	fake := &leadtest.Fake{ID: "lead-1", ShellErr: errors.New(want)}
	_, err := lead.Register(context.Background(), deps(t, fake), lead.RegisterRequest{Root: t.TempDir()})
	if err == nil || err.Error() != want {
		t.Fatalf("Register = %v, want %q", err, want)
	}
}

// The shell check comes before the process lookup, as develop had it: a
// non-interactive shell with no agent process above it is told it is not
// interactive. The transcript check comes after the transcript is looked up.
func TestLeadRegisterChecksTheShellBeforeTheProcess(t *testing.T) {
	const shell = "only an interactive Claude Code session in a terminal can be the lead"
	fake := &leadtest.Fake{ID: "lead-1", ShellErr: errors.New(shell)}
	d := deps(t, fake)
	d.PID = func() int { return 0 }
	if _, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: t.TempDir()}); err == nil || err.Error() != shell {
		t.Fatalf("Register = %v, want %q", err, shell)
	}

	root := t.TempDir()
	path := filepath.Join(root, "t.jsonl")
	const transcript = "cannot find the transcript of interactive thread lead-1"
	fake = &leadtest.Fake{ID: "lead-1", IsInteract: true, Path: path, TranscriptErr: errors.New(transcript)}
	d = deps(t, fake)
	if _, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: root}); err == nil || err.Error() != transcript {
		t.Fatalf("Register = %v, want %q", err, transcript)
	}
	if want := []string{"shell", "transcript " + path}; !slices.Equal(fake.Checks, want) {
		t.Fatalf("checks %q, want %q", fake.Checks, want)
	}
	if len(fake.StateCalls) != 0 {
		t.Fatalf("a refused transcript was read: %q", fake.StateCalls)
	}
}

// The agent is found by a marker variable as os.Getenv finds it: without
// case on Windows, with case elsewhere.
func TestLeadRegisterFindsTheMarkerAsGetenvDoes(t *testing.T) {
	d := deps(t, &leadtest.Fake{})
	d.Facts[0].Markers = []string{"FAKECODE"}
	_, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: t.TempDir(), Environ: []string{"FakeCode=1"}})
	want := "run duolog lead register inside a Claude Code session"
	if runtime.GOOS == "windows" {
		want = "this fake session does not set FAKE_SESSION_ID, so duolog cannot tell which session to register; start the session in this project's folder so that its hooks register it"
	}
	if err == nil || err.Error() != want {
		t.Fatalf("Register = %v, want %q", err, want)
	}
}

func TestLeadRegisterCeilingIsTheObservedMode(t *testing.T) {
	root := t.TempDir()
	fake := &leadtest.Fake{ID: "lead-1", IsInteract: true, Path: filepath.Join(root, "t.jsonl"),
		LeadState: corelead.LeadState{Model: "m", Mode: mode.Edits}}
	d := deps(t, fake)
	if _, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: root, Mode: mode.Full}); err == nil ||
		!strings.Contains(err.Error(), "above the observed mode edits") {
		t.Fatalf("full above edits: %v", err)
	}
	res, err := lead.Register(context.Background(), d, lead.RegisterRequest{Root: root, Mode: mode.Edits})
	if err != nil || res.Mode != mode.Edits || res.PID != os.Getpid() {
		t.Fatalf("%+v %v", res, err)
	}
}
