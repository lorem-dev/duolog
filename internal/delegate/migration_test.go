package delegate

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// unsetEnv removes name for one test and restores it afterwards.
func unsetEnv(t *testing.T, name string) {
	t.Helper()
	t.Setenv(name, "")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
}

// clearRoutes removes every variable that workerenv reads as a route for
// either agent, so that a test does not depend on the developer's shell. A
// test sets what it needs afterwards.
func clearRoutes(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name == "CLAUDE_CODE_OAUTH_TOKEN" || workerenv.RouteName("claude", name, "") || workerenv.RouteName("codex", name, "") {
			unsetEnv(t, name)
		}
	}
}

// The goldens must not inherit route variables from the shell.
func TestClearRoutes(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK"} {
		t.Setenv(name, "x")
	}
	clearRoutes(t)
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK"} {
		if _, set := os.LookupEnv(name); set {
			t.Errorf("%s is still set", name)
		}
	}
}

// A dropped key that was the worker's only login fails the login check. duolog
// starts no login, since a browser login would switch the person's billing,
// and the task goes to the lead with the migration line. A server that refuses
// its credentials still follows capabilities.on_auth_failure.
func TestDroppedOnlyLoginGoesToLeadWithoutLogin(t *testing.T) {
	const secret = "sk-test-value"
	line := workerenv.MigrationLine("codex", "OPENAI_API_KEY")
	keys := func(t *testing.T) *harness {
		h := authHarness(t)
		t.Setenv("OPENAI_API_KEY", secret)
		unsetEnv(t, "ANTHROPIC_API_KEY")
		h.cfg.Capabilities.OnAuthFailure = auth.LoginThenLead
		h.auth.login = auth.LoggedIn
		return h
	}
	check := func(t *testing.T, h *harness, o Outcome, e error, calls int) {
		t.Helper()
		h.requireCalls(calls)
		if e != nil || len(h.auth.logins) != 0 || len(o.Needs) != 1 || o.Needs[0] != "auth:codex" {
			t.Fatal(o, e, h.auth.logins)
		}
		if strings.Count(o.Line, line) != 1 || strings.Contains(o.Line, "! codex login") || strings.Contains(o.Line, secret) {
			t.Fatal(o.Line)
		}
		// The way back is the paid_runs opt-in, no longer env_passthrough.
		if !strings.HasSuffix(o.Line, " Do the task here, or allow paid runs in paid_runs.codex and delegate again.") || strings.Contains(o.Line, "env_passthrough") {
			t.Fatal(o.Line)
		}
	}

	t.Run("preflight", func(t *testing.T) {
		h := keys(t)
		h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: "not logged in"}}
		o, e := h.run("implement")
		check(t, h, o, e, 0)
		// The notice was given, but the login still fails: the line says why again.
		o, e = h.run("implement")
		check(t, h, o, e, 0)
	})

	t.Run("worker auth retry", func(t *testing.T) {
		h := keys(t)
		h.results = []runner.Result{{AuthFailed: true}}
		o, e := h.run("implement")
		check(t, h, o, e, 1)
	})

	t.Run("unrelated mcp failure", func(t *testing.T) {
		h := keys(t)
		h.inventories(capability.Inventory{Agent: "codex", Items: []capability.Item{{Kind: "mcp", Name: "wiki"}}}, capability.Inventory{Agent: "claude"})
		h.auth.server = map[string]*auth.Problem{"wiki": {Agent: "codex", Server: "wiki", Reason: "codex has no OAuth login for it"}}
		o, e := h.runNeeds("implement", "mcp:wiki")
		h.requireCalls(1)
		if e != nil || len(h.auth.logins) != 1 || h.auth.logins[0].Command() != "codex mcp login wiki" || !strings.HasPrefix(o.Line, "done=true") {
			t.Fatal(o, e, h.auth.logins)
		}
		// The first launch of codex in this lead carries the notice, the next not.
		if strings.Count(o.Line, line) != 1 || strings.Contains(o.Line, secret) {
			t.Fatal(o.Line)
		}
		if o, e = h.run("implement"); e != nil || strings.Contains(o.Line, line) {
			t.Fatal(o.Line, e)
		}
	})

	// A key listed in env_passthrough without paid runs is still dropped, so
	// the first launch says why it did not reach the worker.
	t.Run("listed key", func(t *testing.T) {
		h := keys(t)
		h.cfg.EnvPassthrough = append(h.cfg.EnvPassthrough, "OPENAI_API_KEY")
		o, e := h.run("implement")
		h.requireCalls(1)
		if e != nil || !strings.HasPrefix(o.Line, "done=true") || strings.Count(o.Line, line) != 1 {
			t.Fatal(o.Line, e)
		}
	})

	// Paid runs allowed with the key in their env: the person followed the
	// advice, so the first launch carries no line.
	t.Run("allowed key", func(t *testing.T) {
		h := keys(t)
		h.cfg.PaidRuns.Codex.Allow, h.cfg.PaidRuns.Codex.DailyCapUSD = true, 5
		h.cfg.PaidRuns.Codex.Env = []string{"OPENAI_API_KEY"}
		o, e := h.run("implement")
		h.requireCalls(1)
		if e != nil || !strings.HasPrefix(o.Line, "done=true") || strings.Contains(o.Line, "no longer get") {
			t.Fatal(o.Line, e)
		}
	})
}

