package calibration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

// gateHooks is a Codex hooks.json with the duolog gate on every tool.
const gateHooks = `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"duolog gate"}]}]}}`

// tempHome points the home, the user cache and the Codex config at a fresh
// folder, so no test reads or writes the person's own.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "cache"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

// trustCodex writes a Codex config under home that trusts dir and approves
// the gate hook in dir/.codex/hooks.json, as Codex does once the person
// trusts the folder and reviews its hooks.
func trustCodex(t *testing.T, home, dir string, folder, hooks bool) {
	t.Helper()
	var s string
	if folder {
		s += fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", dir)
	}
	if hooks {
		s += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(dir, ".codex", "hooks.json")+":pre_tool_use:0:0")
	}
	write(t, filepath.Join(home, ".codex"), "config.toml", s)
}

// empty reports whether dir exists and holds nothing.
func empty(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commit stages every file in root and returns the new commit.
func commit(t *testing.T, root, message string) string {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", message)
	return git(t, root, "rev-parse", "HEAD")
}

func project(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	return root
}

func tree(t *testing.T, root, c string) string {
	t.Helper()
	dir, cleanup, err := NewTree(context.Background(), root, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	return dir
}

func TestExportHasOnlyParentTree(t *testing.T) {
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")
	write(t, root, "b.txt", "b\n")
	c2 := commit(t, root, "add b")

	dir, cleanup, err := NewTree(ctx, root, c1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(dir), "duolog-cal-") {
		t.Fatalf("tree %s is not named duolog-cal-*", dir)
	}
	if !exists(filepath.Join(dir, "a.txt")) {
		t.Fatal("a.txt is missing from the export")
	}
	if exists(filepath.Join(dir, "b.txt")) {
		t.Fatal("b.txt from the descendant commit is in the export")
	}
	if got := git(t, dir, "rev-list", "--all", "--count"); got != "1" {
		t.Fatalf("rev-list --all --count = %q, want 1", got)
	}
	if got := git(t, dir, "remote"); got != "" {
		t.Fatalf("git remote = %q, want none", got)
	}
	if err := exec.Command("git", "-C", dir, "cat-file", "-e", c2).Run(); err == nil {
		t.Fatalf("descendant commit %s is reachable in the export", c2)
	}
	if exists(filepath.Join(dir, ".git", "logs")) {
		t.Fatal(".git/logs exists in the export")
	}
	if err := VerifyIsolated(dir, root); err != nil {
		t.Fatalf("VerifyIsolated: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if exists(dir) {
		t.Fatalf("%s still exists after cleanup", dir)
	}
}

func TestExportDropsCalibrationFolder(t *testing.T) {
	root := project(t)
	write(t, root, ".duolog/calibration/project.yaml", "classes: {}\n")
	write(t, root, ".duolog/config.yaml", "mode: solo\n")
	c1 := commit(t, root, "add state")

	dir := tree(t, root, c1)
	if !exists(filepath.Join(dir, ".duolog", "config.yaml")) {
		t.Fatal(".duolog/config.yaml is missing from the export")
	}
	if exists(filepath.Join(dir, ".duolog", "calibration")) {
		t.Fatal(".duolog/calibration is in the export")
	}
}

// The export holds the commit's files as Git stores them: export-ignore and
// export-subst, from the tree or from .git/info/attributes, change nothing.
func TestExportIgnoresExportAttributes(t *testing.T) {
	root := project(t)
	write(t, root, ".gitattributes", "tests/ export-ignore\nversion.txt export-subst\n")
	write(t, root, "tests/calc_test.go", "package calc\n")
	write(t, root, "version.txt", "$Format:%H$\n")
	write(t, root, "private.txt", "kept\n")
	write(t, root, "run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".git/info/attributes", "private.txt export-ignore\n")
	c1 := commit(t, root, "add files")

	dir := tree(t, root, c1)
	for name, want := range map[string]string{"tests/calc_test.go": "package calc\n", "version.txt": "$Format:%H$\n", "private.txt": "kept\n", "run.sh": "#!/bin/sh\n"} {
		if body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))); err != nil || string(body) != want {
			t.Fatalf("%s in the export = %q, %v; want %q", name, body, err, want)
		}
	}
	// Windows has no executable bit to keep.
	if info, err := os.Stat(filepath.Join(dir, "run.sh")); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm()&0o100 == 0) {
		t.Fatalf("run.sh in the export: %v, %v; want it executable", info, err)
	}
	if err := VerifyIsolated(dir, root); err != nil {
		t.Fatalf("VerifyIsolated: %v", err)
	}
}

