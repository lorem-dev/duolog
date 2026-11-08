package wiring

import (
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
)

// TestPaidGateKeepsReadsInTheAccountFolder: each duolog command opens its own
// gate, so a good read reaches the next command only through this folder.
func TestPaidGateKeepsReadsInTheAccountFolder(t *testing.T) {
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "AppData"} {
		t.Setenv(key, home)
	}
	gate, err := paidGate(&config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if gate.Cache.Dir == "" || gate.Cache.Dir != gate.Store.Dir {
		t.Fatalf("cache folder %q, account folder %q", gate.Cache.Dir, gate.Store.Dir)
	}
}
