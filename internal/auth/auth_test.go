package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const token = "s3cret-auth-token"

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
			p := New("").Agent(context.Background(), c.agent)
			if (p != nil) != c.bad {
				t.Fatal(p)
			}
		})
	}
	// A missing binary is no answer, not a failed login.
	onlyPath(t)
	if p := New("").Agent(context.Background(), "codex"); p != nil {
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
				w.Write([]byte(c.body))
			}))
			defer srv.Close()
			def := capability.Server{Name: "jira", Agent: "codex", Transport: "http", URL: srv.URL, File: "config.toml [mcp_servers.jira]", Headers: []capability.Pair{{Key: "X-Token", Value: token}}}
			dir := t.TempDir()
			p := New(dir).Server(context.Background(), "claude", "jira", &def)
			if (p != nil) != c.bad || got != token {
				t.Fatal(p, got == token)
			}
			if p != nil {
				if !p.Static || p.Where != def.File || p.LoginArgv() != nil || strings.Contains(p.Reason, token) {
					t.Fatalf("%+v", p)
				}
			}
			data, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
			if strings.Contains(string(data), token) {
				t.Fatal("token in cache")
			}
		})
	}
}

// An unreachable server is not a refused login.
func TestProbeNetworkFailurePasses(t *testing.T) {
	def := capability.Server{Name: "x", Transport: "http", URL: "http://127.0.0.1:1/mcp", Headers: []capability.Pair{{Key: "A", Value: "b"}}}
	if p := New("").Server(context.Background(), "codex", "x", &def); p != nil {
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
	oauth := &capability.Server{Transport: "http", OAuth: true}
	for _, agent := range []string{"codex", "claude"} {
		c := New("")
		if p := c.Server(context.Background(), agent, "wiki", oauth); p == nil || p.Static || p.Command() != agent+" mcp login wiki" {
			t.Fatal(agent, p)
		}
		if p := c.Server(context.Background(), agent, "docs", oauth); p != nil {
			t.Fatal(agent, p)
		}
	}
	// A stdio server would have to start to be checked.
	if p := New("").Server(context.Background(), "codex", "wiki", &capability.Server{Transport: "stdio"}); p != nil {
		t.Fatal(p)
	}
}

func TestPassedCheckIsCached(t *testing.T) {
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"login status": {Stdout: "ok"}}})
	onlyPath(t, codex)
	dir := t.TempDir()
	c := New(dir).(*checker)
	if p := c.Agent(context.Background(), "codex"); p != nil {
		t.Fatal(p)
	}
	// The second check reads the cache, so a logged-out agent still passes
	// until the entry ages out.
	onlyPath(t, testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{"login status": {Exit: 1}}}))
	if p := c.Agent(context.Background(), "codex"); p != nil {
		t.Fatal(p)
	}
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Second) }
	if p := c.Agent(context.Background(), "codex"); p == nil {
		t.Fatal("stale cache used")
	}
}

func TestLogin(t *testing.T) {
	p := Problem{Agent: "codex", Server: "wiki"}
	cases := []struct {
		name string
		sc   testbin.Scenario
		want LoginResult
	}{
		{"done", testbin.Scenario{}, LoggedIn},
		{"closed", testbin.Scenario{Exit: 1, Stderr: "login cancelled\n"}, Declined},
		{"needs a terminal", testbin.Scenario{Exit: 1, Stderr: "Error: Raw mode is not supported on the current process.stdin\n"}, NeedsTerminal},
		{"timed out", testbin.Scenario{SleepMS: 5000}, TimedOut},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := testbin.Install(t, "codex", c.sc)
			onlyPath(t, fake)
			start := time.Now()
			if got := New("").Login(context.Background(), p, 500*time.Millisecond); got != c.want {
				t.Fatal(got)
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("login was not bounded")
			}
			if c.name == "done" {
				if args := string(must(os.ReadFile(fake + ".args.json"))); args != `["mcp","login","wiki"]` {
					t.Fatal(args)
				}
			}
		})
	}
	// A static credential has nothing to log in to.
	if got := New("").Login(context.Background(), Problem{Agent: "codex", Server: "x", Static: true}, time.Second); got != Declined {
		t.Fatal(got)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestFromNeedsAndNames(t *testing.T) {
	got := FromNeeds("claude", []string{"figma", "auth:mcp:jira", "auth:claude"})
	if len(got) != 2 || got[0].Server != "jira" || got[1].Server != "" || got[0].Need() != "mcp:jira" || got[1].Need() != "auth:claude" || got[0].AuthNeed() != "auth:mcp:jira" {
		t.Fatalf("%+v", got)
	}
	if (Problem{Agent: "claude", Server: "claude.ai Slack"}).Command() != "claude mcp login 'claude.ai Slack'" {
		t.Fatal((Problem{Agent: "claude", Server: "claude.ai Slack"}).Command())
	}
	if (Problem{Agent: "claude"}).Command() != "claude auth login" {
		t.Fatal()
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
	def := capability.Server{Name: "svc", Agent: "codex", Transport: "http", URL: srv.URL, Headers: []capability.Pair{{Key: "X-Token", Value: token}}}
	if p := New(t.TempDir()).Server(context.Background(), "claude", "svc", &def); p != nil || leaked != "" {
		t.Fatal(p, leaked == token)
	}
}

// The person pastes the login command into a shell, and a worker named the
// server, so every word other than plain characters is quoted.
func TestLoginCommandQuotesShellCharacters(t *testing.T) {
	got := (Problem{Agent: "claude", Server: "svc;printf${IFS}x|sh"}).Command()
	if got != `claude mcp login 'svc;printf${IFS}x|sh'` {
		t.Fatal(got)
	}
	if got := (Problem{Agent: "codex", Server: "it's"}).Command(); got != `codex mcp login 'it'\''s'` {
		t.Fatal(got)
	}
}
