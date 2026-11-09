package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// checkedProject is a Git project in a temporary folder, the working directory,
// whose one commit holds files (name to text). With no files it has no commit.
func checkedProject(t *testing.T, files map[string]string) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if len(files) == 0 {
		return root
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", name)
	}
	git("commit", "-q", "-m", "first")
	return root
}

// onlyVetAllowed replaces the rules in the user config with one that allows
// go vet for implement, so that go test, the other check a Go project has,
// is one the gate asks about.
func onlyVetAllowed(t *testing.T) {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "duolog", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	rules := "policy:\n  rules:\n    - {action: exec, match: \"go vet*\", decision: allow, roles: [implement]}\n"
	if err := os.WriteFile(path, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A project check that the gate would ask about for implement is named, with
// the command that approves it; one it allows is not.
func TestBlockedProjectChecksNamesWhatTheGateWouldStop(t *testing.T) {
	root := checkedProject(t, map[string]string{"go.mod": "module x\n"})
	onlyVetAllowed(t)
	lines := blockedProjectChecks(context.Background(), root)
	if len(lines) != 1 {
		t.Fatalf("lines = %q, want one for go test", lines)
	}
	for _, want := range []string{`"go test ./..."`, "ask", "implement", "duolog remember 'go test ./...'"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("line %q lacks %q", lines[0], want)
		}
	}
	if strings.Contains(lines[0], "go vet") {
		t.Errorf("an allowed check is named: %q", lines[0])
	}
}

func TestBlockedProjectChecksIsQuietWhenNothingIsStopped(t *testing.T) {
	root := checkedProject(t, map[string]string{"go.mod": "module x\n"})
	if lines := blockedProjectChecks(context.Background(), root); len(lines) != 0 {
		t.Errorf("lines = %q for allowed checks", lines)
	}
	root = checkedProject(t, nil)
	if lines := blockedProjectChecks(context.Background(), root); len(lines) != 0 {
		t.Errorf("lines = %q for a project with no commit", lines)
	}
}

// doctor and install --check print the lines.
func TestDoctorAndInstallCheckPrintBlockedProjectChecks(t *testing.T) {
	checkedProject(t, map[string]string{"go.mod": "module x\n"})
	onlyVetAllowed(t)
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(git))
	fakeRexFirst(t)
	const want = "duolog remember 'go test ./...'"
	out, _ := executeBusCommand(t, "doctor")
	if !strings.Contains(out, want) {
		t.Errorf("doctor output lacks %q:\n%s", want, out)
	}
	out, _ = executeBusCommand(t, "install", "--check", "--scope", "project")
	if !strings.Contains(out, want) {
		t.Errorf("install --check output lacks %q:\n%s", want, out)
	}
	out, _ = executeBusCommand(t, "install", "--check", "--scope", "user")
	if strings.Contains(out, want) {
		t.Errorf("install --check at user scope names a project check:\n%s", out)
	}
}
