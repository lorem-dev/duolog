package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

// devRoot is a checkout whose hooks run the dev launcher, with duolog's dev
// launcher committed, as in duolog's own repository.
func devRoot(t *testing.T, timeout int) (root string, data []byte) {
	t.Helper()
	root = t.TempDir()
	data, err := install.Document(devEvents(timeout))
	if err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, string(data))
	writeFile(t, filepath.Join(root, "scripts", "dev", "bin", "duolog"), "#!/bin/sh\n# duolog built from this checkout.\nexit 0\n")
	writeFile(t, filepath.Join(root, "scripts", "dev", "bin", "duolog.ps1"), "# duolog built from this checkout.\n")
	if !install.IsDevLauncher(filepath.Join(root, "scripts", "dev", "bin", "duolog")) {
		t.Fatal("fixture: no dev launcher")
	}
	return root, data
}

// Codex hashes a dev launcher hook's command, not the script it runs, and
// install never writes that form: neither renewal approves it, even where the
// checkout's launcher is duolog's.
func TestRenewalRefusesDevLauncherForm(t *testing.T) {
	codexHome := adoptHome(t)
	root, current := devRoot(t, install.GateTimeout)
	key := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	config := fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = \"sha256:stale\"\n", quote(key))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	writeFile(t, filepath.Join(codexHome, "duolog-stale-approvals.json"), fmt.Sprintf("{%q: \"sha256:stale\"}", key))
	if lines, err := (Trust{}).AdoptWorktrees(root, os.Environ()); err != nil || len(lines) != 0 || mustRead(t, filepath.Join(codexHome, "config.toml")) != config {
		t.Fatalf("sweep renewed a dev launcher hook: %q, %v", lines, err)
	}

	older, err := install.Document(devEvents(150))
	if err != nil {
		t.Fatal(err)
	}
	approved, err := hookHash(older, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	config = fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = %s\n", quote(key), quote(approved))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	lines, err := Installer{}.RenewApprovals(os.Getenv("HOME"), filepath.Join(root, ".codex", "hooks.json"), older, current)
	if err != nil || len(lines) != 0 || mustRead(t, filepath.Join(codexHome, "config.toml")) != config {
		t.Fatalf("install renewed a dev launcher hook: %q, %v", lines, err)
	}
	if install.UnrenewableApprovalsIn(os.Getenv("HOME"), os.Environ())[key] != approved {
		t.Fatal("the dev launcher approval is not marked unrenewable")
	}
}