func TestExportRefusesDestinationInsideProject(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")

	err := Export(context.Background(), root, c1, filepath.Join(root, "x"))
	if err == nil || !strings.Contains(err.Error(), "outside the project") {
		t.Fatalf("Export inside the project: err = %v, want outside the project", err)
	}
}

// escapingLinks lists link sets, name to target, that each leave the tree
// of a project holding a/b/keep.txt once followed; root is the original
// project.
func escapingLinks(root string) []struct {
	name  string
	links [][2]string
} {
	return []struct {
		name  string
		links [][2]string
	}{
		{"absolute link to the project", [][2]string{{"source", root}}},
		{"relative escape", [][2]string{{"a/up", "../.."}}},
		// Each link stays inside on its own; up resolves to the tree root,
		// so out ends one level above it.
		{"chain of two links", [][2]string{{"a/b/up", "../.."}, {"out", "a/b/up/.."}}},
	}
}

func symlink(t *testing.T, root, name, target string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestExportRefusesEscapingLinks(t *testing.T) {
	for i, tc := range escapingLinks("") {
		t.Run(tc.name, func(t *testing.T) {
			root := project(t)
			write(t, root, "a/b/keep.txt", "k\n")
			for _, link := range escapingLinks(root)[i].links {
				symlink(t, root, link[0], link[1])
			}
			c1 := commit(t, root, "add links")
			write(t, root, "solution.txt", "s\n")
			commit(t, root, "add solution")

			dir, cleanup, err := NewTree(context.Background(), root, c1)
			if err == nil {
				_ = cleanup()
				t.Fatalf("NewTree exported %s", dir)
			}
			if !strings.Contains(err.Error(), "outside the export") {
				t.Fatalf("NewTree: err = %v, want outside the export", err)
			}
		})
	}
}

func TestVerifyIsolatedFindsEscapingLinks(t *testing.T) {
	root := project(t)
	write(t, root, "a/b/keep.txt", "k\n")
	c1 := commit(t, root, "add a")

	for _, tc := range escapingLinks(root) {
		t.Run(tc.name, func(t *testing.T) {
			dir := tree(t, root, c1)
			for _, link := range tc.links {
				symlink(t, dir, link[0], link[1])
			}
			err := VerifyIsolated(dir, root)
			if err == nil || !strings.Contains(err.Error(), "outside the export") {
				t.Fatalf("VerifyIsolated: err = %v, want outside the export", err)
			}
		})
	}
}

func TestExportKeepsInternalLinks(t *testing.T) {
	root := project(t)
	write(t, root, "a/b/keep.txt", "k\n")
	symlink(t, root, "a/b/up", "../..")
	symlink(t, root, "a/keep.txt", "b/keep.txt")
	symlink(t, root, "a/b/round", "up/a/b/keep.txt")
	c1 := commit(t, root, "add links")

	dir := tree(t, root, c1)
	for _, name := range []string{"a/keep.txt", "a/b/round"} {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || string(body) != "k\n" {
			t.Fatalf("read %s through its link: %q %v", name, body, err)
		}
	}
	if err := VerifyIsolated(dir, root); err != nil {
		t.Fatalf("VerifyIsolated: %v", err)
	}
}

// Windows writes a link target with backslashes, and a backslash separates
// there; elsewhere a target written the Windows way is refused.
func TestRelativeTarget(t *testing.T) {
	keep := map[string]string{"b/keep.txt": "b/keep.txt", "../..": "../..", "up/a/b/keep.txt": "up/a/b/keep.txt"}
	refuse := []string{"/etc", "//server/share/x"}
	if runtime.GOOS == "windows" {
		keep[`up\a\b\keep.txt`] = "up/a/b/keep.txt"
		keep[`..\..`] = "../.."
		refuse = append(refuse, `C:\x`, `C:/x`, `C:x`, `\x`, `\\server\share\x`, `\\?\C:\x`)
	} else {
		refuse = append(refuse, `up\a\b\keep.txt`, `C:\x`, `C:x`)
	}
	for target, want := range keep {
		if got, ok := relativeTarget(target); !ok || got != want {
			t.Errorf("relativeTarget(%q) = %q, %v; want %q, true", target, got, ok, want)
		}
	}
	for _, target := range refuse {
		if got, ok := relativeTarget(target); ok {
			t.Errorf("relativeTarget(%q) = %q, true; want it refused", target, got)
		}
	}
}

// A link that climbs out with backslashes is refused, on Windows by the
// climb and elsewhere as a Windows target.
func TestVerifyIsolatedFindsBackslashEscape(t *testing.T) {
	root := project(t)
	write(t, root, "a/b/keep.txt", "k\n")
	c1 := commit(t, root, "add a")
	dir := tree(t, root, c1)
	symlink(t, dir, "a/up", `..\..`)
	err := VerifyIsolated(dir, root)
	if err == nil || !strings.Contains(err.Error(), "outside the export") {
		t.Fatalf("VerifyIsolated: err = %v, want outside the export", err)
	}
}

func TestVerifyIsolatedFindsRemote(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")

	dir := tree(t, root, c1)
	git(t, dir, "remote", "add", "origin", root)
	err := VerifyIsolated(dir, root)
	if err == nil || !strings.Contains(err.Error(), "remote") {
		t.Fatalf("VerifyIsolated with a remote: err = %v, want remote", err)
	}
}

func TestTreeSlotIsStablePerProject(t *testing.T) {
	tempHome(t)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "add a")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-q", "--detach", linked, "HEAD")

	slot, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := TreeSlot(root, 0); again != slot {
		t.Fatalf("TreeSlot changed between calls: %s, then %s", slot, again)
	}
	if fromLinked, _ := TreeSlot(linked, 0); fromLinked != slot {
		t.Fatalf("a linked worktree gets slot %s, want the project's %s", fromLinked, slot)
	}
	parent := filepath.Join(cache, "duolog", "calibration")
	if filepath.Base(slot) != "tree-0" || filepath.Dir(filepath.Dir(slot)) != parent {
		t.Fatalf("slot %s is not %s/<project id>/tree-0", slot, parent)
	}
	second, _ := TreeSlot(root, 1)
	if filepath.Base(second) != "tree-1" || filepath.Dir(second) != filepath.Dir(slot) {
		t.Fatalf("slot 1 is %s, want tree-1 beside %s", second, slot)
	}
	if other, _ := TreeSlot(project(t), 0); other == slot {
		t.Fatalf("two projects share the slot %s", slot)
	}
}

