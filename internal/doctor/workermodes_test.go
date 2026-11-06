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

// Claude review M1: an entry on a read-only role never applies above plan; a
// family entry only lowers. Claude review I3: extras alone are a
// workspace-write request, as delegate applies them.
func TestDoctorSaysWhenAnEntryNeverApplies(t *testing.T) {
	cfg := &config.Config{
		Roles: map[string]config.Role{"review_final": {Sandbox: "read-only"}, "implement": {Sandbox: "workspace-write"}},
		WorkerModes: config.WorkerModes{
			Roles:    map[string]config.WorkerMode{"review_final": {Claude: "bypassPermissions", Codex: "read-only"}, "implement": {Network: true}},
			Families: map[string]config.WorkerMode{"haiku": {Claude: "bypassPermissions"}},
		},
	}
	old := gateBinary
	gateBinary = func(string) (string, error) { return "/bin/duolog", nil }
	t.Cleanup(func() { gateBinary = old })
	got := workerModeFindings(cfg, t.TempDir())
	var lines []string
	for _, f := range got {
		lines = append(lines, f.Level+": "+f.Message)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"info: worker_modes: role review_final asks claude bypassPermissions; it never applies: role review_final is read-only, so its workers start in plan",
		"info: worker_modes: role review_final asks codex read-only; it applies always",
		"info: worker_modes: role implement asks codex workspace-write with network; it applies when the lead is in edits or full",
		"info: worker_modes: family haiku asks claude bypassPermissions; a family entry only lowers: it lowers a role entry above it, and alone it changes nothing",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("lacks %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "warning: worker_modes: family") || strings.Contains(joined, "warning: worker_modes: role review_final") {
		t.Errorf("warns about an entry that cannot loosen:\n%s", joined)
	}
}

func TestDoctorReportsIgnoredWorkerModes(t *testing.T) {
	cfg := &config.Config{WorkerModes: config.WorkerModes{Ignored: []string{"worker_modes.roles.implement.claude bypassPermissions in .duolog/config.yaml is ignored: x"}}}
	got := workerModeFindings(cfg, t.TempDir())
	if len(got) != 1 || got[0].Level != "warning" || !strings.HasPrefix(got[0].Message, "worker_modes.roles.implement.claude bypassPermissions") {
		t.Fatal(got)
	}
}
