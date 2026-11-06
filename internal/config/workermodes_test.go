package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/pathutil"
)

// userConfigPath is in paid_runs_test.go, which both features' tests share.

func TestWorkerModesAbsentByDefault(t *testing.T) {
	cfg, err := Load(isolatedRoot(t))
	if err != nil || len(cfg.WorkerModes.Roles) != 0 || len(cfg.WorkerModes.Families) != 0 || len(cfg.WorkerModes.Ignored) != 0 {
		t.Fatalf("%+v %v", cfg.WorkerModes, err)
	}
	if r := cfg.WorkerModes.Request("implement", []string{"sol"}, "codex"); r != (mode.Request{}) {
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
	if r := w.Request("implement", []string{"sol"}, "claude"); r != (mode.Request{Native: "auto", Source: "role implement"}) {
		t.Fatal(r)
	}
	if r := w.Request("implement", []string{"luna"}, "codex"); r != (mode.Request{Native: "read-only", Source: "role implement, family luna"}) {
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

// Claude review I1, Codex review 2: roots compare canonically, by case on a
// volume that ignores it and through symlinks, and the list covers the
// common credential folders.
func TestWorkerModesRefusedRootAliases(t *testing.T) {
	cases := []string{"~/.aws", "~/.aws/sso", "~/.gnupg", "~/.config/gh", "~/.docker", "~/.kube", "~/.local/share/keyrings", "LINK", "LINKBELOW",
		// Re-review m5: files, and folders whose contents the lead runs.
		"FILE", "~/.config/git", "~/.config/git/hooks", "~/Library/LaunchAgents"}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		cases = append(cases, "~/.SSH", "~/Library/keychains", "~/.Claude", "HOMEUPPER")
	}
	if runtime.GOOS == "darwin" {
		// The data volume's firmlinks: the same folders under another path.
		cases = append(cases, "FIRMLINK", "FIRMLINKHOME", "/System/Volumes/Data", "/system/volumes/DATA")
	}
	for _, root := range cases {
		dir := isolatedRoot(t)
		home, _ := os.UserHomeDir()
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		switch root {
		case "LINK", "LINKBELOW":
			link := filepath.Join(t.TempDir(), "worker-cache")
			if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
				t.Skip("no symlinks:", err)
			}
			root = link
			if root == "LINKBELOW" {
				root = filepath.Join(link, "keys")
			}
		case "HOMEUPPER":
			root = strings.ToUpper(home)
		case "FILE":
			root = filepath.Join(t.TempDir(), "netrc")
			if err := os.WriteFile(root, []byte("machine x"), 0o600); err != nil {
				t.Fatal(err)
			}
		case "FIRMLINK":
			root = filepath.Join("/System/Volumes/Data", pathutil.Resolved(home), ".ssh")
		case "FIRMLINKHOME":
			root = filepath.Join("/System/Volumes/Data", pathutil.Resolved(home))
		}
		writeConfig(t, userConfigPath(t), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {codex: workspace-write, writable_roots: [%q]}\n", root))
		if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "worker_modes.roles.implement.writable_roots[0]") {
			t.Errorf("%s: %v", root, err)
		}
	}
	// An ordinary cache folder still loads, and RefuseRoot agrees at launch.
	dir := isolatedRoot(t)
	cache := t.TempDir()
	writeConfig(t, userConfigPath(t), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {codex: workspace-write, writable_roots: [%q]}\n", cache))
	if _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if RefuseRoot(cache) != "" || RefuseRoot(filepath.Join(home, ".aws")) == "" {
		t.Fatal(RefuseRoot(cache), RefuseRoot(filepath.Join(home, ".aws")))
	}
}

// Re-review, Important 1: the worker's own home folder is protected, not
// only duolog's: a writable root inside the worker's .codex is refused when
// duolog's HOME is another folder.
func TestRefuseRootEnvProtectsTheWorkersHome(t *testing.T) {
	isolatedRoot(t)
	worker := t.TempDir()
	name := "HOME"
	if runtime.GOOS == "windows" {
		name = "USERPROFILE"
	}
	env := []string{name + "=" + worker}
	for _, sub := range []string{".codex", ".claude", ".ssh"} {
		root := filepath.Join(worker, sub, "inner")
		if RefuseRoot(root) != "" {
			t.Fatalf("%s is refused for duolog's own home too; the test needs another home", root)
		}
		if RefuseRootEnv(root, env) == "" {
			t.Errorf("%s inside the worker's home is not refused", root)
		}
	}
	if RefuseRootEnv(worker, env) == "" {
		t.Error("the worker's home folder itself is not refused")
	}
}

// Codex review 3: the Windows credential and vault folders are protected.
func TestWorkerModesWindowsCredentialFolders(t *testing.T) {
	env := map[string]string{"APPDATA": `C:\Users\study\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\study\AppData\Local`}
	got := protectedFolders("windows", `C:\Users\study`, func(k string) string { return env[k] })
	for _, want := range []string{
		filepath.Join(env["LOCALAPPDATA"], "Microsoft", "Credentials"),
		filepath.Join(env["APPDATA"], "Microsoft", "Credentials"),
		filepath.Join(env["LOCALAPPDATA"], "Microsoft", "Vault"),
		filepath.Join(env["APPDATA"], "Microsoft", "Vault"),
		filepath.Join(env["APPDATA"], "Microsoft", "Protect"),
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	isolatedRoot(t)
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	for _, root := range []string{filepath.Join(local, "Microsoft", "Credentials"), filepath.Join(local, "microsoft", "VAULT", "x")} {
		if RefuseRoot(root) == "" {
			t.Errorf("%s is not refused", root)
		}
	}
}

func TestWorkerModesProjectOnlyLowers(t *testing.T) {
	root := isolatedRoot(t)
	// Platform folders: /tmp/a is not absolute on Windows. Single quotes keep
	// a Windows path's backslashes literal in YAML.
	base := t.TempDir()
	a, b, c := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "c")
	writeConfig(t, userConfigPath(t), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {claude: auto, codex: workspace-write, writable_roots: ['%s', '%s']}\n  families:\n    luna: {codex: read-only}\n", a, b))
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {claude: bypassPermissions, network: true, writable_roots: ['%s', '%s']}\n    tests: {claude: auto, codex: workspace-write}\n    docs: {claude: acceptEdits}\n  families:\n    luna: {codex: workspace-write}\n", a, c))
	writeConfig(t, filepath.Join(root, ".duolog/config.local.yaml"), "worker_modes:\n  roles:\n    debug: {claude: bypassPermissions}\n")
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.WorkerModes
	impl := w.Roles["implement"]
	if impl.Claude != "auto" || impl.Network || !slices.Equal(impl.WritableRoots, []string{a}) {
		t.Fatalf("implement %+v", impl)
	}
	if w.Roles["tests"].Claude != "" || w.Roles["tests"].Codex != "workspace-write" || w.Roles["docs"].Claude != "acceptEdits" || w.Roles["debug"].Claude != "" || w.Families["luna"].Codex != "read-only" {
		t.Fatalf("%+v", w)
	}
	joined := strings.Join(w.Ignored, "\n")
	for _, want := range []string{
		"worker_modes.roles.implement.claude bypassPermissions in .duolog/config.yaml is ignored: a project config may only lower worker modes; auto applies",
		"worker_modes.roles.implement.network true in .duolog/config.yaml is ignored",
		"worker_modes.roles.implement.writable_roots " + c + " in .duolog/config.yaml is ignored",
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

// Codex review 1: each project layer lowers the entry as the layers before it
// left it, not as the user layer set it.
func TestWorkerModesLaterLayerCannotUndoLowering(t *testing.T) {
	root := isolatedRoot(t)
	base := t.TempDir()
	cacheA, cacheB := filepath.Join(base, "cache-a"), filepath.Join(base, "cache-b")
	writeConfig(t, userConfigPath(t), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {claude: auto, codex: workspace-write, network: true, writable_roots: ['%s', '%s']}\n", cacheA, cacheB))
	writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "worker_modes:\n  roles:\n    implement: {claude: plan, network: false, writable_roots: []}\n    tests: {claude: plan}\n  families:\n    luna: {codex: read-only}\n")
	writeConfig(t, filepath.Join(root, ".duolog/config.local.yaml"), fmt.Sprintf("worker_modes:\n  roles:\n    implement: {claude: auto, network: true, writable_roots: ['%s']}\n    tests: {claude: acceptEdits}\n  families:\n    luna: {codex: workspace-write}\n", cacheB))
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	w := cfg.WorkerModes
	if impl := w.Roles["implement"]; impl.Claude != "plan" || impl.Network || len(impl.WritableRoots) != 0 {
		t.Fatalf("implement %+v", impl)
	}
	if w.Roles["tests"].Claude != "plan" || w.Families["luna"].Codex != "read-only" {
		t.Fatalf("%+v", w)
	}
	joined := strings.Join(w.Ignored, "\n")
	for _, want := range []string{
		"worker_modes.roles.implement.claude auto in .duolog/config.local.yaml is ignored: a project config may only lower worker modes; plan applies",
		"worker_modes.roles.implement.network true in .duolog/config.local.yaml is ignored",
		"worker_modes.roles.implement.writable_roots " + cacheB + " in .duolog/config.local.yaml is ignored",
		"worker_modes.roles.tests.claude acceptEdits in .duolog/config.local.yaml is ignored: a project config may only lower worker modes; plan applies",
		"worker_modes.families.luna.codex workspace-write in .duolog/config.local.yaml is ignored",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Ignored lacks %q:\n%s", want, joined)
		}
	}
}

// Claude review C1: a project layer that changes a role, adds one or changes a
// model's family gains nothing above duolog's default and loses no lowering.
func TestWorkerModesProjectRolesAndModelsCannotRaise(t *testing.T) {
	ceiling := mode.NewCeiling("claude", "bypassPermissions", mode.Full, mode.Full)
	resolve := func(cfg *Config, role, model string) mode.Resolved {
		route := cfg.Roles[role]
		return mode.Resolve("claude", cfg.WorkerModes.Request(role, Families(cfg, role, model), "claude"), ceiling, route.Sandbox)
	}
	t.Run("family entry on a changed or new role", func(t *testing.T) {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "worker_modes:\n  families:\n    haiku: {claude: bypassPermissions}\n")
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "roles:\n  review_final: {use: {agent: claude, model: haiku}, sandbox: workspace-write}\n  newrole: {use: {agent: claude, model: haiku}, fallback: {agent: codex, model: sol}, sandbox: workspace-write}\n")
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"review_final", "newrole"} {
			if r := resolve(cfg, role, "haiku"); r.Looser || r.Native != "acceptEdits" {
				t.Errorf("%s: %+v", role, r)
			}
		}
	})
	t.Run("role entry on a role the project changes", func(t *testing.T) {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "worker_modes:\n  roles:\n    review_final: {claude: bypassPermissions}\n    implement: {claude: bypassPermissions, codex: workspace-write, network: true}\n    docs: {claude: auto}\n")
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "roles:\n  review_final: {use: {agent: claude, model: haiku}, sandbox: workspace-write}\n")
		writeConfig(t, filepath.Join(root, ".duolog/config.local.yaml"), "roles:\n  implement: {use: {agent: claude, model: sonnet}}\n")
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, role := range []string{"review_final", "implement"} {
			if r := resolve(cfg, role, "sonnet"); r.Looser {
				t.Errorf("%s: %+v", role, r)
			}
		}
		if net, roots := cfg.WorkerModes.Extras("implement"); net || len(roots) > 0 {
			t.Error("implement keeps extras", net, roots)
		}
		if r := resolve(cfg, "docs", "sonnet"); r.Native != "auto" {
			t.Errorf("an untouched role loses its entry: %+v", r)
		}
		joined := strings.Join(cfg.WorkerModes.Ignored, "\n")
		for _, want := range []string{
			"worker_modes.roles.review_final.claude bypassPermissions is ignored: .duolog/config.yaml changes role review_final; duolog's default applies",
			"worker_modes.roles.implement.claude bypassPermissions is ignored: .duolog/config.local.yaml changes role implement",
			"worker_modes.roles.implement.network true is ignored: .duolog/config.local.yaml changes role implement",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("Ignored lacks %q:\n%s", want, joined)
			}
		}
	})
	t.Run("a model's family", func(t *testing.T) {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "worker_modes:\n  roles:\n    implement: {claude: bypassPermissions}\n  families:\n    haiku: {claude: plan}\n")
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "models:\n  haiku: {family: opus}\n")
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		// Level plan; a Claude worker starts in default, which the gate holds.
		if r := resolve(cfg, "implement", "haiku"); r.Level != mode.Plan || r.Native != "default" {
			t.Errorf("%+v", r)
		}
	})
	// Re-review Important 2: routes chosen outside roles.<R>.
	t.Run("a route moved outside roles", func(t *testing.T) {
		root := isolatedRoot(t)
		writeConfig(t, userConfigPath(t), "roles:\n  implement: {use: {agent: claude, model: haiku}, fallback: {agent: claude, model: sonnet}}\n  docs: {use: {agent: claude, model: haiku}, fallback: {agent: claude, model: haiku}}\nworker_modes:\n  roles:\n    implement: {claude: bypassPermissions, codex: workspace-write}\n    docs: {codex: workspace-write}\n  families:\n    haiku: {claude: plan}\n    luna: {codex: read-only}\n")
		writeConfig(t, filepath.Join(root, ".duolog/config.yaml"), "sync:\n  agents:\n    model_map: {haiku: sol}\n")
		cfg, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		// A ladder step, routing.learned.yaml or --use on opus or a haiku pin.
		for _, model := range []string{"opus", "claude-3-5-haiku-20241022", "claude-opus-5-5"} {
			if r := resolve(cfg, "implement", model); r.Level != mode.Plan || r.Native != "default" {
				t.Errorf("implement on %s: %+v", model, r)
			}
		}
		// A project model_map moves the derived Codex fallback from luna to sol.
		req := cfg.WorkerModes.Request("docs", Families(cfg, "docs", "sol"), "codex")
		if req.Native != "read-only" {
			t.Errorf("docs on the mapped sol: %+v", req)
		}
	})
}