func TestNewTreeAtReplacesSlot(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")
	write(t, root, "b.txt", "b\n")
	commit(t, root, "add b")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	// A run that stopped without its cleanup left a tree and a stray file.
	write(t, dir, "stale.txt", "left over\n")
	write(t, dir, ".git/HEAD", "ref: refs/heads/main\n")

	cleanup, err := NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "stale.txt")) {
		t.Fatal("stale.txt survived the new export")
	}
	if !exists(filepath.Join(dir, "a.txt")) || exists(filepath.Join(dir, "b.txt")) {
		t.Fatal("the slot does not hold the tree of the exported commit")
	}
	if err := VerifyIsolated(dir, root); err != nil {
		t.Fatalf("VerifyIsolated: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if !empty(t, dir) {
		t.Fatalf("cleanup left %s missing or not empty", dir)
	}
	// The next cell reuses the kept folder.
	cleanup, err = NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func TestNewTreeAtRefusesProjectFolders(t *testing.T) {
	tempHome(t)
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")
	for _, dir := range []string{filepath.Join(root, "x"), root, filepath.Dir(root)} {
		write(t, root, "x/keep.txt", "k\n")
		cleanup, err := NewTreeAt(context.Background(), root, c1, dir, os.Environ())
		if err == nil {
			_ = cleanup()
			t.Fatalf("NewTreeAt exported into %s", dir)
		}
		if !exists(filepath.Join(root, "a.txt")) || !exists(filepath.Join(root, "x", "keep.txt")) {
			t.Fatalf("NewTreeAt into %s removed project files", dir)
		}
	}
}

func TestNewTreeAtCopiesCodexHooksFromHead(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, ".codex/hooks.json", `{"hooks":{}}`)
	c1 := commit(t, root, "add a")
	write(t, root, ".codex/hooks.json", gateHooks)
	commit(t, root, "add the gate")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	cleanup, err := NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	body, err := os.ReadFile(filepath.Join(dir, ".codex", "hooks.json"))
	if err != nil || string(body) != gateHooks {
		t.Fatalf("hooks.json in the tree = %q, %v; want the project's at HEAD", body, err)
	}
	if err := VerifyIsolated(dir, root); err != nil {
		t.Fatalf("VerifyIsolated: %v", err)
	}

	bare := project(t)
	write(t, bare, "a.txt", "a\n")
	c2 := commit(t, bare, "add a")
	other, _ := TreeSlot(bare, 0)
	cleanup, err = NewTreeAt(ctx, bare, c2, other, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if exists(filepath.Join(other, ".codex")) {
		t.Fatal("a project without hooks.json got a .codex folder in its tree")
	}
}

func TestNewTreeAtCopiesCodexHooksFromWorkingCopy(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, ".codex/hooks.json", `{"hooks":{}}`)
	c1 := commit(t, root, "add a")
	// The person installed the gate and has not committed it yet.
	write(t, root, ".codex/hooks.json", gateHooks)
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	cleanup, err := NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	body, err := os.ReadFile(filepath.Join(dir, ".codex", "hooks.json"))
	if err != nil || string(body) != gateHooks {
		t.Fatalf("hooks.json in the tree = %q, %v; want the working copy's", body, err)
	}

	// Without a working copy file, HEAD's is used.
	if err := os.Remove(filepath.Join(root, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	cleanup, err = NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(filepath.Join(dir, ".codex", "hooks.json"))
	if err != nil || string(body) != `{"hooks":{}}` {
		t.Fatalf("hooks.json in the tree = %q, %v; want HEAD's", body, err)
	}
}

func TestCheckTreeTrust(t *testing.T) {
	home := tempHome(t)
	dir := t.TempDir()
	write(t, dir, ".codex/hooks.json", gateHooks)
	for _, tc := range []struct {
		name          string
		folder, hooks bool
	}{
		{"nothing trusted", false, false},
		{"folder only", true, false},
		{"hooks only", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trustCodex(t, home, dir, tc.folder, tc.hooks)
			err := CheckTreeTrust("codex", dir, os.Environ())
			if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "hooks") {
				t.Fatalf("CheckTreeTrust = %v, want an error naming %s and its hooks", err, dir)
			}
		})
	}
	trustCodex(t, home, dir, true, true)
	if err := CheckTreeTrust("codex", dir, os.Environ()); err != nil {
		t.Fatalf("CheckTreeTrust on a trusted tree: %v", err)
	}
	// Claude needs no trust, but a tree without the duolog gate is refused.
	bare := t.TempDir()
	if err := CheckTreeTrust("claude", bare, os.Environ()); err == nil || !strings.Contains(err.Error(), bare) || !strings.Contains(err.Error(), "gate") {
		t.Fatalf("CheckTreeTrust for claude without a gate = %v, want an error naming %s and the gate", err, bare)
	}
	write(t, bare, ".claude/settings.local.json", claudeGate(t))
	if err := CheckTreeTrust("claude", bare, os.Environ()); err != nil {
		t.Fatalf("CheckTreeTrust for claude with the gate: %v", err)
	}
}

