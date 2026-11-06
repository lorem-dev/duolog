package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const (
	anthropicSecret = "sk-ant-runner-secret"
	openaiSecret    = "sk-openai-runner-secret"
	mcpSecret       = "mcp-bridged-secret"
)

// passthroughConfig lists both agents' API keys in env_passthrough, as a
// person who used them on purpose would, and allows paid Claude runs.
func passthroughConfig() *config.Config {
	cfg := &config.Config{EnvPassthrough: []string{"HOME", "PATH", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}}
	cfg.PaidRuns.Claude.Allow, cfg.PaidRuns.Claude.DailyCapUSD = true, 5
	cfg.PaidRuns.Claude.Env = []string{"ANTHROPIC_API_KEY"}
	return cfg
}

// built gives s the worker process of its route, as a launch path builds it:
// the variant, the route's context and protected roots, and the bridged MCP
// env through Build's route detection.
func built(t *testing.T, s Spec, cfg *config.Config, variant workerenv.Variant) Spec {
	t.Helper()
	in := workerenv.ProcessInput{Agent: s.Route.Agent, Shape: workerenv.Worker, Variant: variant, Config: cfg,
		Environ: os.Environ(), LeadID: "lead-123", Role: "implement", StateDir: project.StateRoot(s.Cwd),
		Minimal: s.Route.Context == "minimal", ProtectedRoots: s.ProtectedRoots, Launch: Launch(s)}
	if s.SkillTest {
		in.StateDir, in.Minimal = "", true
	}
	if s.MCP != nil {
		in.ExtraEnv = s.MCP.Env
	}
	p, err := workerenv.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	s.Process = &p
	return s
}

