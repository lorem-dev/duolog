package workerenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var layerTime = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

// denied marks a fake file that exists but cannot be read.
const denied = "\x00denied"

// fakeRead serves files from memory; any other path does not exist.
func fakeRead(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		data, ok := files[path]
		switch {
		case !ok:
			return nil, fs.ErrNotExist
		case data == denied:
			return nil, fs.ErrPermission
		}
		return []byte(data), nil
	}
}

type fakeInfo struct{ fs.FileInfo }

func (fakeInfo) ModTime() time.Time { return layerTime }

func fakeStat(string) (os.FileInfo, error) { return fakeInfo{}, nil }

func signalNames(signals []Signal) []string {
	names := []string{}
	for _, s := range signals {
		names = append(names, s.Name)
	}
	return names
}

func equal(a, b []string) bool {
	return slices.Equal(a, b) || len(a) == 0 && len(b) == 0
}

// stubSystem replaces the reads that reach outside the test's files: the
// managed drop-in folder, non-file policy and Codex folder trust.
func stubSystem(t *testing.T, dirs map[string][]string, dirErr error, policy bool, trusted bool) {
	t.Helper()
	oldList, oldPolicy, oldTrust := listFiles, NonFilePolicy, codexTrusted
	t.Cleanup(func() { listFiles, NonFilePolicy, codexTrusted = oldList, oldPolicy, oldTrust })
	listFiles = func(dir string) ([]string, error) {
		if names, ok := dirs[dir]; ok {
			return names, nil
		}
		if dirErr != nil {
			return nil, dirErr
		}
		return nil, fs.ErrNotExist
	}
	NonFilePolicy = func(string) bool { return policy }
	codexTrusted = func(string, string) (bool, error) { return trusted, nil }
}

