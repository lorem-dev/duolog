package claude

import (
	"slices"
	"testing"
)

// Claude adds the duolog marketplace, then installs the plugin with plugin
// install.
func TestPluginCommands(t *testing.T) {
	got := Installer{}.PluginCommands()
	if len(got) != 2 || !slices.Equal(got[0], []string{"plugin", "marketplace", "add", "lorem-dev/duolog"}) || !slices.Equal(got[1], []string{"plugin", "install", "duolog@duolog"}) {
		t.Fatal(got)
	}
}