// Re-review, Important 2: the tree's gate is judged from the worker's own
// environment. The caller's CLAUDE_CONFIG_DIR runs the gate and its
// CODEX_HOME trusts the tree, but the worker gets neither: the tree gets a
// local Claude gate, and Codex is refused for the folder trust the worker's
// own home lacks.
func TestTreeTrustReadsTheWorkersEnvironment(t *testing.T) {
	home := tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "add a")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	worker := []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	caller := t.TempDir()
	write(t, caller, "settings.json", claudeGate(t))
	t.Setenv("CLAUDE_CONFIG_DIR", caller)

	bare := t.TempDir()
	if err := CheckTreeTrust("claude", bare, worker); err == nil {
		t.Error("claude: a tree whose gate is only in the caller's CLAUDE_CONFIG_DIR passed")
	}
	cleanup, err := NewTreeAt(ctx, root, c1, dir, worker)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if !install.HasGate(filepath.Join(dir, ".claude", "settings.local.json")) {
		t.Error("claude: no local gate in the tree, although the worker's settings have none")
	}

	// trustCodex writes <home>/.codex/config.toml, so CODEX_HOME is that
	// .codex folder.
	callerHome := t.TempDir()
	codexHome := filepath.Join(callerHome, ".codex")
	write(t, dir, ".codex/hooks.json", gateHooks)
	trustCodex(t, callerHome, dir, true, true)
	t.Setenv("CODEX_HOME", codexHome)
	trustCodex(t, home, dir, false, true)
	if err := CheckTreeTrust("codex", dir, worker); err == nil {
		t.Error("codex: a tree trusted only in the caller's CODEX_HOME passed")
	}
	// Passed on, the caller's folders are the worker's, and they count.
	both := append(slices.Clone(worker), "CLAUDE_CONFIG_DIR="+caller, "CODEX_HOME="+codexHome)
	if err := CheckTreeTrust("claude", bare, both); err != nil {
		t.Errorf("claude with CLAUDE_CONFIG_DIR passed on: %v", err)
	}
	if err := CheckTreeTrust("codex", dir, both); err != nil {
		t.Errorf("codex with CODEX_HOME passed on: %v", err)
	}
}

