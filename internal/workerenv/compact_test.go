package workerenv

import (
	"encoding/json"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

var testLadder = map[string]int{"pro": 250000, "max": 500000, "ultra": 700000}

// compactEnv builds a launch with workers.compact_window set to window, the
// test ladder and the given plan, and returns the env block of its --settings
// JSON.
func compactEnv(t *testing.T, agent string, shape Shape, variant Variant, window, plan string, extra map[string]any) map[string]string {
	t.Helper()
	cfg := goldenConfig()
	cfg.Workers.CompactWindow, cfg.Workers.CompactLadder = window, testLadder
	p := newGoldenPlace(t)
	in := ProcessInput{
		Agent: agent, Shape: shape, Variant: variant, Config: cfg, Plan: plan,
		Locations: Locations{Home: p.home, Root: p.root, GOOS: "linux", Environ: goldenEnviron(p)},
		Environ:   goldenEnviron(p), LeadID: "lead-1", Role: "implement", PeerID: "peer-1", StateDir: p.root,
		ExtraSettings: extra,
	}
	proc, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if proc.Settings == "" {
		return nil
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(proc.Settings), &settings); err != nil {
		t.Fatal(err)
	}
	return settings.Env
}

func TestCompactWindowZeroWritesNoEnvEntry(t *testing.T) {
	for _, shape := range []Shape{Worker, Peer} {
		for _, plan := range []string{"", "pro"} {
			if env := compactEnv(t, "claude", shape, Free, "0", plan, nil); len(env) != 0 {
				t.Fatalf("%s %q: got env %v", shape, plan, env)
			}
		}
	}
}

func TestCompactWindowWritesTheEnvEntryForWorkersAndPeers(t *testing.T) {
	for _, shape := range []Shape{Worker, Peer} {
		for _, variant := range []Variant{Free, Paid} {
			env := compactEnv(t, "claude", shape, variant, "250000", "", nil)
			if len(env) != 1 || env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "250000" {
				t.Fatalf("%s %s: got env %v", shape, variant, env)
			}
		}
	}
}

func TestCompactWindowAutoFollowsThePlan(t *testing.T) {
	for _, c := range []struct{ plan, want string }{
		{"pro", "250000"}, {"max", "500000"}, {"ultra", "700000"}, {"enterprise", ""}, {"", ""},
	} {
		env := compactEnv(t, "claude", Worker, Free, "auto", c.plan, nil)
		if got, ok := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; got != c.want || ok != (c.want != "") {
			t.Fatalf("plan %q: got env %v, want %q", c.plan, env, c.want)
		}
	}
}

func TestCompactWindowKeepsTheCallersEnvSettings(t *testing.T) {
	env := compactEnv(t, "claude", Worker, Free, "250000", "", map[string]any{"env": map[string]any{"KEEP": "1"}})
	if env["KEEP"] != "1" || env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "250000" {
		t.Fatalf("got env %v", env)
	}
}

func TestCompactWindowLeavesCodexAndTheLeadAlone(t *testing.T) {
	for _, c := range []struct {
		agent string
		shape Shape
	}{{"codex", Worker}, {"codex", Peer}, {"claude", Lead}} {
		cfg := goldenConfig()
		cfg.Workers = config.Workers{CompactWindow: "250000"}
		base := goldenConfig()
		p := newGoldenPlace(t)
		build := func(cfg *config.Config) Process {
			proc, err := Build(ProcessInput{
				Agent: c.agent, Shape: c.shape, Variant: Free, Config: cfg,
				Locations: Locations{Home: p.home, Root: p.root, GOOS: "linux", Environ: goldenEnviron(p)},
				Environ:   goldenEnviron(p), LeadID: "lead-1", Role: "implement", PeerID: "peer-1", StateDir: p.root,
			})
			if err != nil {
				t.Fatal(err)
			}
			return proc
		}
		got, want := build(cfg), build(base)
		if got.Settings != want.Settings || len(got.Prefix) != len(want.Prefix) || len(got.Env) != len(want.Env) {
			t.Fatalf("%s %s: compact_window changed the process", c.agent, c.shape)
		}
	}
}
