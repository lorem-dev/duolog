package dev_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeBuild = "#!/bin/sh\necho \"$1 from $0\"\n"

func run(t *testing.T, dir string, env []string, name string, args ...string) (string, string, int) {
	t.Helper()
	// A chain of fallbacks that never ends must fail the test, not hang it.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s %v did not end: %s", name, args, stderr.String())
	}
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s %v: %v", name, args, err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// pathWithoutDuolog is PATH minus every folder that holds a duolog, so that a
// duolog installed on the host, or the hooks' PATH entry for this checkout,
// cannot answer for the fallback under test.
func pathWithoutDuolog() string {
	var keep []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "duolog")); err == nil {
			continue
		}
		keep = append(keep, dir)
	}
	return strings.Join(keep, string(os.PathListSeparator))
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// fixture makes a temporary repository with a package that does not compile,
// the launcher committed in it, and a linked worktree. It returns the main
// checkout, the worktree and the environment for running the launcher.
func fixture(t *testing.T) (string, string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the POSIX launcher is tested on POSIX hosts")
	}
	for _, tool := range []string{"git", "go", "sh"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	script, err := os.ReadFile(filepath.Join("bin", "duolog"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	home := filepath.Join(base, "home")
	main := filepath.Join(base, "main")
	wt := filepath.Join(base, "wt")
	write(t, filepath.Join(home, ".keep"), "", 0o644)
	write(t, filepath.Join(main, "go.mod"), "module example.test/dup\n\ngo 1.21\n", 0o644)
	write(t, filepath.Join(main, "cmd", "duolog", "main.go"), "package main\n\nfunc main() { this does not compile }\n", 0o644)
	write(t, filepath.Join(main, "scripts", "dev", "bin", "duolog"), string(script), 0o755)
	write(t, filepath.Join(main, ".gitignore"), ".cache/\n", 0o644)
	env := []string{
		"HOME=" + home,
		"PATH=" + pathWithoutDuolog(),
		"GOCACHE=" + filepath.Join(base, "gocache"),
		"GOFLAGS=-mod=mod",
		"GOPATH=" + filepath.Join(base, "gopath"),
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.test",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.test",
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
		{"worktree", "add", "-q", wt},
	} {
		if _, stderr, code := run(t, main, env, "git", args...); code != 0 {
			t.Fatalf("git %v: %s", args, stderr)
		}
	}
	return main, wt, env
}

func launcher(wt string) string {
	return filepath.Join(wt, "scripts", "dev", "bin", "duolog")
}

func TestLauncherFallsBackToMainCheckoutBuild(t *testing.T) {
	main, wt, env := fixture(t)
	write(t, filepath.Join(main, ".cache", "dev", "duolog"), fakeBuild, 0o755)

	stdout, stderr, code := run(t, wt, append(env, "DUOLOG_DEPTH=1"), launcher(wt), "version")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, filepath.Join(".cache", "dev", "duolog")) || !strings.HasPrefix(stdout, "version from ") {
		t.Errorf("stdout = %q, want the main checkout build", stdout)
	}
	if !strings.Contains(stdout, filepath.Base(main)) {
		t.Errorf("stdout = %q, want the build under %s", stdout, main)
	}
	if !strings.Contains(stderr, "main checkout") {
		t.Errorf("stderr = %q, want a note on the main checkout build", stderr)
	}
}

func TestLauncherFallsBackToInstalledDuolog(t *testing.T) {
	_, wt, env := fixture(t)
	installed := filepath.Join(t.TempDir(), "installed")
	write(t, filepath.Join(installed, "duolog"), fakeBuild, 0o755)
	// This launcher's own folder is first on PATH, as in a Claude session:
	// it must not be picked as the installed duolog.
	path := filepath.Join(wt, "scripts", "dev", "bin") + ":" + installed + ":" + pathWithoutDuolog()
	env = append(env, "PATH="+path, "DUOLOG_DEPTH=1")

	stdout, stderr, code := run(t, wt, env, launcher(wt), "version")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "version from "+filepath.Join(installed, "duolog")) {
		t.Errorf("stdout = %q, want the installed duolog", stdout)
	}
	if !strings.Contains(stderr, "installed duolog") {
		t.Errorf("stderr = %q, want a note on the installed duolog", stderr)
	}
}

