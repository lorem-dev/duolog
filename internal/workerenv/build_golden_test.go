package workerenv

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// Build is the contract between duolog and the agent processes it starts:
// the environment names, the argv prefix, Claude's settings, Codex's -c
// overrides and the route signals. These goldens pin all of it before the
// code moves. Run with -update to rewrite them after a change that is meant.
var updateGolden = flag.Bool("update", false, "rewrite the golden files")

// checkGolden compares got with testdata/golden/<name>.golden.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".golden")
	got += "\n"
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v; run with -update to write it", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from its golden.\n got:\n%s\nwant:\n%s", name, got, want)
	}
}

// goldenPlace holds the temporary folders of one case and turns their paths
// into fixed tokens, in the slash form and in the native one.
type goldenPlace struct{ tmp, home, root string }

func newGoldenPlace(t *testing.T) goldenPlace {
	t.Helper()
	tmp := t.TempDir()
	place := goldenPlace{tmp: tmp, home: filepath.Join(tmp, "home"), root: filepath.Join(tmp, "project")}
	for _, dir := range []string{place.home, place.root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return place
}

// clean replaces the folders longest first, so that <HOME> wins over <TMP>.
func (p goldenPlace) clean(s string) string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	for _, pair := range [][2]string{{filepath.Dir(exe), "<EXE_DIR>"}, {p.home, "<HOME>"}, {p.tmp, "<TMP>"}} {
		s = strings.ReplaceAll(s, filepath.ToSlash(pair[0]), pair[1])
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	return s
}

// write puts a settings file in place.
func (p goldenPlace) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// goldenEnviron is what the launching process has set: every kind of name.
// The values are secrets, tokens or paths of no worth, and no golden holds
// them.
func goldenEnviron(p goldenPlace) []string {
	return []string{
		"PATH=/usr/bin", "HOME=" + p.home,
		"ANTHROPIC_API_KEY=sk-ant-golden", "ANTHROPIC_AUTH_TOKEN=tok-golden",
		"ANTHROPIC_BASE_URL=https://llm.example.com/claude",
		"OPENAI_API_KEY=sk-oai-golden", "CODEX_API_KEY=sk-oai-golden", "CORP_LLM_KEY=corp-golden",
		"CLAUDE_PAID_EXTRA=c", "CODEX_PAID_EXTRA=x", "GOLDEN_KEEP=kept", "GOLDEN_UNLISTED=unlisted",
		"DUOLOG_LEAD=old-lead", "REX_SESSION=rex", "DISABLE_AUTOUPDATER=0",
	}
}

func goldenConfig() *config.Config {
	cfg := &config.Config{EnvPassthrough: []string{
		"PATH", "HOME", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"OPENAI_API_KEY", "CODEX_API_KEY", "CORP_LLM_KEY", "GOLDEN_KEEP", "DUOLOG_LEAD", "REX_SESSION",
	}}
	cfg.PaidRuns.UsableShare = 0.5
	cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 5
	cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY", "CLAUDE_PAID_EXTRA"}
	cfg.PaidRuns.Claude.SubscriptionEndpoints = map[string]string{"corp-gateway": "https://llm.example.com/claude"}
	cfg.PaidRuns.Codex.Allow, cfg.PaidRuns.Codex.DailyCapUSD = true, 5
	cfg.PaidRuns.Codex.Env = []string{"OPENAI_API_KEY", "CODEX_PAID_EXTRA"}
	return cfg
}

// goldenLayers writes the agent's user settings into the case's home folder
// and reads them as Build's callers do. Managed policy is left out: it lives
// outside the temporary folders and would make the golden depend on the
// machine. The settings turn fast mode on, set a base URL that is a
// subscription endpoint and an API key, and for Codex name a provider with
// its own env_key and a service tier.
func goldenLayers(t *testing.T, p goldenPlace, agent string, cfg *config.Config) []LayerFacts {
	t.Helper()
	switch agent {
	case "claude":
		p.write(t, filepath.Join(p.home, ".claude", "settings.json"),
			`{"fastMode": true, "env": {"ANTHROPIC_BASE_URL": "https://llm.example.com/claude", "ANTHROPIC_API_KEY": "sk-ant-settings"}, `+
				`"enabledPlugins": {"alpha@market": true, "beta@market": true, "duolog@duolog": true}}`)
	case "codex":
		p.write(t, filepath.Join(p.home, ".codex", "config.toml"),
			"model_provider = \"corp\"\nservice_tier = \"fast\"\n[model_providers.corp]\nname = \"corp\"\nbase_url = \"https://llm.corp.example/v1\"\nenv_key = \"CORP_LLM_KEY\"\n")
	}
	loc := Locations{Home: p.home, Root: p.root, GOOS: "linux", Environ: goldenEnviron(p)}
	var facts []LayerFacts
	for _, layer := range SettingsLayers(agent, loc) {
		if !layer.Managed {
			facts = append(facts, ReadLayer(agent, layer, endpoints(cfg, agent), os.ReadFile, os.Stat))
		}
	}
	return facts
}

// goldenFixed are the variables whose value duolog fixes or the test chose;
// every other value is a secret or a path and shows as a name only.
var goldenFixed = []string{"DUOLOG_DEPTH", "DUOLOG_LEAD", "DUOLOG_ROLE", "DUOLOG_PEER", "DUOLOG_RUN_ID",
	"DISABLE_AUTOUPDATER", "CLAUDE_CODE_DISABLE_AUTO_MEMORY", "GOLDEN_KEEP"}

// renderProcess is the golden text of one built process.
func renderProcess(p goldenPlace, proc Process, runFlags ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent: %s\nshape: %s\nvariant: %s\n", proc.Agent, proc.Shape, proc.Variant)

	b.WriteString("\nenv (in order; a value only where it is fixed):\n")
	for _, pair := range proc.Env {
		name, value, _ := strings.Cut(pair, "=")
		switch {
		case name == "PATH":
			// The search path separator is the system's, ":" or ";".
			fmt.Fprintf(&b, "  PATH=%s\n", strings.ReplaceAll(p.clean(value), string(os.PathListSeparator), "<PATHSEP>"))
		case slices.Contains(goldenFixed, name):
			fmt.Fprintf(&b, "  %s=%s\n", name, value)
		default:
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}

	b.WriteString("\nargv prefix:\n")
	for i, word := range proc.Prefix {
		if i > 0 && proc.Prefix[i-1] == "--settings" {
			word = "<SETTINGS>"
		}
		fmt.Fprintf(&b, "  %s\n", p.clean(word))
	}
	b.WriteString("\nlaunch words:\n")
	for _, word := range proc.Launch {
		fmt.Fprintf(&b, "  %s\n", p.clean(word))
	}
	b.WriteString("\nlaunch argv with run flags " + fmt.Sprint(runFlags) + ":\n")
	for i, word := range proc.LaunchArgv(runFlags...) {
		if i > 0 && proc.LaunchArgv(runFlags...)[i-1] == "--settings" {
			word = "<SETTINGS>"
		}
		fmt.Fprintf(&b, "  %s\n", p.clean(word))
	}

	b.WriteString("\nclaude settings (--settings):\n")
	if proc.Settings == "" {
		b.WriteString("  none\n")
	} else {
		var indented bytes.Buffer
		if err := json.Indent(&indented, []byte(p.clean(proc.Settings)), "  ", "  "); err != nil {
			fmt.Fprintf(&b, "  not JSON: %v\n", err)
		} else {
			fmt.Fprintf(&b, "  %s\n", indented.String())
		}
	}

	b.WriteString("\nsignals (name@layer):\n")
	for _, s := range proc.Signals {
		fmt.Fprintf(&b, "  %s@%s\n", s.Name, s.Layer)
	}
	if len(proc.Signals) == 0 {
		b.WriteString("  none\n")
	}
	fmt.Fprintf(&b, "\ndropped: %v\nendpoint entries: %v\nfast pinned: %t\nprovider env key: %q\n",
		proc.Dropped, proc.EndpointEntries, proc.FastPinned, proc.ProviderEnvKey)
	// The cache key hashes each layer's modification time, which differs from
	// run to run, so only its shape is pinned.
	fmt.Fprintf(&b, "cache key: %d hex digits\n", len(proc.CacheKey))
	return strings.TrimRight(b.String(), "\n")
}

// goldenCase changes the base input of one launch.
type goldenCase struct {
	name    string
	agent   string
	shape   Shape
	variant Variant
	set     func(p goldenPlace, in *ProcessInput)
}

var (
	codexReadOnly = []string{"exec", "-s", "read-only", "--skip-git-repo-check"}
	codexWrite    = []string{"exec", "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=true",
		"-c", `sandbox_workspace_write.writable_roots=["/safe/cache"]`}
)

func goldenCases() []goldenCase {
	var cases []goldenCase
	for _, agent := range []string{"claude", "codex"} {
		for _, shape := range []Shape{Worker, Peer, Lead} {
			for _, variant := range []Variant{Free, Paid} {
				cases = append(cases, goldenCase{name: fmt.Sprintf("%s_%s_%s", agent, shape, variant), agent: agent, shape: shape, variant: variant})
			}
		}
	}
	extraEnv := []string{"DUOLOG_RUN_ID=run-7", "MCP_TOKEN=mcp-golden", "ANTHROPIC_API_KEY=sk-ant-from-mcp", "OPENAI_API_KEY=sk-oai-from-mcp"}
	cases = append(cases,
		goldenCase{"claude_worker_free_minimal_extras", "claude", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.Minimal = true
			in.ExtraSettings = map[string]any{
				"permissions": map[string]any{"deny": []string{"Bash(rm:*)"}},
				"fastMode":    true, // never reaches the process
				"sandbox": map[string]any{"enabled": true, "filesystem": map[string]any{
					"denyRead": []string{"/private/keep"}}},
			}
			in.ProtectedRoots = []string{filepath.Join(p.home, ".duolog-usage")}
			in.ExtraEnv = extraEnv
			in.Launch = []string{"-p", "--output-format", "json"}
		}},
		goldenCase{"claude_worker_paid_extras", "claude", Worker, Paid, func(p goldenPlace, in *ProcessInput) {
			in.ExtraSettings = map[string]any{"model": "opus"}
			in.ExtraEnv = extraEnv
		}},
		goldenCase{"claude_worker_free_compact_window", "claude", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.Config.Workers.CompactWindow = "250000"
		}},
		goldenCase{"claude_peer_free_prefix", "claude", Peer, Free, func(p goldenPlace, in *ProcessInput) {
			in.Prefix = []string{"claude", "--model", "opus"}
		}},
		goldenCase{"claude_lead_free_observed_prefix", "claude", Lead, Free, func(p goldenPlace, in *ProcessInput) {
			in.Prefix = []string{"claude", "--dangerously-skip-permissions"}
		}},
		goldenCase{"codex_worker_free_read_only", "codex", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.Launch = codexReadOnly
		}},
		goldenCase{"codex_worker_free_workspace_write", "codex", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.Launch = codexWrite
		}},
		goldenCase{"codex_worker_paid_workspace_write", "codex", Worker, Paid, func(p goldenPlace, in *ProcessInput) {
			in.Launch = codexWrite
		}},
		goldenCase{"codex_peer_free_workspace_write", "codex", Peer, Free, func(p goldenPlace, in *ProcessInput) {
			in.Launch = codexWrite
		}},
		goldenCase{"codex_peer_paid_workspace_write", "codex", Peer, Paid, func(p goldenPlace, in *ProcessInput) {
			in.Launch = codexWrite
		}},
		goldenCase{"codex_worker_free_overrides_and_extras", "codex", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.ExtraSettings = map[string]any{"tui.notifications": false, "notify": []string{}, "model_reasoning_effort": "high", "service_tier": "fast"}
			in.ExtraEnv = extraEnv
			in.Launch = codexWrite
		}},
		goldenCase{"codex_worker_paid_overrides_and_extras", "codex", Worker, Paid, func(p goldenPlace, in *ProcessInput) {
			in.ExtraSettings = map[string]any{"tui.notifications": false, "model_reasoning_effort": "high"}
			in.ExtraEnv = extraEnv
			in.Launch = codexWrite
		}},
		goldenCase{"codex_worker_free_speed_in_launch", "codex", Worker, Free, func(p goldenPlace, in *ProcessInput) {
			in.Launch = []string{"exec", "-c", `service_tier="fast"`}
		}},
		goldenCase{"codex_lead_free_observed_prefix", "codex", Lead, Free, func(p goldenPlace, in *ProcessInput) {
			in.Prefix = []string{"codex", "-c", `service_tier="fast"`, "-c", "model=sol"}
		}},
	)
	return cases
}

