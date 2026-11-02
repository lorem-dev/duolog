package delegate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/runner"
)

// fakeChecker answers the login checks of a test. A problem stays until a
// login with result LoggedIn clears it.
type fakeChecker struct {
	agent  map[string]*auth.Problem
	server map[string]*auth.Problem
	login  auth.LoginResult
	logins []auth.Problem
}

func (f *fakeChecker) Agent(_ context.Context, agent string) *auth.Problem { return f.agent[agent] }
func (f *fakeChecker) Server(_ context.Context, agent, name string, def *capability.Server) *auth.Problem {
	return f.server[name]
}
func (f *fakeChecker) Login(_ context.Context, p auth.Problem, _ time.Duration) auth.LoginResult {
	f.logins = append(f.logins, p)
	if f.login == auth.LoggedIn {
		delete(f.agent, p.Agent)
		delete(f.server, p.Server)
	}
	return f.login
}
func (f *fakeChecker) Forget(auth.Problem) {}

func authHarness(t *testing.T) *harness {
	h := newHarness(t)
	h.lead("claude", mode.Edits, "high")
	return h
}

func TestLoginThenWorkerRuns(t *testing.T) {
	h := authHarness(t)
	h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: "not logged in"}}
	h.auth.login = auth.LoggedIn
	o, e := h.run("implement")
	h.requireCalls(1)
	if e != nil || len(h.auth.logins) != 1 || h.auth.logins[0].Command() != "codex login" || !strings.HasPrefix(o.Line, "done=true") {
		t.Fatal(o, e, h.auth.logins)
	}
}

func TestDeclinedLoginGoesToLeadOnce(t *testing.T) {
	for _, result := range []auth.LoginResult{auth.Declined} {
		h := authHarness(t)
		h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "wiki"}}}, capability.Inventory{Agent: "claude"})
		h.auth.server = map[string]*auth.Problem{"wiki": {Agent: "codex", Server: "wiki", Reason: "codex has no OAuth login for it"}}
		h.auth.login = result
		o, e := h.runNeeds("implement", "mcp:wiki")
		h.requireCalls(0)
		if e != nil || len(o.Needs) != 1 || o.Needs[0] != "mcp:wiki" || !strings.Contains(o.Line, "closed, timed out or failed") || !strings.Contains(o.Line, "! codex mcp login wiki") {
			t.Fatal(o, e)
		}
		// The person said no: the next delegation of this lead does not ask again.
		o, e = h.runNeeds("implement", "mcp:wiki")
		if e != nil || len(h.auth.logins) != 1 || !strings.Contains(o.Line, "declined earlier") {
			t.Fatal(o, e, h.auth.logins)
		}
		// A new lead session may ask again.
		if declined, _ := h.reg.AuthDeclined("other-lead", h.auth.logins[0].Key()); declined {
			t.Fatal("declined for every lead")
		}
	}
}

func TestLoginNeedsTerminal(t *testing.T) {
	h := authHarness(t)
	h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: "not logged in"}}
	h.auth.login = auth.NeedsTerminal
	o, e := h.run("implement")
	h.requireCalls(0)
	if e != nil || len(o.Needs) != 1 || o.Needs[0] != "auth:codex" || !strings.Contains(o.Line, "Ask the person to run: ! codex login") {
		t.Fatal(o, e)
	}
}

func TestStaticTokenRejectedGoesToLead(t *testing.T) {
	h := authHarness(t)
	h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "jira"}}}, capability.Inventory{Agent: "claude"})
	h.auth.server = map[string]*auth.Problem{"jira": {Agent: "codex", Server: "jira", Static: true, Where: "config.toml [mcp_servers.jira]", Reason: "the server rejected the token from config (HTTP 401)"}}
	o, e := h.runNeeds("implement", "mcp:jira")
	h.requireCalls(0)
	// Nothing to log in to: no login runs, and the line names the entry.
	if e != nil || len(h.auth.logins) != 0 || len(o.Needs) != 1 || o.Needs[0] != "mcp:jira" || !strings.Contains(o.Line, "fix the credential in config.toml [mcp_servers.jira]") || strings.Contains(o.Line, "! ") {
		t.Fatal(o, e)
	}
}

func TestAuthFailureActions(t *testing.T) {
	h := authHarness(t)
	h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: "not logged in"}}
	h.cfg.Capabilities.OnAuthFailure = auth.Lead
	o, e := h.run("implement")
	if e != nil || len(h.auth.logins) != 0 || o.Needs[0] != "auth:codex" || !strings.Contains(o.Line, "to log in, run: ! codex login") {
		t.Fatal(o, e)
	}
	h.cfg.Capabilities.OnAuthFailure = auth.Fail
	if _, e = h.run("implement"); e == nil || !strings.Contains(e.Error(), "codex login: not logged in") {
		t.Fatal(e)
	}
	h.requireCalls(0)
}

// A rejection that shows only during the run gets the same treatment, and a
// login that works runs the worker once more.
func TestRunTimeRejection(t *testing.T) {
	h := authHarness(t)
	h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "wiki"}}}, capability.Inventory{Agent: "claude"})
	h.results = []runner.Result{{Done: false, Summary: "wiki said 401", Files: []string{}, Needs: []string{"auth:mcp:wiki"}}}
	h.auth.login = auth.LoggedIn
	o, e := h.runNeeds("implement", "mcp:wiki")
	h.requireCalls(2)
	if e != nil || len(h.auth.logins) != 1 || h.auth.logins[0].Command() != "codex mcp login wiki" || !strings.HasPrefix(o.Line, "done=true") {
		t.Fatal(o, e)
	}
	// The worker was told how to report a rejected login.
	if !strings.Contains(string(h.calls[0].Stdin), "auth:mcp:<server>") {
		t.Fatal("worker line missing")
	}
}

func TestRunTimeAgentFailureWithStaticBridgedToken(t *testing.T) {
	h := bridgeHarness(t)
	h.results = []runner.Result{{Done: false, Summary: "jira said 401", Files: []string{}, Needs: []string{"auth:mcp:jira"}}}
	o, e := h.runNeeds("implement", "mcp:jira")
	h.requireCalls(1)
	if e != nil || len(h.auth.logins) != 0 || len(o.Needs) != 1 || o.Needs[0] != "mcp:jira" || !strings.Contains(o.Line, ".claude.json mcpServers") || strings.Contains(o.Line, leadToken) {
		t.Fatal(o, e)
	}
	// The agent refusing its own login during the run is classified too.
	h.results = []runner.Result{{AuthFailed: true}}
	h.calls = nil
	h.auth.login = auth.Declined
	o, e = h.run("implement")
	if e != nil || o.Needs[0] != "auth:codex" || len(h.auth.logins) != 1 {
		t.Fatal(o, e)
	}
	filepath.Walk(h.s.Dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(read(t, path), leadToken) {
			t.Fatalf("token in %s", path)
		}
		return nil
	})
}