// Every settings layer counts: user, each ancestor project and local file,
// an explicit --settings argument and managed policy, with its drop-in files.
// Route names in env, any apiKeyHelper, a forceLoginMethod other than
// claudeai and fastMode true in managed policy are signals, named and never
// valued. A missing optional file is absent; a malformed or unreadable layer
// is paid. Helpers never run, and a later layer never clears an earlier one.
func TestSettingsEveryLayerAndUnreadable(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	root := filepath.Join(work, "repo")
	dropIns := "/etc/claude-code/managed-settings.d"
	stubSystem(t, map[string][]string{dropIns: {"20-b.json", "10-a.json", ".hidden.json", "notes.txt"}}, nil, false, false)
	loc := Locations{Home: home, Root: root, GOOS: "linux", Environ: []string{"PATH=/bin"}}

	claudeDir := filepath.Join(home, ".claude")
	want := []Layer{
		{Name: "user", Path: filepath.Join(claudeDir, "settings.json")},
		{Name: "project", Path: filepath.Join(work, ".claude", "settings.json")},
		{Name: "project", Path: filepath.Join(root, ".claude", "settings.json")},
		{Name: "local", Path: filepath.Join(work, ".claude", "settings.local.json")},
		{Name: "local", Path: filepath.Join(root, ".claude", "settings.local.json")},
		{Name: "managed", Path: "/etc/claude-code/managed-settings.json", Managed: true},
		{Name: "managed", Path: dropIns + "/10-a.json", Managed: true},
		{Name: "managed", Path: dropIns + "/20-b.json", Managed: true},
	}
	layers := SettingsLayers("claude", loc)
	if !slices.Equal(layers, want) {
		t.Fatalf("claude layers:\n got %v\nwant %v", layers, want)
	}

	// CLAUDE_CONFIG_DIR moves the user layer; the environment is the shape's.
	moved := filepath.Join(home, "alt")
	loc.Environ = append(loc.Environ, "CLAUDE_CONFIG_DIR="+moved)
	if got := SettingsLayers("claude", loc)[0]; got.Path != filepath.Join(moved, "settings.json") {
		t.Errorf("CLAUDE_CONFIG_DIR user layer = %v", got)
	}
	loc.Environ = loc.Environ[:1]

	// Managed roots per platform; ProgramData comes from the environment.
	for _, c := range []struct {
		goos    string
		environ []string
		want    string
	}{
		{"darwin", nil, "/Library/Application Support/ClaudeCode/managed-settings.json"},
		{"linux", nil, "/etc/claude-code/managed-settings.json"},
		{"windows", nil, `C:\ProgramData\ClaudeCode\managed-settings.json`},
		{"windows", []string{`PROGRAMDATA=D:\PD`}, `D:\PD\ClaudeCode\managed-settings.json`},
	} {
		got := SettingsLayers("claude", Locations{Home: home, Root: home, GOOS: c.goos, Environ: c.environ})
		first := got[slices.IndexFunc(got, func(l Layer) bool { return l.Managed })]
		if first.Path != c.want || first.Name != "managed" {
			t.Errorf("%s managed layer = %+v, want %s", c.goos, first, c.want)
		}
	}

	// The settings, one per layer.
	marker := filepath.Join(home, "helper-ran")
	files := map[string]string{
		want[0].Path: `{"env": {"ANTHROPIC_BASE_URL": "https://gw.example.com", "CLAUDE_CODE_OAUTH_TOKEN": "sk-test-oauth", "EDITOR": "vi"}}`,
		want[1].Path: `{"apiKeyHelper": "touch ` + filepath.ToSlash(marker) + `"}`,
		want[2].Path: `{"forceLoginMethod": "claudeai", "fastMode": true}`,
		want[3].Path: `{"forceLoginMethod": "console"}`,
		want[4].Path: `{"env": {"ANTHROPIC_API_KEY": ""}}`,
		want[5].Path: `{"fastMode": true}`,
		want[6].Path: `{"fastMode": false}`,
		// want[7] is missing: absent.
	}
	read := fakeRead(files)
	argv := ArgvLayers("claude", []string{"-p", "--settings", `{"fastMode": true, "env": {"CLAUDE_CODE_USE_NEW": "1"}}`}, loc)
	if len(argv) != 1 || argv[0].Name != "argv" || argv[0].Managed {
		t.Fatalf("argv layers = %v", argv)
	}
	all := append(slices.Clone(layers[:5]), argv[0])
	all = append(all, layers[5:]...)
	fast := true
	slow := false
	for i, c := range []struct {
		signals, env []string
		fast         *bool
	}{
		{[]string{"env:ANTHROPIC_BASE_URL"}, []string{"ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "EDITOR"}, nil},
		{[]string{"apiKeyHelper"}, nil, nil},
		{nil, nil, &fast},
		{[]string{"forceLoginMethod"}, nil, nil},
		{[]string{"env:ANTHROPIC_API_KEY"}, []string{"ANTHROPIC_API_KEY"}, nil},
		{[]string{"env:CLAUDE_CODE_USE_NEW"}, []string{"CLAUDE_CODE_USE_NEW"}, &fast},
		{[]string{"fastMode"}, nil, &fast},
		{nil, nil, &slow},
		{nil, nil, nil},
	} {
		facts := ReadLayer("claude", all[i], nil, read, fakeStat)
		if facts.Unreadable {
			t.Errorf("layer %d %v unreadable", i, all[i])
		}
		if got := signalNames(facts.Signals); !equal(got, c.signals) {
			t.Errorf("layer %d %v signals = %v, want %v", i, all[i], got, c.signals)
		}
		for _, s := range facts.Signals {
			if s.Layer != all[i].Name {
				t.Errorf("layer %d signal %+v names the wrong layer", i, s)
			}
		}
		if !equal(facts.EnvNames, c.env) {
			t.Errorf("layer %d env names = %v, want %v", i, facts.EnvNames, c.env)
		}
		if (facts.FastMode == nil) != (c.fast == nil) || facts.FastMode != nil && *facts.FastMode != *c.fast {
			t.Errorf("layer %d fast mode = %v, want %v", i, facts.FastMode, c.fast)
		}
		if i != 5 && i != 8 && !facts.Modified.Equal(layerTime) {
			t.Errorf("layer %d modified = %v", i, facts.Modified)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("reading settings ran apiKeyHelper")
	}

	// Malformed, unreadable and unresolved layers are paid.
	for _, c := range []struct {
		name, content string
		layer         Layer
	}{
		{"malformed", `{"env": `, Layer{Name: "user", Path: "s.json"}},
		{"not an object", `["env"]`, Layer{Name: "user", Path: "s.json"}},
		{"env not an object", `{"env": ["ANTHROPIC_API_KEY"]}`, Layer{Name: "project", Path: "s.json"}},
		{"denied", denied, Layer{Name: "local", Path: "s.json"}},
		{"missing argv file", "", Layer{Name: "argv", Path: "missing.json"}},
		{"bad argv json", "", Layer{Name: "argv", Path: `{"env": `}},
		{"unresolved policy", "", Layer{Name: "managed", Managed: true}},
	} {
		files := map[string]string{}
		if c.content != "" {
			files["s.json"] = c.content
		}
		facts := ReadLayer("claude", c.layer, nil, fakeRead(files), fakeStat)
		if !facts.Unreadable || !equal(signalNames(facts.Signals), []string{"unreadable"}) {
			t.Errorf("%s: facts = %+v, want unreadable", c.name, facts)
		}
	}

	// A drop-in folder that cannot be listed, and non-file policy, add a
	// managed layer that reads as unreadable.
	stubSystem(t, nil, fs.ErrPermission, true, false)
	layers = SettingsLayers("claude", loc)
	var managed []Layer
	for _, l := range layers {
		if l.Managed {
			managed = append(managed, l)
		}
	}
	if len(managed) != 3 || managed[1].Path != dropIns || managed[2].Path != "" {
		t.Fatalf("managed layers = %v", managed)
	}
	for _, l := range managed[1:] {
		if facts := ReadLayer("claude", l, nil, fakeRead(map[string]string{dropIns: denied}), fakeStat); !facts.Unreadable {
			t.Errorf("managed layer %v readable", l)
		}
	}

	// Codex: CODEX_HOME, then trusted project layers, a selected profile
	// file and argv -c fields.
	codexHome := filepath.Join(home, "codex-home")
	loc.Environ = []string{"CODEX_HOME=" + codexHome}
	stubSystem(t, nil, nil, false, false)
	if got := SettingsLayers("codex", loc); !slices.Equal(got, []Layer{{Name: "user", Path: filepath.Join(codexHome, "config.toml")}}) {
		t.Errorf("untrusted codex layers = %v", got)
	}
	stubSystem(t, nil, nil, false, true)
	codexWant := []Layer{
		{Name: "user", Path: filepath.Join(codexHome, "config.toml")},
		{Name: "project", Path: filepath.Join(work, ".codex", "config.toml")},
		{Name: "project", Path: filepath.Join(root, ".codex", "config.toml")},
	}
	if got := SettingsLayers("codex", loc); !slices.Equal(got, codexWant) {
		t.Errorf("trusted codex layers:\n got %v\nwant %v", got, codexWant)
	}
	loc.Environ = nil
	if got := SettingsLayers("codex", loc)[0].Path; got != filepath.Join(home, ".codex", "config.toml") {
		t.Errorf("default codex user layer = %s", got)
	}
	loc.Environ = []string{"CODEX_HOME=" + codexHome}

	codexArgv := ArgvLayers("codex", []string{"exec", "--profile", "fast", "-c", "forced_login_method=api",
		"--config", `openai_base_url="https://gw.example.com"`, "-c", `model="gpt-5"`}, loc)
	argvWant := []Layer{
		{Name: "profile", Path: filepath.Join(codexHome, "fast.config.toml")},
		{Name: "argv", Path: "forced_login_method=api"},
		{Name: "argv", Path: `openai_base_url="https://gw.example.com"`},
		{Name: "argv", Path: `model="gpt-5"`},
	}
	if !slices.Equal(codexArgv, argvWant) {
		t.Fatalf("codex argv layers:\n got %v\nwant %v", codexArgv, argvWant)
	}
	codexFiles := map[string]string{
		codexWant[0].Path: `
forced_login_method = "chatgpt"
profile = "work"
model_provider = "openai"

[profiles.work]
model_provider = "corp"

[model_providers.corp]
name = "Corp"
base_url = "https://gw.example.com/v1"
env_key = "CORP_LLM_SECRET"

[model_providers.unused]
env_key = "UNUSED_SECRET"
`,
		codexWant[1].Path: `forced_login_method = "api"`,
		codexWant[2].Path: `model_provider = "proxy"
[model_providers.proxy]
requires_openai_auth = true
`,
		argvWant[0].Path: `chatgpt_base_url = "https://gw.example.com"`,
	}
	read = fakeRead(codexFiles)
	for i, c := range []struct {
		layer   Layer
		signals []string
		env     []string
	}{
		{codexWant[0], []string{"model_provider"}, []string{"CORP_LLM_SECRET"}},
		{codexWant[1], []string{"forced_login_method"}, nil},
		{codexWant[2], nil, nil},
		{argvWant[0], []string{"chatgpt_base_url"}, nil},
		{argvWant[1], []string{"forced_login_method"}, nil},
		{argvWant[2], []string{"openai_base_url"}, nil},
		{argvWant[3], nil, nil},
		{Layer{Name: "profile", Path: filepath.Join(codexHome, "missing.config.toml")}, nil, nil},
	} {
		facts := ReadLayer("codex", c.layer, map[string]string{"gw": "https://gw.example.com"}, read, fakeStat)
		if facts.Unreadable {
			t.Errorf("codex layer %d unreadable", i)
		}
		if got := signalNames(facts.Signals); !equal(got, c.signals) {
			t.Errorf("codex layer %d %v signals = %v, want %v", i, c.layer, got, c.signals)
		}
		if !equal(facts.EnvNames, c.env) || len(facts.EndpointEntries) != 0 {
			t.Errorf("codex layer %d env %v entries %v, want env %v", i, facts.EnvNames, facts.EndpointEntries, c.env)
		}
	}
	for _, content := range []string{"forced_login_method = ", denied, `profile = 3`} {
		facts := ReadLayer("codex", codexWant[0], nil, fakeRead(map[string]string{codexWant[0].Path: content}), fakeStat)
		if !facts.Unreadable {
			t.Errorf("codex %q readable", content)
		}
	}
}
