package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// parentMarker is in duolog's own environment only; no worker check sees it.
const parentMarker = "DUOLOG_TEST_PARENT_ONLY"

// workerProcess is a built worker process of agent whose PATH finds only bin.
// Its prefix and settings differ by variant, as Build's do.
func workerProcess(agent string, variant workerenv.Variant, bin, codexHome string) workerenv.Process {
	p := workerenv.Process{Agent: agent, Shape: workerenv.Worker, Variant: variant,
		Env: []string{"PATH=" + filepath.Dir(bin), "WORKER_VARIANT=" + string(variant)}, CacheKey: "route-" + string(variant)}
	switch agent {
	case "claude":
		p.Settings = `{"autoMemoryEnabled":false}`
		if variant == workerenv.Free {
			p.Settings = `{"autoMemoryEnabled":false,"fastMode":false}`
		}
		p.Prefix = []string{"claude", "--settings", p.Settings}
	case "codex":
		p.Prefix = []string{"codex", "-c", "check_for_update_on_startup=false"}
		p.Env = append(p.Env, "CODEX_HOME="+codexHome)
	}
	return p
}

// recorded is what a fake binary saw on its last call.
func recorded(t *testing.T, bin string) (args, env []string) {
	t.Helper()
	for suffix, into := range map[string]*[]string{".args.json": &args, ".env.json": &env} {
		data, err := os.ReadFile(bin + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}
	return args, env
}

func callCount(t *testing.T, bin string) int {
	t.Helper()
	data, err := os.ReadFile(bin + ".calls.ndjson")
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// The agent check of a worker runs the worker's own process: its prefix, its
// settings and exactly its environment, found on its own PATH. A login that
// only duolog's environment has cannot pass it, and a passed check is kept
// per process route, never per agent. Server checks keep their policy, and
// Login still runs the agent's own login command.
func TestAgentAuthUsesWorkerProcess(t *testing.T) {
	ctx := context.Background()
	t.Setenv(parentMarker, "1")

	t.Run("claude free and paid", func(t *testing.T) {
		cache := t.TempDir()
		// duolog's own claude is logged in; the worker's is not, in the paid
		// variant, so only the worker's process can tell.
		parent := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"loggedIn":true}`})
		onlyPath(t, parent)
		if p := New(cache).Agent(ctx, "claude"); p != nil {
			t.Fatal(p)
		}
		for _, c := range []struct {
			variant  workerenv.Variant
			loggedIn bool
		}{{workerenv.Free, true}, {workerenv.Paid, false}} {
			free := workerProcess("claude", c.variant, "", "")
			key := strings.Join(free.Argv("auth", "status", "--json")[1:], " ")
			reply := `{"loggedIn":false}`
			if c.loggedIn {
				reply = `{"loggedIn":true}`
			}
			worker := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{key: {Stdout: reply}}})
			// Only the worker's own PATH finds the worker's binary.
			onlyPath(t, parent)
			p := workerProcess("claude", c.variant, worker, "")
			problem := NewForProcess(cache, p).Agent(ctx, "claude")
			if (problem == nil) != c.loggedIn {
				t.Fatalf("%s: %v", c.variant, problem)
			}
			args, env := recorded(t, worker)
			if want := p.Argv("auth", "status", "--json")[1:]; !slices.Equal(args, want) {
				t.Fatalf("%s check ran %q, want %q", c.variant, args, want)
			}
			for _, pair := range p.Env {
				if !slices.Contains(env, pair) {
					t.Errorf("%s check lacks %s: %q", c.variant, pair, env)
				}
			}
			if slices.ContainsFunc(env, func(pair string) bool { return strings.HasPrefix(pair, parentMarker+"=") }) {
				t.Errorf("%s check inherited duolog's environment: %q", c.variant, env)
			}
			// A passed check is kept for this route; another route asks again.
			before := callCount(t, worker)
			NewForProcess(cache, p).Agent(ctx, "claude")
			if after := callCount(t, worker); c.loggedIn && after != before || !c.loggedIn && after != before+1 {
				t.Errorf("%s: %d checks, then %d", c.variant, before, after)
			}
		}
		// The parent's passed check never stood for a worker: both worker
		// checks above ran their own binary.
	})

	t.Run("codex home", func(t *testing.T) {
		home := t.TempDir()
		p := workerProcess("codex", workerenv.Free, "", home)
		key := strings.Join(p.Argv("login", "status")[1:], " ")
		worker := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{key: {Exit: 1, Stderr: "Not logged in\n"}}})
		onlyPath(t)
		p = workerProcess("codex", workerenv.Free, worker, home)
		if problem := NewForProcess(t.TempDir(), p).Agent(ctx, "codex"); problem == nil || problem.Agent != "codex" || problem.Server != "" {
			t.Fatalf("a worker without a Codex login passed: %v", problem)
		}
		_, env := recorded(t, worker)
		if !slices.Contains(env, "CODEX_HOME="+home) {
			t.Fatalf("the check missed the worker's CODEX_HOME: %q", env)
		}
	})

	t.Run("servers keep their policy", func(t *testing.T) {
		parent := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{
			"mcp list --json": {Stdout: `[{"name":"wiki","auth_status":"not_logged_in"}]`},
		}})
		worker := testbin.Install(t, "codex", testbin.Scenario{Stdout: "unexpected"})
		onlyPath(t, parent)
		c := NewForProcess(t.TempDir(), workerProcess("codex", workerenv.Free, worker, t.TempDir()))
		if problem := c.Server(ctx, "codex", "wiki", nil); problem == nil || problem.Server != "wiki" {
			t.Fatalf("server check: %v", problem)
		}
		if n := callCount(t, worker); n != 0 {
			t.Fatalf("a server check ran the worker's process %d times", n)
		}
		// Login stays the agent's own command for the person.
		if got := (Problem{Agent: "codex"}).LoginArgv(); !slices.Equal(got, []string{"codex", "login"}) {
			t.Fatal(got)
		}
	})

	t.Run("forget drops the route's pass", func(t *testing.T) {
		cache := t.TempDir()
		key := strings.Join(workerProcess("claude", workerenv.Free, "", "").Argv("auth", "status", "--json")[1:], " ")
		worker := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{key: {Stdout: `{"loggedIn":true}`}}})
		onlyPath(t)
		p := workerProcess("claude", workerenv.Free, worker, "")
		c := NewForProcess(cache, p)
		c.Agent(ctx, "claude")
		c.Forget(Problem{Agent: "claude"})
		c.Agent(ctx, "claude")
		if n := callCount(t, worker); n != 2 {
			t.Fatalf("%d checks after Forget, want 2", n)
		}
	})
}
