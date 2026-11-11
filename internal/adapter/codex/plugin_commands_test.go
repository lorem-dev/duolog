package codex

import (
	"slices"
	"testing"
)

// Codex adds the duolog marketplace, then adds the plugin with plugin add.
func TestPluginCommands(t *testing.T) {
	got := Installer{}.PluginCommands()
	if len(got) != 2 || !slices.Equal(got[0], []string{"plugin", "marketplace", "add", "lorem-dev/duolog"}) || !slices.Equal(got[1], []string{"plugin", "add", "duolog@duolog"}) {
		t.Fatal(got)
	}
}
