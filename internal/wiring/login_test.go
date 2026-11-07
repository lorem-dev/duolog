package wiring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// The login tests that were internal/auth's run the real ports of both agents
// through the checker that delegate builds over them.

const loginToken = "s3cret-auth-token"

// checkerOf is the real checker for a worker that runs as p; the zero process
// is no worker's.
func checkerOf(cacheDir string, p launch.Process) delegate.Checker {
	return delegate.NewChecker(DelegateDeps(), cacheDir, p)
}

func onlyPath(t *testing.T, bins ...string) {
	t.Helper()
	dirs := []string{t.TempDir()}
	for _, b := range bins {
		dirs = append(dirs, filepath.Dir(b))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

func TestAgentStatus(t *testing.T) {
	cases := []struct {
		name, agent string
		sc          testbin.Scenario
		bad         bool
	}{
		{"codex logged in", "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"login status": {Stdout: "Logged in using ChatGPT\n"}}}, false},
		{"codex logged out", "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"login status": {Exit: 1, Stderr: "Not logged in\n"}}}, true},
		{"claude logged in", "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"auth status --json": {Stdout: `{"loggedIn":true}`}}}, false},
		{"claude logged out", "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"auth status --json": {Stdout: `{"loggedIn":false}`, Exit: 1}}}, true},
		{"claude unreadable", "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"auth status --json": {Stdout: "?"}}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			onlyPath(t, testbin.Install(t, c.agent, c.sc))
			p := checkerOf("", launch.Process{}).Agent(context.Background(), c.agent)
			if (p != nil) != c.bad {
				t.Fatal(p)
			}
		})
	}
	// A missing binary is no answer, not a failed login.
	onlyPath(t)
	if p := checkerOf("", launch.Process{}).Agent(context.Background(), "codex"); p != nil {
		t.Fatal(p)
	}
}

func TestStaticServerProbe(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		bad    bool
	}{
		{"ok", 200, `{"jsonrpc":"2.0","id":1,"result":{}}`, false},
		{"401", 401, "", true},
		{"403", 403, "", true},
		{"rpc error names the token", 200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32001,"message":"Invalid token"}}`, true},
		{"other rpc error", 200, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no such method"}}`, false},
		{"server error", 500, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					got = r.Header.Get("X-Token")
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			def := caps.Server{Name: "jira", Agent: "codex", Transport: "http", URL: srv.URL, File: "config.toml [mcp_servers.jira]", Headers: []caps.Pair{{Key: "X-Token", Value: loginToken}}}
			dir := t.TempDir()
			p := checkerOf(dir, launch.Process{}).Server(context.Background(), "claude", "jira", &def)
			if (p != nil) != c.bad || got != loginToken {
				t.Fatal(p, got == loginToken)
			}
			if p != nil {
				if !p.Static || p.Where != def.File || p.LoginArgv() != nil || strings.Contains(p.Reason, loginToken) {
					t.Fatalf("%+v", p)
				}
			}
			data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
			if strings.Contains(string(data), loginToken) {
				t.Fatal("token in cache")
			}
		})
	}
}

// An unreachable server is not a refused login.
func TestProbeNetworkFailurePasses(t *testing.T) {
	def := caps.Server{Name: "x", Transport: "http", URL: "http://127.0.0.1:1/mcp", Headers: []caps.Pair{{Key: "A", Value: "b"}}}
	if p := checkerOf("", launch.Process{}).Server(context.Background(), "codex", "x", &def); p != nil {
		t.Fatal(p)
	}
}

func TestOAuthServerStatus(t *testing.T) {
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"mcp list --json": {Stdout: `[{"name":"wiki","auth_status":"not_logged_in"},{"name":"docs","auth_status":"o_auth"}]`}}})
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"mcp get wiki": {Stdout: "wiki:\n  Status: ! Needs authentication\n"},
		"mcp get docs": {Stdout: "docs:\n  Status: Connected\n"},
	}})
	onlyPath(t, codex, claude)
	oauth := &caps.Server{Transport: "http", OAuth: true}
	for _, agent := range []string{"codex", "claude"} {
		c := checkerOf("", launch.Process{})
		if p := c.Server(context.Background(), agent, "wiki", oauth); p == nil || p.Static || p.Command() != agent+" mcp login wiki" {
			t.Fatal(agent, p)
		}
		if p := c.Server(context.Background(), agent, "docs", oauth); p != nil {
			t.Fatal(agent, p)
		}
	}
	// A stdio server would have to start to be checked.
	if p := checkerOf("", launch.Process{}).Server(context.Background(), "codex", "wiki", &caps.Server{Transport: "stdio"}); p != nil {
		t.Fatal(p)
	}
}