// A family entry lowers a role entry; alone it asks no more than duolog's
// default.
func TestWorkerModesFamilyOnlyLowers(t *testing.T) {
	w := WorkerModes{
		Roles:    map[string]WorkerMode{"implement": {Claude: "bypassPermissions"}},
		Families: map[string]WorkerMode{"haiku": {Claude: "auto"}, "opus": {Claude: "bypassPermissions"}, "sonnet": {Claude: "plan"}},
	}
	for _, tc := range []struct {
		role   string
		family []string
		want   mode.Request
	}{
		{"implement", []string{"haiku"}, mode.Request{Native: "auto", Source: "role implement, family haiku"}},
		{"implement", []string{"opus"}, mode.Request{Native: "bypassPermissions", Source: "role implement, family opus"}},
		// Re-review m1: alone, an entry above the default is skipped.
		{"tests", []string{"opus"}, mode.Request{}},
		{"tests", []string{"opus", "haiku", "sonnet"}, mode.Request{Native: "plan", Source: "family sonnet"}},
		{"tests", []string{"sol"}, mode.Request{}},
		{"implement", []string{"opus", "haiku"}, mode.Request{Native: "auto", Source: "role implement, family opus, family haiku"}},
	} {
		if got := w.Request(tc.role, tc.family, "claude"); got != tc.want {
			t.Errorf("%s %v: %+v want %+v", tc.role, tc.family, got, tc.want)
		}
	}
}

func TestFamily(t *testing.T) {
	cfg, _ := Load(isolatedRoot(t))
	for model, want := range map[string]string{"sol": "sol", "luna": "luna", "sonnet": "sonnet", "gpt-6.1-sol": "sol", "claude-haiku-5-5": "haiku", "claude-3-5-haiku-20241022": "haiku", "claude-3-opus-latest": "opus", "other": "other"} {
		if got := Family(cfg, model); got != want {
			t.Errorf("Family(%q)=%q want %q", model, got, want)
		}
	}
}