func TestLauncherPrefersMainCheckoutOverInstalled(t *testing.T) {
	main, wt, env := fixture(t)
	write(t, filepath.Join(main, ".cache", "dev", "duolog"), fakeBuild, 0o755)
	installed := filepath.Join(t.TempDir(), "installed")
	write(t, filepath.Join(installed, "duolog"), fakeBuild, 0o755)
	env = append(env, "PATH="+installed+":"+pathWithoutDuolog())

	stdout, _, code := run(t, wt, env, launcher(wt), "version")
	if code != 0 || !strings.Contains(stdout, main) {
		t.Errorf("exit %d, stdout = %q, want the main checkout build", code, stdout)
	}
}

func TestLauncherFailsClosedWithoutFallback(t *testing.T) {
	_, wt, env := fixture(t)
	for _, tc := range []struct {
		depth string
		want  int
	}{{"1", 2}, {"0", 1}} {
		_, stderr, code := run(t, wt, append(env, "DUOLOG_DEPTH="+tc.depth), launcher(wt), "version")
		if code != tc.want {
			t.Errorf("depth %s: exit %d, want %d, stderr: %s", tc.depth, code, tc.want, stderr)
		}
		if !strings.Contains(stderr, "there is no earlier build") {
			t.Errorf("depth %s: stderr = %q", tc.depth, stderr)
		}
	}
}

// Two worktrees that do not build, both launchers on PATH: neither takes the
// other's launcher for an installed duolog, so the worker stops with exit 2
// instead of the two handing the run to each other.
func TestLauncherSkipsOtherCheckoutLaunchers(t *testing.T) {
	main, wt, env := fixture(t)
	wt2 := filepath.Join(filepath.Dir(wt), "wt2")
	if _, stderr, code := run(t, main, env, "git", "worktree", "add", "-q", wt2); code != 0 {
		t.Fatalf("git worktree add: %s", stderr)
	}
	bins := filepath.Dir(launcher(wt2)) + ":" + filepath.Dir(launcher(wt))
	env = append(env, "PATH="+bins+":"+pathWithoutDuolog(), "DUOLOG_DEPTH=1")
	for _, tree := range []string{wt, wt2} {
		_, stderr, code := run(t, tree, env, launcher(tree), "version")
		if code != 2 {
			t.Errorf("%s: exit %d, want 2, stderr: %s", tree, code, stderr)
		}
		if strings.Contains(stderr, "running the installed duolog") {
			t.Errorf("%s: took a launcher for an installed duolog: %s", tree, stderr)
		}
	}
}

// An installed duolog that leads back to a launcher already tried, such as a
// wrapper that runs a checkout's launcher, does not start the chain again.
func TestLauncherRunsNoFallbackTwice(t *testing.T) {
	_, wt, env := fixture(t)
	installed := filepath.Join(t.TempDir(), "installed")
	write(t, filepath.Join(installed, "duolog"), "#!/bin/sh\nexec "+launcher(wt)+" \"$@\"\n", 0o755)
	env = append(env, "PATH="+installed+":"+pathWithoutDuolog(), "DUOLOG_DEPTH=1")

	_, stderr, code := run(t, wt, env, launcher(wt), "version")
	if code != 2 {
		t.Errorf("exit %d, want 2, stderr: %s", code, stderr)
	}
	if strings.Count(stderr, "running the installed duolog") != 1 || !strings.Contains(stderr, "already ran in this chain") {
		t.Errorf("stderr = %q, want one fallback and then a stop", stderr)
	}
}

func TestLauncherPrefersEarlierWorktreeBuild(t *testing.T) {
	main, wt, env := fixture(t)
	write(t, filepath.Join(main, ".cache", "dev", "duolog"), fakeBuild, 0o755)
	write(t, filepath.Join(wt, ".cache", "dev", "duolog"), "#!/bin/sh\necho worktree build\n", 0o755)

	stdout, _, code := run(t, wt, env, launcher(wt), "version")
	if code != 0 || strings.TrimSpace(stdout) != "worktree build" {
		t.Errorf("exit %d, stdout = %q, want the worktree build", code, stdout)
	}
}
