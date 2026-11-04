package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/pathutil"
)

func TestClaudeArgvUsesSharedStateRoot(t *testing.T) {
	root, linked := t.TempDir(), t.TempDir()
	for _, name := range []string{".git/objects", ".git/refs/heads", ".git/worktrees/linked"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, contents := range map[string]string{
		filepath.Join(root, ".git/HEAD"):                       "ref: refs/heads/main\n",
		filepath.Join(root, ".git/worktrees/linked/HEAD"):      "ref: refs/heads/linked\n",
		filepath.Join(root, ".git/worktrees/linked/commondir"): "../..\n",
		filepath.Join(root, ".git/worktrees/linked/gitdir"):    filepath.Join(linked, ".git") + "\n",
		filepath.Join(linked, ".git"):                          "gitdir: " + filepath.Join(root, ".git/worktrees/linked") + "\n",
	} {
		if err := os.WriteFile(name, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := spec(t, "claude")
	s.Cwd = linked
	for _, context := range []string{"minimal", "project"} {
		s.Route.Context = context
		args := Argv(s)
		i := slices.Index(args, "--settings")
		if i < 0 || i+1 == len(args) {
			t.Fatal(args)
		}
		if !strings.Contains(args[i+1], filepath.ToSlash(pathutil.Resolved(root))+"/.duolog/decisions.ndjson") || strings.Contains(args[i+1], filepath.ToSlash(pathutil.Resolved(linked))+"/.duolog/") {
			t.Fatal(context, args[i+1])
		}
	}
}

func TestClaudeSkillArgvOmitsSandbox(t *testing.T) {
	s := spec(t, "claude")
	s.SkillTest = true
	args := Argv(s)
	i := slices.Index(args, "--settings")
	if i < 0 || i+1 == len(args) || strings.Contains(args[i+1], `"sandbox"`) {
		t.Fatal(args)
	}
	i = slices.Index(args, "--tools")
	if i < 0 || i+1 == len(args) || args[i+1] != "" {
		t.Fatal(args)
	}
}