func TestBuildGolden(t *testing.T) {
	for _, c := range goldenCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newGoldenPlace(t)
			cfg := goldenConfig()
			in := ProcessInput{
				Agent: c.agent, Shape: c.shape, Variant: c.variant, Config: cfg,
				Locations: Locations{Home: p.home, Root: p.root, GOOS: "linux", Environ: goldenEnviron(p)},
				Environ:   goldenEnviron(p), LeadID: "lead-1", Role: "implement", PeerID: "peer-1", StateDir: p.root,
				Layers: goldenLayers(t, p, c.agent, cfg),
			}
			if c.agent == "codex" {
				in.ProviderEnvKey = "CORP_LLM_KEY"
			}
			if c.set != nil {
				c.set(p, &in)
			}
			proc, err := Build(in)
			if err != nil {
				t.Fatal(err)
			}
			checkGolden(t, c.name, renderProcess(p, proc, "--run-flag", "value"))
		})
	}
}

// The refusals are part of the contract too: a paid variant without the
// opt-in, and an unknown agent, shape or variant.
func TestBuildRefusalsGolden(t *testing.T) {
	var b strings.Builder
	for _, c := range []struct {
		name string
		set  func(in *ProcessInput)
	}{
		{"paid without opt-in", func(in *ProcessInput) { in.Variant = Paid; in.Config.PaidRuns.Claude.Allow = false }},
		{"unknown agent", func(in *ProcessInput) { in.Agent = "gemini" }},
		{"unknown shape", func(in *ProcessInput) { in.Shape = "daemon" }},
		{"unknown variant", func(in *ProcessInput) { in.Variant = "cheap" }},
		{"no config", func(in *ProcessInput) { in.Config = nil }},
	} {
		in := ProcessInput{Agent: "claude", Shape: Worker, Variant: Free, Config: goldenConfig()}
		c.set(&in)
		_, err := Build(in)
		fmt.Fprintf(&b, "%s: %v\n", c.name, err)
	}
	checkGolden(t, "build_refusals", strings.TrimRight(b.String(), "\n"))
}