func claudeGate(t *testing.T) string {
	t.Helper()
	hooks, err := install.HooksJSON("claude", 150)
	if err != nil {
		t.Fatal(err)
	}
	return string(hooks)
}

// A tree gets the duolog gate for Claude in .claude/settings.local.json when
// nothing else gives it one, outside the tree's commit; a parent's own copy
// of that personal file is not exported.
func TestNewTreeAtAddsClaudeGate(t *testing.T) {
	home := tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, ".claude/settings.local.json", `{"hooks":{}}`)
	c1 := commit(t, root, "add a")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}

	cleanup, err := NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, ".claude", "settings.local.json")
	if !install.HasGate(local) {
		t.Fatalf("%s has no duolog gate", local)
	}
	if out := git(t, dir, "ls-files"); out != "a.txt" {
		t.Fatalf("the tree's commit tracks %q, want only a.txt", out)
	}
	if err := CheckTreeTrust("claude", dir, os.Environ()); err != nil {
		t.Fatalf("CheckTreeTrust: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}

	// The person's own Claude settings run the gate: no second copy.
	write(t, filepath.Join(home, ".claude"), "settings.json", claudeGate(t))
	cleanup, err = NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if exists(local) {
		t.Fatalf("%s written although the user settings run the gate", local)
	}
}

// The judge slot gets the Claude gate as a tree does, since CheckTreeTrust
// refuses Claude without it. Its hooks are written afresh, never through a
// link a worker left, and only they and the slot's repository stay.
func TestNewJudgeSlotGivesClaudeGate(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, filepath.Join(root, ".codex"), "hooks.json", gateHooks)
	commit(t, root, "add hooks")
	slot, err := JudgeSlot(root)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := NewJudgeSlot(ctx, root, slot, map[string][]byte{"a.patch": []byte("+a\n")}, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if !install.HasGate(filepath.Join(slot, ".claude", "settings.local.json")) {
		t.Fatal("the judge slot has no Claude gate")
	}
	if err := CheckTreeTrust("claude", slot, os.Environ()); err != nil {
		t.Fatalf("CheckTreeTrust: %v", err)
	}
	// A worker leaves a link in place of the hooks and a Codex config.
	outside := filepath.Join(t.TempDir(), "outside.json")
	write(t, filepath.Dir(outside), "outside.json", "keep\n")
	hooks := filepath.Join(slot, ".codex", "hooks.json")
	if err := os.Remove(hooks); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, hooks); err != nil {
		t.Skip("no symlinks:", err)
	}
	write(t, filepath.Join(slot, ".codex"), "config.toml", "model = \"x\"\n")
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJudgeSlot(ctx, root, slot, nil, os.Environ()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(outside); string(data) != "keep\n" {
		t.Fatalf("the slot wrote through the link: %q", data)
	}
	if info, err := os.Lstat(hooks); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("hooks %v, %v; want a regular file", info, err)
	}
	if exists(filepath.Join(slot, ".codex", "config.toml")) {
		t.Fatal("the worker's Codex config stayed in the slot")
	}
	if !exists(filepath.Join(slot, ".git")) {
		t.Fatal("the slot lost its repository")
	}
}

