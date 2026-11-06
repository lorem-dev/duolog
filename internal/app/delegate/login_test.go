package delegate

import (
	"context"
	"testing"
	"time"

	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// countingLogin is a Login that counts its checks and says the agent is logged
// in or not.
type countingLogin struct {
	loggedIn bool
	checks   int
}

func (l *countingLogin) Status(context.Context, corelaunch.Process) *Problem {
	l.checks++
	if l.loggedIn {
		return nil
	}
	return &Problem{Agent: "codex", Reason: "not logged in"}
}

func (l *countingLogin) Server(context.Context, corelaunch.Process, string) *Problem { return nil }

func (*countingLogin) LoginArgv(server string) []string {
	if server == "" {
		return []string{"codex", "login"}
	}
	return []string{"codex", "mcp", "login", server}
}

func TestPassedCheckIsCached(t *testing.T) {
	login := &countingLogin{loggedIn: true}
	d := Deps{Agents: map[string]Agent{"codex": {Login: login}}}
	c := NewChecker(d, t.TempDir(), corelaunch.Process{}).(*loginChecker)
	if p := c.Agent(context.Background(), "codex"); p != nil {
		t.Fatal(p)
	}
	// The second check reads the cache, so a logged-out agent still passes
	// until the entry ages out.
	login.loggedIn = false
	if p := c.Agent(context.Background(), "codex"); p != nil || login.checks != 1 {
		t.Fatal(p, login.checks)
	}
	c.now = func() time.Time { return time.Now().Add(cacheTTL + time.Second) }
	if p := c.Agent(context.Background(), "codex"); p == nil || login.checks != 2 {
		t.Fatal("stale cache used", p, login.checks)
	}
}

// A problem a check returns carries the agent's login command; a worker's own
// needs do not, and the run fills it in from the agent's Login.
func TestCheckerGivesTheLoginCommand(t *testing.T) {
	d := Deps{Agents: map[string]Agent{"codex": {Login: &countingLogin{}}}}
	p := NewChecker(d, "", corelaunch.Process{}).Agent(context.Background(), "codex")
	if p == nil || p.Command() != "codex login" {
		t.Fatalf("%+v", p)
	}
	if got := d.withLogin(Problem{Agent: "codex", Server: "wiki"}); got.Command() != "codex mcp login wiki" {
		t.Fatalf("%+v", got)
	}
	// A static credential has nothing to log in to.
	if got := d.withLogin(Problem{Agent: "codex", Server: "x", Static: true}); got.LoginArgv() != nil {
		t.Fatalf("%+v", got)
	}
}

func TestFromNeedsAndNames(t *testing.T) {
	got := fromNeeds("claude", []string{"figma", "auth:mcp:jira", "auth:claude"})
	if len(got) != 2 || got[0].Server != "jira" || got[1].Server != "" || got[0].Need() != "mcp:jira" || got[1].Need() != "auth:claude" || got[0].AuthNeed() != "auth:mcp:jira" {
		t.Fatalf("%+v", got)
	}
	if got := (Problem{Agent: "claude"}).Command(); got != "" {
		t.Fatalf("a problem without a login command shows %q", got)
	}
}

// The person pastes the login command into a shell, and a worker named the
// server, so every word other than plain characters is quoted.
func TestLoginCommandQuotesShellCharacters(t *testing.T) {
	cases := []struct {
		p    coreagent.LoginProblem
		want string
	}{
		{coreagent.LoginProblem{Agent: "claude", Server: "claude.ai Slack", Login: []string{"claude", "mcp", "login", "claude.ai Slack"}}, "claude mcp login 'claude.ai Slack'"},
		{coreagent.LoginProblem{Agent: "claude", Login: []string{"claude", "auth", "login"}}, "claude auth login"},
		{coreagent.LoginProblem{Agent: "claude", Server: "svc;printf${IFS}x|sh", Login: []string{"claude", "mcp", "login", "svc;printf${IFS}x|sh"}}, `claude mcp login 'svc;printf${IFS}x|sh'`},
		{coreagent.LoginProblem{Agent: "codex", Server: "it's", Login: []string{"codex", "mcp", "login", "it's"}}, `codex mcp login 'it'\''s'`},
	}
	for _, c := range cases {
		if got := c.p.Command(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.p, got, c.want)
		}
	}
}
