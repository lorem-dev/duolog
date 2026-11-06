package dev_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeBuild = "#!/bin/sh\necho \"$1 from $0\"\n"

// TestMain lets the test binary stand in for a duolog build: the Windows
// launcher tests have a fake go copy it as the build, then run it.
func TestMain(m *testing.M) {
	if os.Getenv("DUOLOG_TEST_FAKE_BUILD") == "1" {
		fmt.Println(strings.Join(append([]string{"fake build"}, os.Args[1:]...), " "))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

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
	names := []string{"duolog"}
	if runtime.GOOS == "windows" {
		for _, ext := range filepath.SplitList(os.Getenv("PATHEXT")) {
			names = append(names, "duolog"+strings.ToLower(ext))
		}
	}
	var keep []string
dirs:
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, name := range names {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				continue dirs
			}
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

// A fake go that prints a download line on stderr, as a real first build
// does, then succeeds by copying the test binary as the build or fails.
const (
	fakeGoBuilds = "@echo off\r\n" +
		"1>&2 echo go: downloading example.test/dep v1.0.0\r\n" +
		"copy /y \"%DUOLOG_TEST_EXE%\" \"%~3\" >nul\r\n" +
		"exit /b %errorlevel%\r\n"
	fakeGoFails = "@echo off\r\n" +
		"1>&2 echo go: downloading example.test/dep v1.0.0\r\n" +
		"1>&2 echo cmd\\duolog\\main.go:3:15: syntax error\r\n" +
		"exit /b 1\r\n"
	// git outside a repository fails with a line on stderr.
	fakeGit = "@echo off\r\n" +
		"1>&2 echo fatal: not a git repository (or any of the parent directories): .git\r\n" +
		"exit /b 128\r\n"
)

// windowsFixture lays out a checkout with the PowerShell launcher, puts a
// fake go and git first on PATH and no duolog anywhere on it, and returns the
// checkout, the environment and Windows PowerShell 5.1, which Codex runs.
func windowsFixture(t *testing.T, fakeGo string) (string, []string, string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the PowerShell launcher is tested on Windows")
	}
	shell, err := exec.LookPath("powershell")
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skip("Windows PowerShell is not installed")
		}
		t.Fatal("CI must provide Windows PowerShell 5.1:", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("bin", "duolog.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	fake := filepath.Join(base, "fake")
	write(t, filepath.Join(root, "scripts", "dev", "bin", "duolog.ps1"), string(script), 0o644)
	write(t, filepath.Join(fake, "go.cmd"), fakeGo, 0o755)
	write(t, filepath.Join(fake, "git.cmd"), fakeGit, 0o755)
	env := append(os.Environ(),
		"PATH="+fake+string(os.PathListSeparator)+pathWithoutDuolog(),
		"DUOLOG_DEV_TRIED=",
		"DUOLOG_TEST_EXE="+exe,
		"DUOLOG_TEST_FAKE_BUILD=1",
	)
	return root, env, shell
}

func runPowerShellLauncher(t *testing.T, root string, env []string, shell, depth string) (string, string, int) {
	t.Helper()
	script := filepath.Join(root, "scripts", "dev", "bin", "duolog.ps1")
	return run(t, root, append(env, "DUOLOG_DEPTH="+depth), shell,
		"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script, "version")
}

// Windows PowerShell 5.1 with 'Stop' turns a redirected stderr line into a
// terminating error. A build that only says "go: downloading" must still be
// installed and run, and its output must stay off the hook's stdout.
func TestPowerShellLauncherBuildsDespiteStderr(t *testing.T) {
	root, env, shell := windowsFixture(t, fakeGoBuilds)

	stdout, stderr, code := runPowerShellLauncher(t, root, env, shell, "1")
	if code != 0 {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", code, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "fake build version" {
		t.Errorf("stdout = %q, want only the build's output", stdout)
	}
	if !strings.Contains(stderr, "go: downloading") {
		t.Errorf("stderr = %q, want the build output", stderr)
	}
	if _, err := os.Stat(filepath.Join(root, ".cache", "dev", "duolog.exe")); err != nil {
		t.Errorf("the build was not kept: %v", err)
	}
}

// A failed build whose stderr has lines, and a git that fails with stderr,
// reach the fallback: a worker stops with exit 2, anyone else with 1.
func TestPowerShellLauncherFailsClosedWithoutFallback(t *testing.T) {
	root, env, shell := windowsFixture(t, fakeGoFails)
	for _, tc := range []struct {
		depth string
		want  int
	}{{"1", 2}, {"0", 1}} {
		stdout, stderr, code := runPowerShellLauncher(t, root, env, shell, tc.depth)
		if code != tc.want {
			t.Errorf("depth %s: exit %d, want %d, stderr: %s", tc.depth, code, tc.want, stderr)
		}
		if !strings.Contains(stderr, "there is no earlier build") {
			t.Errorf("depth %s: stderr = %q, want the fallback to run", tc.depth, stderr)
		}
		if strings.Contains(stderr, "NativeCommandError") || strings.Contains(stderr, "fatal: not a git repository") {
			t.Errorf("depth %s: stderr = %q, a native command's stderr escaped", tc.depth, stderr)
		}
		if stdout != "" {
			t.Errorf("depth %s: stdout = %q, want none", tc.depth, stdout)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".cache", "dev", "*"))
	if err != nil || len(matches) != 0 {
		t.Errorf("left behind %v (%v)", matches, err)
	}
}
