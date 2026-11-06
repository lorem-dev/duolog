package claude

import (
	"context"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

func TestResolveClaudeAliasUnchanged(t *testing.T) {
	for _, name := range []string{"opus", "claude-opus-5-5"} {
		got, err := Resolver{}.Resolve(context.Background(), &config.Config{}, "", config.Target{Agent: "claude", Model: name})
		if err != nil || got != name {
			t.Fatalf("got %q, %v", got, err)
		}
	}
}
