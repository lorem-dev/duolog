package kit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunChecksUsesArgumentsAndWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := []string{"git rev-parse --is-inside-work-tree", "git diff --exit-code -- base.txt", "git no-such-subcommand", "no-such-program-x", "", "git --version ; no-such-program-x"}
	for i, want := range []int{0, 0, 1, -1, -1, 0} {
		if got := RunCheck(context.Background(), root, commands[i]); got != want {
			t.Fatalf("check %d %q: exit %d, want %d", i, commands[i], got, want)
		}
	}
}

// A check that starts its own child, as go test and npm test do, is stopped
// with that child once ctx ends.
func TestRunChecksStopsWithContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hang.sh"), []byte("sleep 30 &\necho $! > child.pid\nwait\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	started := time.Now()
	exit := RunCheck(ctx, dir, "sh hang.sh")
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("RunCheck took %s after its context ended", elapsed)
	}
	if exit == 0 {
		t.Fatalf("exit %d, want a failure", exit)
	}
	data, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil {
		if time.Now().After(deadline) {
			_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
			t.Fatalf("the check's child %d still runs", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