// Emptying a tree keeps the Codex hooks the person approved there, as a
// regular file only, and removes everything else.
func TestCleanupKeepsApprovedHooks(t *testing.T) {
	tempHome(t)
	ctx := context.Background()
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, ".codex/hooks.json", gateHooks)
	write(t, root, ".codex/config.toml", "model = \"x\"\n")
	c1 := commit(t, root, "add a")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "ls-files"); out != ".codex/config.toml\na.txt" {
		t.Fatalf("the tree's commit tracks %q, want .codex/config.toml and a.txt", out)
	}
	write(t, dir, ".codex/notes.txt", "candidate\n")
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".codex")); len(entries) != 1 || entries[0].Name() != "hooks.json" {
		t.Fatalf(".codex in the emptied tree holds %v, want hooks.json only", entries)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("the emptied tree holds %v, want .codex only", entries)
	}

	// A link in place of the hooks is not kept, and nothing is written
	// through it.
	outside := filepath.Join(t.TempDir(), "outside.json")
	write(t, filepath.Dir(outside), "outside.json", "keep\n")
	if err := os.Remove(filepath.Join(dir, ".codex", "hooks.json")); err != nil {
		t.Fatal(err)
	}
	symlink(t, dir, ".codex/hooks.json", outside)
	cleanup, err = NewTreeAt(ctx, root, c1, dir, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cleanup() })
	if body, err := os.ReadFile(outside); err != nil || string(body) != "keep\n" {
		t.Fatalf("the file behind the link = %q, %v; want it unchanged", body, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, ".codex", "hooks.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("hooks.json in the tree: %v, %v; want a regular file", info, err)
	}
}

// PrepareTree gives a plan's slot the hooks to approve before any run.
func TestPrepareTreeWritesHooks(t *testing.T) {
	tempHome(t)
	root := project(t)
	write(t, root, ".codex/hooks.json", gateHooks)
	commit(t, root, "add the gate")
	dir, err := TreeSlot(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareTree(context.Background(), root, dir); err != nil {
		t.Fatal(err)
	}
	onlyHooks(t, dir)
}
