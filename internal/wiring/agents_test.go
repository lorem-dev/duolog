package wiring

import (
	"reflect"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

// TestBuiltinAgentsMatchRegistry: the stand-in that Load uses without options
// agrees with the registry's descriptors on the facts Load reads.
func TestBuiltinAgentsMatchRegistry(t *testing.T) {
	builtin, registry := configfile.BuiltinAgents(), Descriptors()
	if len(builtin) != len(registry) {
		t.Fatalf("BuiltinAgents has %d agents, the registry %d", len(builtin), len(registry))
	}
	for i, b := range builtin {
		r := registry[i]
		if b.Name != r.Name || b.Config.SubscriptionEndpoints != r.Config.SubscriptionEndpoints || b.Config.SkillsDir != r.Config.SkillsDir {
			t.Errorf("agent %d: builtin %q (subscription endpoints %t, skills dir %q), registry %q (%t, %q)",
				i, b.Name, b.Config.SubscriptionEndpoints, b.Config.SkillsDir, r.Name, r.Config.SubscriptionEndpoints, r.Config.SkillsDir)
		}
	}
}

func TestRegistry(t *testing.T) {
	names := Names()
	if want := []string{"claude", "codex"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("Names() = %v, want %v", names, want)
	}
	agents := Agents()
	if len(agents) != len(names) {
		t.Fatalf("Agents() has %d entries, Names() has %d", len(agents), len(names))
	}
	for i, a := range agents {
		if a.Facts.Name != names[i] {
			t.Errorf("Agents()[%d].Facts.Name = %q, want %q", i, a.Facts.Name, names[i])
		}
	}
}
