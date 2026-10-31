package trust

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestTrustWithShortPaths(t *testing.T) {
	root := t.TempDir()
	long, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	short := testbin.ShortPath(t, root)
	for _, paths := range [][2]string{{long, short}, {short, long}} {
		key := filepath.Join(paths[0], ".codex", "hooks.json") + ":PreToolUse:0:0"
		fixture(t, fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"reviewed\"\n", paths[0], key))
		writeHook(t, root, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`)
		if ok, err := Folder("codex", paths[1]); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if state, err := Hooks("codex", paths[1]); err != nil || !state.Trusted {
			t.Fatal(state, err)
		}
		if state, err := Hooks("codex", t.TempDir()); err != nil || state.Trusted {
			t.Fatal("trusted unrelated project", state, err)
		}
	}
}