func TestLogin(t *testing.T) {
	p := delegate.Problem{Agent: "codex", Server: "wiki"}
	// Only the timeout case gets a short wait: a slow runner can take more than
	// half a second just to start the fake agent.
	cases := []struct {
		name string
		sc   testbin.Scenario
		wait time.Duration
		want delegate.LoginResult
	}{
		{"done", testbin.Scenario{}, 30 * time.Second, delegate.LoggedIn},
		{"closed", testbin.Scenario{Exit: 1, Stderr: "login cancelled\n"}, 30 * time.Second, delegate.Declined},
		{"needs a terminal", testbin.Scenario{Exit: 1, Stderr: "Error: Raw mode is not supported on the current process.stdin\n"}, 30 * time.Second, delegate.NeedsTerminal},
		{"timed out", testbin.Scenario{SleepMS: 60000}, 500 * time.Millisecond, delegate.TimedOut},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := testbin.Install(t, "codex", c.sc)
			onlyPath(t, fake)
			start := time.Now()
			if got := checkerOf("", launch.Process{}).Login(context.Background(), p, c.wait); got != c.want {
				t.Fatal(got)
			}
			// The fake hangs for a minute; ending within 15 s of a 0.5 s wait
			// leaves a slow runner room and still proves the login was bounded.
			if c.want == delegate.TimedOut && time.Since(start) > 15*time.Second {
				t.Fatal("login was not bounded")
			}
			if c.name == "done" {
				data, err := os.ReadFile(fake + ".args.json")
				if err != nil || string(data) != `["mcp","login","wiki"]` {
					t.Fatal(string(data), err)
				}
			}
		})
	}
	// A static credential has nothing to log in to.
	if got := checkerOf("", launch.Process{}).Login(context.Background(), delegate.Problem{Agent: "codex", Server: "x", Static: true}, time.Second); got != delegate.Declined {
		t.Fatal(got)
	}
}

// A redirect would carry the token headers to another url, so the check stops
// at the first answer and does not count it as a rejection.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Token")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	def := caps.Server{Name: "svc", Agent: "codex", Transport: "http", URL: srv.URL, Headers: []caps.Pair{{Key: "X-Token", Value: loginToken}}}
	if p := checkerOf(t.TempDir(), launch.Process{}).Server(context.Background(), "claude", "svc", &def); p != nil || leaked != "" {
		t.Fatal(p, leaked == loginToken)
	}
}

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
		if p := checkerOf(cache, launch.Process{}).Agent(ctx, "claude"); p != nil {
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
			problem := checkerOf(cache, p).Agent(ctx, "claude")
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
			checkerOf(cache, p).Agent(ctx, "claude")
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
		if problem := checkerOf(t.TempDir(), p).Agent(ctx, "codex"); problem == nil || problem.Agent != "codex" || problem.Server != "" {
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
		c := checkerOf(t.TempDir(), workerProcess("codex", workerenv.Free, worker, t.TempDir()))
		if problem := c.Server(ctx, "codex", "wiki", nil); problem == nil || problem.Server != "wiki" {
			t.Fatalf("server check: %v", problem)
		}
		if n := callCount(t, worker); n != 0 {
			t.Fatalf("a server check ran the worker's process %d times", n)
		}
		// Login stays the agent's own command for the person.
		if got := Agents()[1].Login.LoginArgv(""); !slices.Equal(got, []string{"codex", "login"}) {
			t.Fatal(got)
		}
	})

	t.Run("forget drops the route's pass", func(t *testing.T) {
		cache := t.TempDir()
		key := strings.Join(workerProcess("claude", workerenv.Free, "", "").Argv("auth", "status", "--json")[1:], " ")
		worker := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{key: {Stdout: `{"loggedIn":true}`}}})
		onlyPath(t)
		p := workerProcess("claude", workerenv.Free, worker, "")
		c := checkerOf(cache, p)
		c.Agent(ctx, "claude")
		c.Forget(delegate.Problem{Agent: "claude"})
		c.Agent(ctx, "claude")
		if n := callCount(t, worker); n != 2 {
			t.Fatalf("%d checks after Forget, want 2", n)
		}
	})
}
