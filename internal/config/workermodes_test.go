package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
)

func userConfigPath(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "duolog", "config.yaml")
}

func TestWorkerModesAbsentByDefault(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil || len(cfg.WorkerModes.Roles) != 0 || len(cfg.WorkerModes.Families) != 0 || len(cfg.WorkerModes.Ignored) != 0 {
		t.Fatalf("%+v %v", cfg.WorkerModes, err)
	}
	if r := cfg.WorkerModes.Request("implement", "sol", "codex"); r != (mode.Request{}) {
		t.Fatal(r)
	}
}

func TestWorkerModesUserLayer(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "worker_modes:\n  roles:\n    implement: {claude: auto, codex: workspace-write, network: true, writable_roots: [\"~/go/pkg/mod\"]}\n  families:\n    luna: {codex: read-only}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.WorkerModes
	if r := w.Request("implement", "sol", "claude"); r != (mode.Request{Native: "auto", Source: "role implement"}) {
		t.Fatal(r)
	}
	if r := w.Request("implement", "luna", "codex"); r != (mode.Request{Native: "read-only", Source: "role implement, family luna"}) {
		t.Fatal(r)
	}
	home, _ := os.UserHomeDir()
	if net, roots := w.Extras("implement"); !net || !slices.Equal(roots, []string{filepath.Join(home, "go", "pkg", "mod")}) {
		t.Fatal(net, roots)
	}
}

func TestWorkerModesUnknownNameIsLoadError(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"roles: {implement: {claude: dontAsk}}", `worker_modes.roles.implement.claude: unknown native mode "dontAsk"`},
		{"roles: {implement: {claude: Auto}}", `worker_modes.roles.implement.claude: unknown native mode "Auto"`},
		{"roles: {implement: {codex: danger-full-access}}", "worker_modes.roles.implement.codex: danger-full-access is not offered to workers; use network and writable_roots"},
		{"families: {luna: {codex: never}}", `worker_modes.families.luna.codex: unknown native mode "never"`},
		{"roles: {nosuchrole: {claude: plan}}", "worker_modes.roles.nosuchrole: no such role"},
	} {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "worker_modes:\n  "+tc.yaml+"\n")
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.yaml, err)
		}
	}
}

func TestWorkerModesRolesOnlyExtras(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"families: {luna: {codex: workspace-write, network: true}}", "worker_modes.families.luna: network and writable_roots belong to a role"},
		{"roles: {review_intermediate: {codex: workspace-write, network: true}}", "worker_modes.roles.review_intermediate: network and writable_roots need a workspace-write role"},
		{"roles: {implement: {codex: read-only, writable_roots: [\"/tmp/x\"]}}", "worker_modes.roles.implement: network and writable_roots need codex workspace-write"},
	} {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "worker_modes:\n  "+tc.yaml+"\n")
		if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.yaml, err)
		}
	}
}

func TestWorkerModesRefusedRoots(t *testing.T) {
	for _, root := range []string{"/", "~", "~/Library", "~/.ssh/keys", "~/.codex", "~/.claude", "relative/dir", "USERDIR"} {
		dir := isolatedRoot(t)
		if root == "USERDIR" {
			userDir, _ := os.UserConfigDir()
			root = filepath.Join(userDir, "duolog")
		}
		writeConfig(t, userConfigPath(t), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {codex: workspace-write, writable_roots: [%q]}\n", root))
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "worker_modes.roles.implement.writable_roots[0]") {
			t.Errorf("%s: %v", root, err)
		}
	}
}

func TestWorkerModesProjectOnlyLowers(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "worker_modes:\n  roles:\n    implement: {claude: auto, codex: workspace-write, writable_roots: [\"/tmp/a\", \"/tmp/b\"]}\n  families:\n    luna: {codex: read-only}\n")
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "worker_modes:\n  roles:\n    implement: {claude: bypassPermissions, network: true, writable_roots: [\"/tmp/a\", \"/tmp/c\"]}\n    tests: {claude: auto, codex: workspace-write}\n    docs: {claude: acceptEdits}\n  families:\n    luna: {codex: workspace-write}\n")
	writeConfig(t, filepath.Join(root, ".duolog/config.local.yaml"), "worker_modes:\n  roles:\n    debug: {claude: bypassPermissions}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.WorkerModes
	impl := w.Roles["implement"]
	if impl.Claude != "auto" || impl.Network || !slices.Equal(impl.WritableRoots, []string{"/tmp/a"}) {
		t.Fatalf("implement %+v", impl)
	}
	if w.Roles["tests"].Claude != "" || w.Roles["tests"].Codex != "workspace-write" || w.Roles["docs"].Claude != "acceptEdits" || w.Roles["debug"].Claude != "" || w.Families["luna"].Codex != "read-only" {
		t.Fatalf("%+v", w)
	}
	joined := strings.Join(w.Ignored, "\n")
	for _, want := range []string{
		"worker_modes.roles.implement.claude bypassPermissions in .duolog/config.yaml is ignored: a project config may only lower worker modes; auto applies",
		"worker_modes.roles.implement.network true in .duolog/config.yaml is ignored",
		"worker_modes.roles.implement.writable_roots /tmp/c in .duolog/config.yaml is ignored",
		"worker_modes.roles.tests.claude auto in .duolog/config.yaml is ignored: a project config may only lower worker modes; duolog's default applies",
		"worker_modes.families.luna.codex workspace-write in .duolog/config.yaml is ignored",
		"worker_modes.roles.debug.claude bypassPermissions in .duolog/config.local.yaml is ignored",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Ignored lacks %q:\n%s", want, joined)
		}
	}
	// A project layer may lower.
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "worker_modes:\n  roles:\n    implement: {claude: plan}\n")
	if cfg, err = Load(root); err != nil || cfg.WorkerModes.Roles["implement"].Claude != "plan" {
		t.Fatalf("%+v %v", cfg.WorkerModes, err)
	}
}

func TestFamily(t *testing.T) {
	cfg, _ := Load(isolatedRoot(t))
	for model, want := range map[string]string{"sol": "sol", "luna": "luna", "sonnet": "sonnet", "gpt-6.1-sol": "sol", "claude-haiku-5-5": "haiku", "other": "other"} {
		if got := Family(cfg, model); got != want {
			t.Errorf("Family(%q)=%q want %q", model, got, want)
		}
	}
}
