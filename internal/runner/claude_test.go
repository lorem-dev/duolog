package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
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
		s = rebuilt(t, s)
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

// TestProtectedRootsForwarded: the store roots in the Spec reach the Claude
// worker's denyRead whether collection is on or off; a Codex worker has only
// the gate, and the person's settings are never edited.
func TestProtectedRootsForwarded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	userSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(userSettings), 0700); err != nil {
		t.Fatal(err)
	}
	const installed = `{"sandbox":{"enabled":false}}`
	if err := os.WriteFile(userSettings, []byte(installed), 0600); err != nil {
		t.Fatal(err)
	}
	roots := []string{filepath.Join(home, "state", "duolog", "telemetry"), filepath.Join(home, "state", "duolog", "balance")}
	for _, collection := range []string{"", "off"} {
		t.Setenv("DUOLOG_TELEMETRY", collection)
		for _, context := range []string{"minimal", "project"} {
			s := spec(t, "claude")
			s.Route.Context = context
			s.ProtectedRoots = roots
			s = rebuilt(t, s)
			args := Argv(s)
			i := slices.Index(args, "--settings")
			if i < 0 || i+1 == len(args) {
				t.Fatal(args)
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(args[i+1]), &settings); err != nil {
				t.Fatal(err)
			}
			sandbox, _ := settings["sandbox"].(map[string]any)
			if _, on := sandbox["enabled"]; on {
				t.Fatal("duolog must never turn the sandbox on", args[i+1])
			}
			fs, _ := sandbox["filesystem"].(map[string]any)
			denied, _ := fs["denyRead"].([]any)
			for _, root := range roots {
				if !slices.Contains(denied, any(filepath.ToSlash(root))) {
					t.Fatal(collection, context, args[i+1])
				}
			}
			if !slices.Contains(denied, any(filepath.ToSlash(project.StateRoot(s.Cwd))+"/.duolog/journal")) {
				t.Fatal(args[i+1])
			}
		}
		codex := spec(t, "codex")
		codex.ProtectedRoots = roots
		codex = rebuilt(t, codex)
		if line := strings.Join(Argv(codex), " "); strings.Contains(line, filepath.ToSlash(roots[0])) || strings.Contains(line, roots[0]) {
			t.Fatal(line)
		}
	}
	if data, err := os.ReadFile(userSettings); err != nil || string(data) != installed {
		t.Fatalf("user settings changed: %s %v", data, err)
	}
}

func TestClaudeSkillArgvOmitsSandbox(t *testing.T) {
	s := spec(t, "claude")
	s.SkillTest = true
	s = rebuilt(t, s)
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