func envOf(t *testing.T, fake string) []string {
	t.Helper()
	var env []string
	if err := json.Unmarshal([]byte(readFile(t, fake+".env.json")), &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func hasName(env []string, name string) bool {
	return slices.ContainsFunc(env, func(pair string) bool { return strings.HasPrefix(pair, name+"=") })
}

// A listed API key never reaches a free worker, even with paid runs allowed;
// the paid variant gets only its own agent's paid env. Every launch path, a
// fresh run, a resumed one and a skill test, takes the process's prefix once
// and keeps its own sandbox, MCP limits, minimal context and persistence
// flags. Bridged MCP env is part of the build, so a route name in it is
// withheld from a free worker and is a paid signal of a paid one. The runner
// refuses a launch without a built process, or with MCP env the build never
// saw, and reports the child to Started before it waits.
func TestWorkerBuildGatesExplicitPassthrough(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", anthropicSecret)
	t.Setenv("OPENAI_API_KEY", openaiSecret)
	cfg := passthroughConfig()
	answer := `{"session_id":"w","usage":{"input_tokens":10,"output_tokens":2},"structured_output":{"done":true,"summary":"ok","files":[],"needs":[]}}`

	t.Run("actual environment", func(t *testing.T) {
		for _, variant := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
			fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: answer})
			s := spec(t, "claude")
			s.MCP = &MCP{Names: []string{"jira"}, Env: []string{"DUOLOG_MCP_JIRA_1=" + mcpSecret, "ANTHROPIC_AUTH_TOKEN=" + mcpSecret}}
			s = built(t, s, cfg, variant)
			var pids []int
			s.Started = func(pid int) error {
				pids = append(pids, pid)
				return nil
			}
			if _, err := Run(context.Background(), s); err != nil {
				t.Fatal(variant, err)
			}
			if len(pids) != 1 || pids[0] <= 0 {
				t.Fatalf("%s: Started saw %v", variant, pids)
			}
			env := envOf(t, fake)
			if hasName(env, "OPENAI_API_KEY") || strings.Contains(strings.Join(env, "\n"), openaiSecret) {
				t.Fatalf("%s claude worker got the codex key", variant)
			}
			if !slices.Contains(env, "DUOLOG_MCP_JIRA_1="+mcpSecret) {
				t.Fatalf("%s worker lost its bridged server env", variant)
			}
			switch variant {
			case workerenv.Free:
				if hasName(env, "ANTHROPIC_API_KEY") || hasName(env, "ANTHROPIC_AUTH_TOKEN") {
					t.Fatalf("free worker got a route name: %q", env)
				}
				if !slices.Contains(s.Process.Dropped, "ANTHROPIC_AUTH_TOKEN") {
					t.Fatalf("a bridged route name was not withheld: %v", s.Process.Dropped)
				}
			case workerenv.Paid:
				if !slices.Contains(env, "ANTHROPIC_API_KEY="+anthropicSecret) || !slices.Contains(env, "ANTHROPIC_AUTH_TOKEN="+mcpSecret) {
					t.Fatalf("paid worker lacks its own paid env: %q", env)
				}
				if !slices.Contains(s.Process.Signals, workerenv.Signal{Name: "env:ANTHROPIC_AUTH_TOKEN", Layer: "mcp"}) {
					t.Fatalf("a bridged route name is no paid signal: %v", s.Process.Signals)
				}
			}
		}
	})

	t.Run("prefix once on every path", func(t *testing.T) {
		for _, variant := range []workerenv.Variant{workerenv.Free, workerenv.Paid} {
			for _, path := range []string{"fresh", "resumed", "persistent", "skill"} {
				for _, agent := range []string{"claude", "codex"} {
					if agent == "codex" && variant == workerenv.Paid {
						continue
					}
					s := spec(t, agent)
					s.MCP = &MCP{AllowedTools: []string{"mcp__jira__get"}, DisallowedTools: []string{"mcp__jira__delete"}}
					switch path {
					case "resumed":
						s.ThreadID = "thread-1"
					case "persistent":
						s.Persistent = true
					case "skill":
						s.SkillTest = true
					}
					s = built(t, s, cfg, variant)
					argv := Argv(s)
					if !slices.Equal(argv[:len(s.Process.Prefix)], s.Process.Prefix) {
						t.Fatalf("%s %s %s: argv does not start with the prefix: %q", variant, agent, path, argv)
					}
					line := strings.Join(argv, "\x00")
					checkOnce := func(word string) {
						if n := strings.Count(line, word); n != 1 {
							t.Errorf("%s %s %s: %q appears %d times: %q", variant, agent, path, word, n, argv)
						}
					}
					if agent == "claude" {
						checkOnce("--settings")
						settings := argv[slices.Index(argv, "--settings")+1]
						if fast := strings.Contains(settings, `"fastMode":false`); fast != (variant == workerenv.Free) {
							t.Errorf("%s %s: fast mode pin %v in %s", variant, path, fast, settings)
						}
						if !strings.Contains(settings, "claudeMdExcludes") {
							t.Errorf("%s %s: minimal context lost: %s", variant, path, settings)
						}
						if path != "skill" {
							checkOnce("--disallowedTools=Agent,Task,mcp__jira__delete")
							checkOnce("--allowedTools=mcp__jira__get")
							checkOnce("--strict-mcp-config")
							checkOnce("--disable-slash-commands")
						}
					} else {
						checkOnce("check_for_update_on_startup=false")
						if path != "skill" {
							checkOnce("project_doc_max_bytes=0")
							checkOnce("allow_login_shell=false")
						}
					}
					if path != "skill" {
						for _, arg := range modeArgs(agent) {
							if !slices.Contains(argv, arg) {
								t.Errorf("%s %s %s: sandbox %q lost: %q", variant, agent, path, arg, argv)
							}
						}
					}
					switch {
					case path == "resumed" && agent == "claude":
						checkOnce("--resume")
					case path == "resumed":
						checkOnce("resume")
					case path == "fresh" && agent == "claude", path == "skill" && agent == "claude":
						checkOnce("--no-session-persistence")
					case path == "fresh", path == "skill":
						checkOnce("--ephemeral")
					case path == "persistent" && strings.Contains(line, "--no-session-persistence"), path == "persistent" && strings.Contains(line, "--ephemeral"):
						t.Errorf("%s %s: a persistent run is ephemeral: %q", variant, agent, argv)
					}
				}
			}
		}
	})

	t.Run("refused launches", func(t *testing.T) {
		fake := testbin.Install(t, "claude", testbin.Scenario{Stdout: answer})
		s := spec(t, "claude")
		s.Process = nil
		if _, err := Run(context.Background(), s); err == nil {
			t.Fatal("ran without a built process")
		}
		s = built(t, spec(t, "claude"), cfg, workerenv.Free)
		s.MCP = &MCP{Names: []string{"jira"}, Env: []string{"DUOLOG_MCP_JIRA_1=" + mcpSecret}}
		if _, err := Run(context.Background(), s); err == nil || strings.Contains(err.Error(), mcpSecret) {
			t.Fatalf("ran with MCP env outside the build: %v", err)
		}
		other := built(t, spec(t, "codex"), cfg, workerenv.Free)
		s = built(t, spec(t, "claude"), cfg, workerenv.Free)
		s.Process = other.Process
		if _, err := Run(context.Background(), s); err == nil {
			t.Fatal("ran a claude route with a codex process")
		}
		if _, err := os.Stat(fake + ".calls.ndjson"); err == nil {
			t.Fatal("a refused launch started the binary")
		}
	})

	t.Run("started fails", func(t *testing.T) {
		testbin.Install(t, "claude", testbin.Scenario{Stdout: answer, SleepMS: 30000})
		s := built(t, spec(t, "claude"), cfg, workerenv.Free)
		attach := errors.New("account state refused the child")
		var pid int
		s.Started = func(p int) error {
			pid = p
			return attach
		}
		start := time.Now()
		_, err := Run(context.Background(), s)
		if !errors.Is(err, attach) || pid <= 0 {
			t.Fatalf("a failed attach was not reported: %v (pid %d)", err, pid)
		}
		// The child that could not be recorded was stopped, not waited for.
		if elapsed := time.Since(start); elapsed > 20*time.Second {
			t.Fatalf("the unrecorded child ran on for %v", elapsed)
		}
	})
}

// modeArgs are the sandbox flags of spec's plan mode.
func modeArgs(agent string) []string {
	if agent == "codex" {
		return []string{"-s", "read-only"}
	}
	return []string{"--permission-mode", "plan"}
}
