package policy

import (
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
)

// A path that ends in claude or codex, given to a program that never runs its
// operands, is not a start of the agent. Unknown programs still fail closed.
func TestAgentNamedPathIsNotLaunch(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	cfg, err := configfile.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eval := func(command string) Decision {
		return Evaluate(cfg.Policy, LeadRules{}, mode.Full, Action{Kind: "exec", Target: command, Role: "implement"})
	}
	for _, command := range []string{
		"go test ./internal/adapter/claude", "go test ./internal/adapter/claude/", "go test ./internal/adapter/claude | head",
		"go vet ./internal/adapter/codex", "ls internal/adapter/claude", "git add internal/adapter/claude/pane.go",
		// Not the last operand, or after an option: still a package path.
		"go test ./internal/adapter/claude/ ./internal/core/policy/", "go test ./claude ./pkg", "go test -v ./claude",
		"go test -count=1 ./internal/adapter/claude/... ./cmd/duolog/", "go vet ./internal/adapter/codex ./internal/adapter/claude",
	} {
		if d := eval(command); d.Kind == Deny {
			t.Errorf("%q denied: %+v", command, d)
		}
	}
	for _, command := range []string{
		"foo claude", "nohup ./claude", "xargs claude", "go run ./cmd/claude", "npx claude", "uv run claude", "echo $(claude)",
		"go test $(claude)", "env X=1 claude", "go test ./pkg -exec ./claude", "go test -exec ./claude ./pkg",
		"bat f --pager claude", "git grep -O claude", "less claude",
		"go test ./pkg $(claude)", "go test -v $(claude) ./pkg", "go test -count=1 `claude` ./pkg", "go test -exec=./claude ./pkg",
	} {
		if d := eval(command); d.Kind != Deny {
			t.Errorf("%q: %+v", command, d)
		}
	}
}
