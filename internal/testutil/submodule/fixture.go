// Package submodule builds temporary Git submodule fixtures for tests.
package submodule

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
)

// Fixture names related checkouts and an unrelated repository nested by path.
// Config files are left to the caller so tests can choose each work project's rules.
type Fixture struct {
	Superproject, Submodule, Linked, Nested      string
	SuperLinked, SuperLinkedSubmodule, Unrelated string
}

// New builds submodules at two depths and linked checkouts of both projects.
// All repositories and their Git metadata live under t.TempDir().
func New(t *testing.T) Fixture {
	t.Helper()
	base := t.TempDir()
	nestedSource := filepath.Join(base, "nested-source")
	source := filepath.Join(base, "source")
	f := Fixture{
		Superproject: filepath.Join(base, "superproject"),
		Linked:       filepath.Join(base, "linked"),
		SuperLinked:  filepath.Join(base, "super-linked"),
	}
	f.Submodule = filepath.Join(f.Superproject, "module")
	f.Nested = filepath.Join(f.Submodule, "nested")
	f.SuperLinkedSubmodule = filepath.Join(f.SuperLinked, "module")
	f.Unrelated = filepath.Join(f.Superproject, "unrelated")
	for _, dir := range []string{nestedSource, source, f.Superproject, f.Unrelated} {
		initRepository(t, dir)
	}
	git(t, source, "submodule", "add", "-q", nestedSource, "nested")
	git(t, source, "commit", "-q", "-am", "Add nested submodule")
	git(t, f.Superproject, "submodule", "add", "-q", source, "module")
	git(t, f.Superproject, "commit", "-q", "-am", "Add submodule")
	git(t, f.Superproject, "submodule", "update", "--init", "--recursive")
	git(t, f.Submodule, "worktree", "add", "-q", "--detach", f.Linked)
	git(t, f.Superproject, "worktree", "add", "-q", "--detach", f.SuperLinked)
	git(t, f.SuperLinked, "submodule", "update", "--init", "--recursive")
	return f
}

// Init makes dir a new repository with one empty commit.
func Init(t *testing.T, dir string) {
	t.Helper()
	initRepository(t, dir)
}

// Git runs git in dir with the fixture's test identity, unsigned commits,
// local file transport and a cleaned Git environment.
func Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	git(t, dir, args...)
}

func initRepository(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "Initial commit")
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	options := []string{
		"-c", "protocol.file.allow=always",
		"-c", "user.name=test", "-c", "user.email=test@example.invalid",
		"-c", "commit.gpgsign=false",
	}
	cmd := exec.Command("git", append(options, args...)...)
	cmd.Dir = dir
	cmd.Env = configfile.CleanGitEnv(os.Environ())
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
