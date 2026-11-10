package agentsync

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSyncDetectsPermissionOnlyChanges(t *testing.T) {
	// Kills returning early on an equal content hash despite changed permissions.
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX executable bit")
	}
	root := skillProject(t)
	source := filepath.Join(root, ".claude/skills/probe/scripts/a.sh")
	target := filepath.Join(root, ".agents/skills/probe/scripts/a.sh")
	for _, mode := range []os.FileMode{0600, 0700, 0600} {
		if err := os.Chmod(source, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(target); err == nil {
			rep, err := runWith(root, syncCfg(t), Options{From: "claude", Check: true})
			if err == nil && len(rep.Written) == 0 {
				t.Error("check missed permission change")
			}
		}
		if _, err := runWith(root, syncCfg(t), Options{From: "claude"}); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("mode %o want %o", info.Mode().Perm(), mode)
		}
	}
}
