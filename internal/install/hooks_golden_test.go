package install

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from its golden\n got: %q\nwant: %q", name, got, want)
	}
}

// The hooks document holds both Codex shell forms in every file, so the
// golden is the same on every OS.
func TestHooksJSONGolden(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			data, err := HooksJSON(agent, 150)
			if err != nil {
				t.Fatal(err)
			}
			checkGolden(t, agent+"-all-hooks", data)
		})
	}
}

// Each golden records the POSIX and the Windows command, on every OS.
func TestCodexGateCommandsGolden(t *testing.T) {
	for _, event := range []string{"PreToolUse", "PermissionRequest"} {
		t.Run(event, func(t *testing.T) {
			posix, windows := CodexGateCommands(event)
			checkGolden(t, "codex-"+event+"-commands", []byte("posix:\n"+posix+"\nwindows:\n"+windows+"\n"))
		})
	}
}
