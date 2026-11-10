package delegate

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/paidruns"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

// A route that moved during admission gets its login checked by the real
// checker, as it does when no test replaces it. Giving a worktree Codex's
// trust moves the route, because the process's cache key holds the config
// file's state. Mutation killed: call the Deps.NewChecker field, nil outside
// tests, which panicked on the first delegate in a new worktree.
func TestAMovedRouteIsCheckedByTheRealChecker(t *testing.T) {
	t.Parallel()
	login := &countingLogin{loggedIn: true}
	d := Deps{Agents: map[string]Agent{"codex": {Login: login}}}
	a := &permitRun{permit: paidruns.Permit{Process: corelaunch.Process{Agent: "codex", CacheKey: "after"}}}
	if err := a.sameRoute(context.Background(), d, corelaunch.Process{CacheKey: "before"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if login.checks != 1 {
		t.Fatalf("login checks = %d, want the moved route checked once", login.checks)
	}
}