// Notices follow the line with "; ", except after one that ends in a full
// stop, which the next notice follows with a space: ".;" never appears.
func TestJoinNotices(t *testing.T) {
	migration := workerenv.MigrationLine("codex", "OPENAI_API_KEY")
	overage := "codex worker ran on paid overage; duolog stopped further runs"
	if !strings.HasSuffix(migration, ".") {
		t.Fatalf("the migration line no longer ends in a full stop: %q", migration)
	}
	for _, c := range []struct {
		notices []string
		want    string
	}{
		{nil, ""},
		{[]string{migration}, "; " + migration},
		{[]string{overage}, "; " + overage},
		{[]string{migration, overage}, "; " + migration + " " + overage},
		{[]string{overage, migration}, "; " + overage + "; " + migration},
	} {
		got := joinNotices(c.notices)
		if got != c.want || strings.Contains(got, ".;") {
			t.Errorf("joinNotices(%q) = %q, want %q", c.notices, got, c.want)
		}
	}
}

// A claimed migration line does not come again in this lead, so a line that
// is too long is cut before it and the line follows whole, however the run
// ends.
func TestClaimedNoticeSurvivesALongLine(t *testing.T) {
	line := workerenv.MigrationLine("codex", "OPENAI_API_KEY")
	keys := func(t *testing.T) *harness {
		h := authHarness(t)
		t.Setenv("OPENAI_API_KEY", "sk-test-value")
		unsetEnv(t, "ANTHROPIC_API_KEY")
		return h
	}
	check := func(t *testing.T, o Outcome) {
		t.Helper()
		if len(o.Line) > maxLine || strings.Count(o.Line, line) != 1 || !strings.HasSuffix(o.Line, line) || !strings.Contains(o.Line, "[cut; ") {
			t.Fatalf("%d bytes: %s", len(o.Line), o.Line)
		}
	}
	long := strings.TrimSpace(strings.Repeat("refused ", 80))

	t.Run("files list", func(t *testing.T) {
		h := keys(t)
		var files []string
		for i := range 10 {
			files = append(files, fmt.Sprintf("src/%d%s.go", i, strings.Repeat("x", 35)))
		}
		h.results = []runner.Result{{Done: true, Summary: "Completed.", Files: files}}
		o, e := h.run("implement")
		h.requireCalls(1)
		if e != nil || !strings.HasPrefix(o.Line, "done=true summary=") || !strings.Contains(o.Line, "files=src/0") {
			t.Fatal(o.Line, e)
		}
		check(t, o)
		// The next command of this lead does not repeat it.
		if o, e = h.run("implement"); e != nil || !strings.HasPrefix(o.Line, "done=true") || strings.Contains(o.Line, "no longer get") {
			t.Fatal(o.Line, e)
		}
	})

	t.Run("worker error", func(t *testing.T) {
		h := keys(t)
		h.errs = []error{errors.New(long)}
		o, e := h.run("implement")
		if e == nil {
			t.Fatal(o.Line)
		}
		check(t, o)
	})

	t.Run("refused login fails", func(t *testing.T) {
		h := keys(t)
		h.cfg.Capabilities.OnAuthFailure = auth.Fail
		h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: long}}
		o, e := h.run("implement")
		h.requireCalls(0)
		if e == nil || !strings.HasPrefix(o.Line, "role implement: codex login: refused") {
			t.Fatal(o.Line, e)
		}
		check(t, o)
	})

	t.Run("refused login goes to lead", func(t *testing.T) {
		h := keys(t)
		h.cfg.Capabilities.OnAuthFailure = auth.LoginThenLead
		h.auth.agent = map[string]*auth.Problem{"codex": {Agent: "codex", Reason: long}}
		o, e := h.run("implement")
		h.requireCalls(0)
		if e != nil || len(h.auth.logins) != 0 || len(o.Needs) != 1 || !strings.HasPrefix(o.Line, "role implement waits for lead: codex login: refused") {
			t.Fatal(o.Line, e)
		}
		check(t, o)
	})
}
