package doctor

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
)

func TestDoctorReportsWorkerModes(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	old := gateBinary
	gateBinary = func(string) (string, error) { return "", errors.New("duolog is not on the worker PATH") }
	t.Cleanup(func() { gateBinary = old })
	cfg := &config.Config{WorkerModes: config.WorkerModes{Roles: map[string]config.WorkerMode{"implement": {Claude: "auto"}, "docs": {Claude: "plan"}}}}
	got := workerModeFindings(cfg, root)
	want := []Finding{
		{"info", "worker_modes: role docs asks claude plan; it applies always"},
		{"info", "worker_modes: role implement asks claude auto; it applies when the lead is in auto or full"},
		{"warning", "worker_modes: role implement asks claude auto, but the gate cannot be shown to run for claude here: Claude has no duolog gate here; delegate refuses such a run"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
}

func TestDoctorReportsIgnoredWorkerModes(t *testing.T) {
	cfg := &config.Config{WorkerModes: config.WorkerModes{Ignored: []string{"worker_modes.roles.implement.claude bypassPermissions in .duolog/config.yaml is ignored: x"}}}
	got := workerModeFindings(cfg, t.TempDir())
	if len(got) != 1 || got[0].Level != "warning" || !strings.HasPrefix(got[0].Message, "worker_modes.roles.implement.claude bypassPermissions") {
		t.Fatal(got)
	}
}
