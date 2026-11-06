package lead_test

import (
	"context"
	"os"
	"path/filepath"
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
	fake := &leadtest.Fake{ID: "lead-1", IsInteract: false}
	_, err := lead.Register(context.Background(), deps(t, fake), lead.RegisterRequest{Root: t.TempDir()})
	const want = "only an interactive Claude Code session in a terminal can be the lead"
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
