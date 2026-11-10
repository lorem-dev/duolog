package install

import (
	"github.com/lorem-dev/duolog/internal/store"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMergeNullHooks(t *testing.T) {
	// Mutation killed: writing into the nil map decoded from hooks:null panics.
	got, err := Merge([]byte(`{"hooks":null}`), []byte(gateHook(`,"timeout":630`)))
	if err != nil || len(got) == 0 {
		t.Fatalf("%s %v", got, err)
	}
}

func TestForeignDuologTextIsPreserved(t *testing.T) {
	// Mutation killed: substring ownership deletes foreign logging and comments.
	for _, command := range []string{`logger 'duolog hook failed'`, `echo "duolog gate denied"`, `# duolog gate --agent claude`} {
		if IsOurCommand(command) {
			t.Errorf("foreign command claimed: %q", command)
		}
	}
}

// Mutation killed: dropping the dev launcher forms makes install add a
// second set of hooks beside this repository's own and loses their
// exemption in install checks.
func TestDevLauncherHooksAreOurs(t *testing.T) {
	for _, command := range []string{
		`r=$(git rev-parse --show-toplevel 2>/dev/null); if [ -x "$r/scripts/dev/bin/duolog" ]; then exec "$r"/scripts/dev/bin/duolog gate --agent codex --event PreToolUse; elif [ "${DUOLOG_DEPTH:-0}" -ge 1 ] 2>/dev/null; then exit 2; fi`,
		`powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/dev/bin/duolog.ps1 hook Stop --agent codex`,
		`exec "$CLAUDE_PROJECT_DIR"/scripts/dev/bin/duolog hook SessionStart --agent claude`,
	} {
		if !IsOurCommand(command) {
			t.Errorf("dev hook not claimed: %q", command)
		}
	}
	if IsOurCommand(`powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/dev/bin/duolog.ps1 version`) {
		t.Error("a dev launcher call that is no hook claimed")
	}
}

func TestStaleApprovalsWaitForSharedLock(t *testing.T) {
	// Mutation killed: unlocked read-modify-write can lose another project's markers.
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.LockFile(filepath.Join(dir, staleApprovalsFile+".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	done := make(chan error, 1)
	hooks := filepath.Join(t.TempDir(), "hooks.json")
	go func() { done <- MarkStaleApprovals(home, hooks) }()
	select {
	case err := <-done:
		t.Fatalf("ignored lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("lock not released")
	}
}