// Install marks every changed gate approval stale, so that trust never counts
// it, but one that did not approve duolog's own hook as duolog wrote it is
// also marked never to be renewed, and the sweep leaves it stale.
func TestInstallMarksForeignStaleApprovalUnrenewable(t *testing.T) {
	codexHome := adoptHome(t)
	root := t.TempDir()
	current, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte(strings.ReplaceAll(string(current), "duolog gate --agent", "duolog gate --old --agent"))
	path := filepath.Join(root, ".codex", "hooks.json")
	key := path + ":pre_tool_use:0:0"
	approved, err := hookHash(old, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = %s\n", quote(key), quote(approved))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	writeFile(t, path, string(current))
	if lines, err := (Installer{}).RenewApprovals(os.Getenv("HOME"), path, old, current); err != nil || len(lines) != 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	home := os.Getenv("HOME")
	if install.StaleApprovalsIn(home, os.Environ())[key] != approved || install.UnrenewableApprovalsIn(home, os.Environ())[key] != approved {
		t.Fatal("foreign approval not marked stale and unrenewable")
	}
	if lines, err := (Trust{}).AdoptWorktrees(root, os.Environ()); err != nil || len(lines) != 0 || mustRead(t, filepath.Join(codexHome, "config.toml")) != config {
		t.Fatalf("sweep renewed a foreign approval: %q, %v", lines, err)
	}
	if s, err := hooks(root); err != nil || !strings.Contains(s.Unproven, "approve it again in /hooks") {
		t.Fatalf("trust = %+v, %v", s, err)
	}
}

// Renewal is best-effort: when the config cannot be written, here because a
// folder sits where its backup goes, install gets a warning line and no
// error, and the gate approval is still marked stale.
func TestRenewApprovalsFailureWarnsAndMarksStale(t *testing.T) {
	codexHome := adoptHome(t)
	root := t.TempDir()
	older, err := HooksJSON(150)
	if err != nil {
		t.Fatal(err)
	}
	current, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".codex", "hooks.json")
	key := path + ":pre_tool_use:0:0"
	approved, err := hookHash(older, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexHome, "config.toml"), fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = %s\n", quote(key), quote(approved)))
	if err := os.MkdirAll(filepath.Join(codexHome, "config.toml"+configBackupSuffix), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(current))
	lines, err := Installer{}.RenewApprovals(os.Getenv("HOME"), path, older, current)
	if err != nil || len(lines) != 1 || !strings.HasPrefix(lines[0], "warning: Codex: duolog could not renew") {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	home := os.Getenv("HOME")
	if install.StaleApprovalsIn(home, os.Environ())[key] != approved {
		t.Fatal("not marked stale")
	}
	// It approved duolog's own hook as duolog wrote it, so a later sweep may
	// renew it.
	if _, ok := install.UnrenewableApprovalsIn(home, os.Environ())[key]; ok {
		t.Fatal("own approval marked unrenewable")
	}
}

// A worktree of another repository counts only below the lead: a lead
// elsewhere gets nothing from the submodule's trust.
func TestAdoptWorktreeOfOtherRepositoryStaysBelowLead(t *testing.T) {
	f := newSuperFixture(t)
	before := mustRead(t, f.config)
	elsewhere := t.TempDir()
	if lines, err := (Trust{}).AdoptWorktree(elsewhere, f.worktree, os.Environ()); err != nil || len(lines) != 0 || mustRead(t, f.config) != before {
		t.Fatalf("lead outside adopted: %q, %v", lines, err)
	}
	if problem := (Trust{}).WorktreeTrustProblem(elsewhere, f.worktree, os.Environ()); problem != "" {
		t.Fatalf("problem for a lead outside: %q", problem)
	}
	// The superproject, which holds both, is a lead it counts for.
	if _, ok := sourceRoots(t.Context(), f.super, f.worktree); !ok {
		t.Fatal("superproject lead not a source")
	}
}

// pruneText is a config whose block, which AdoptWorktree could have written
// for dir from from, has the trust key trustKey.
func pruneText(from, dir, trustKey string) []byte {
	src := filepath.Join(from, ".codex", "hooks.json") + ":stop:0:0"
	dst := filepath.Join(dir, ".codex", "hooks.json") + ":stop:0:0"
	return fmt.Appendf(nil, "[hooks.state.%s]\ntrusted_hash = \"sha256:x\"\n\n%s%s%s%s%s\n[projects.%s]\ntrust_level = \"trusted\"\n\n[hooks.state.%s]\ntrusted_hash = \"sha256:x\"\n%s\n",
		quote(src), commentPrefix, dir, commentMiddle, from, commentSuffix, quote(trustKey), quote(dst), commentEnd)
}

// Prune takes out a removed worktree's block only when the paths are spelled
// exactly as the comment spells a clean path, and the folder above the
// worktree and the source root exist.
func TestPruneNeedsExactPathsAndMountedFolders(t *testing.T) {
	from := t.TempDir()
	parent := filepath.Join(from, ".worktrees")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(parent, "gone")
	if _, removed := pruneBlocks(pruneText(from, gone, gone)); len(removed) != 1 {
		t.Fatalf("control case not pruned: %q", removed)
	}
	for name, data := range map[string][]byte{
		"unclean comment path":          pruneText(from, parent+string(filepath.Separator)+"x"+string(filepath.Separator)+".."+string(filepath.Separator)+"gone", gone),
		"trust key spelled differently": pruneText(from, gone, gone+string(filepath.Separator)+"."),
		"source root missing":           pruneText(filepath.Join(from, "missing"), gone, gone),
		"parent folder missing":         pruneText(from, filepath.Join(from, "unmounted", "gone"), filepath.Join(from, "unmounted", "gone")),
	} {
		if edited, removed := pruneBlocks(data); len(removed) != 0 || string(edited) != string(data) {
			t.Errorf("%s: pruned %q", name, removed)
		}
	}
}
