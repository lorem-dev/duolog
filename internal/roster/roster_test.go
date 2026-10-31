package roster

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/session"
)

func setup(t *testing.T) (*config.Config, *session.Registry) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	return cfg, reg
}
func TestRosterLineUnderBudget(t *testing.T) {
	cfg, reg := setup(t)
	line := Line(cfg, "lead", reg)
	if float64(len(strings.Fields(line)))*1.5 > 80 {
		t.Fatal(line)
	}
	for _, want := range []string{"plan=opus", "tests=terra", "mechanical=luna"} {
		if !strings.Contains(line, want) {
			t.Fatalf("%q missing %q", line, want)
		}
	}
	if strings.Contains(line, "gpt-") || strings.Contains(line, "claude-") {
		t.Fatal(line)
	}
}
func TestRosterMarksUnavailable(t *testing.T) {
	cfg, reg := setup(t)
	if err := reg.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if line := Line(cfg, "", reg); !strings.Contains(line, "codex=unavailable") {
		t.Fatal(line)
	}
}
func TestRosterDoesNotRunAgents(t *testing.T) {
	cfg, reg := setup(t)
	if Line(cfg, "", reg) == "" {
		t.Fatal("empty roster")
	}
	if _, err := os.Stat(reg.Dir); err != nil {
		t.Fatal(err)
	}
}
