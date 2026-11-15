package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/install"
)

// superFixture is a superproject at super, which Codex does not trust, with
// a submodule at super/sub that Codex trusts with every duolog hook
// approved, and a linked worktree of the submodule below it.
type superFixture struct {
	codexHome, super, sub, worktree, config string
}

func newSuperFixture(t *testing.T) superFixture {
	t.Helper()
	base := t.TempDir()
	f := superFixture{codexHome: adoptHome(t), super: filepath.Join(base, "super")}
	origin := filepath.Join(base, "origin")
	repoWithHooks(t, origin)
	writeFile(t, filepath.Join(f.super, "README"), "super\n")
	git(t, f.super, "init", "-q")
	git(t, f.super, "add", "README")
	git(t, f.super, "commit", "-q", "-m", "super")
	git(t, f.super, "-c", "protocol.file.allow=always", "submodule", "add", "-q", origin, "sub")
	git(t, f.super, "commit", "-q", "-m", "sub")
	f.sub = filepath.Join(f.super, "sub")
	f.worktree = filepath.Join(f.sub, ".worktrees", "x")
	git(t, f.sub, "worktree", "add", "-q", "--detach", f.worktree)
	f.config = filepath.Join(f.codexHome, "config.toml")
	writeFile(t, f.config, configHead+"\n"+approvalsFor(f.sub))
	return f
}

// A lead in a superproject that delegates into a worktree of a submodule:
// the worktree is no worktree of the lead's repository, so its own
// repository's main checkout, the submodule, is the source.
func TestAdoptWorktreeOfSubmoduleFromSuperprojectLead(t *testing.T) {
	f := newSuperFixture(t)
	before, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := linkedWorktree(f.super, f.worktree); ok {
		t.Fatal("fixture: the worktree belongs to the superproject")
	}
	lines, err := Trust{}.AdoptWorktree(f.super, f.worktree, os.Environ())
	if err != nil || len(lines) != 1+len(events(install.GateTimeout)) {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	after, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatalf("config changed before its end:\n%s", after)
	}
	if !strings.Contains(string(after), string(filepath.Separator)+filepath.Join("super", "sub")+commentSuffix) {
		t.Fatalf("source is not the submodule:\n%s", after)
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
	if s, err := hooks(f.worktree); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("worktree hooks = %+v, %v", s, err)
	}
}

// The sweep from the superproject's root finds the trusted submodule, whose
// git worktree list names its Git folder first, and adopts its worktree.
func TestAdoptWorktreesSweepsSubmoduleWorktree(t *testing.T) {
	f := newSuperFixture(t)
	lines, err := Trust{}.AdoptWorktrees(f.super, os.Environ())
	if err != nil || len(lines) != 1+len(events(install.GateTimeout)) {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
}

// The submodule's main checkout counts only once git proves the worktree
// linked to it: a folder with a copied .git file gets nothing.
func TestAdoptWorktreeOfSubmoduleNeedsAProvenWorktree(t *testing.T) {
	f := newSuperFixture(t)
	fake := filepath.Join(f.sub, ".worktrees", "fake")
	gitFile, err := os.ReadFile(filepath.Join(f.worktree, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fake, ".git"), string(gitFile))
	writeFile(t, filepath.Join(fake, ".codex", "hooks.json"), mustRead(t, filepath.Join(f.sub, ".codex", "hooks.json")))
	before := mustRead(t, f.config)
	if lines, err := (Trust{}).AdoptWorktree(f.super, fake, os.Environ()); err != nil || len(lines) != 0 || mustRead(t, f.config) != before {
		t.Fatalf("fake worktree adopted: %q %v", lines, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The sweep finds a worktree made with plain git worktree add below the
// project root, gives it trust, and once the worktree is removed takes the
// tables it wrote out again, leaving the config as it was.
func TestAdoptWorktreesSweepsPlainWorktreeAndPrunesRemoved(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	lines, err := Trust{}.AdoptWorktrees(f.lead, os.Environ())
	if err != nil || len(lines) != 1+len(events(install.GateTimeout)) {
		t.Fatalf("sweep lines = %q, %v", lines, err)
	}
	if ok, err := folder(f.worktree); err != nil || !ok {
		t.Fatalf("worktree not trusted: %v %v", ok, err)
	}
	adopted := f.read(t)
	// A second sweep finds nothing to do.
	if lines, err := (Trust{}).AdoptWorktrees(f.lead, os.Environ()); err != nil || len(lines) != 0 || !bytes.Equal(f.read(t), adopted) {
		t.Fatalf("second sweep = %q, %v", lines, err)
	}
	git(t, f.lead, "worktree", "remove", "--force", f.worktree)
	lines, err = Trust{}.AdoptWorktrees(f.lead, os.Environ())
	// The line spells the folder as git listed it, which on Windows can be the
	// long form of a short temporary path, so only the folder's name is
	// compared.
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], filepath.Join(".worktrees", filepath.Base(f.worktree))) || !strings.Contains(lines[0], "no longer exists") {
		t.Fatalf("prune lines = %q, %v", lines, err)
	}
	if after := f.read(t); !bytes.Equal(after, before) {
		t.Fatalf("pruned config:\n%s\nwant:\n%s", after, before)
	}
}

// The sweep never takes out a block whose worktree exists, a block for a
// removed worktree whose tables someone changed, or a table duolog did not
// write.
func TestAdoptWorktreesKeepsLiveAndForeignTables(t *testing.T) {
	f := newAdoptFixture(t)
	if _, err := (Trust{}).AdoptWorktree(f.lead, f.worktree, os.Environ()); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(f.lead, ".worktrees", "gone")
	foreign := fmt.Sprintf("\n[projects.%s]\ntrust_level = \"trusted\"\n", quote(filepath.Join(f.lead, ".worktrees", "other-gone")))
	// A duolog block for a removed worktree whose approval no longer holds
	// the source's hash is not duolog's to remove.
	changed := fmt.Sprintf("\n%s%s%s%s%s\n[hooks.state.%s]\ntrusted_hash = \"sha256:edited\"\n%s\n",
		commentPrefix, gone, commentMiddle, f.lead, commentSuffix,
		quote(filepath.Join(gone, ".codex", "hooks.json")+":pre_tool_use:0:0"), commentEnd)
	writeFile(t, f.config, string(f.read(t))+foreign+changed)
	before := f.read(t)
	lines, err := Trust{}.AdoptWorktrees(f.lead, os.Environ())
	if err != nil || len(lines) != 0 || !bytes.Equal(f.read(t), before) {
		t.Fatalf("sweep changed the config: %q, %v\n%s", lines, err, f.read(t))
	}
	if edited, gone := pruneBlocks(before); len(gone) != 0 || !bytes.Equal(edited, before) {
		t.Fatalf("pruned %q", gone)
	}
}

// Repositories outside the project root are not swept.
func TestAdoptWorktreesStaysBelowRoot(t *testing.T) {
	f := newAdoptFixture(t)
	before := f.read(t)
	if lines, err := (Trust{}).AdoptWorktrees(t.TempDir(), os.Environ()); err != nil || len(lines) != 0 || !bytes.Equal(f.read(t), before) {
		t.Fatalf("sweep outside root = %q, %v", lines, err)
	}
	if !(Trust{}).SweepCovers(f.lead, f.worktree, os.Environ()) {
		t.Fatal("sweep would not cover the worktree")
	}
	if (Trust{}).SweepCovers(t.TempDir(), f.worktree, os.Environ()) {
		t.Fatal("sweep covers a worktree outside root")
	}
}

// The sweep renews a gate approval that duolog's install made stale, where
// the config still holds the recorded hash and the hook at its place is
// duolog's, and drops it from the stale file. A stale key whose hash the
// person changed, or whose hook is not duolog's, stays.
func TestAdoptWorktreesRenewsStaleApproval(t *testing.T) {
	codexHome := adoptHome(t)
	own, foreign := t.TempDir(), t.TempDir()
	generated, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	writeHook(t, own, string(generated))
	writeHook(t, foreign, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"other"}]}]}}`)
	ownKey := filepath.Join(own, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	movedKey := filepath.Join(own, ".codex", "hooks.json") + ":permission_request:0:0"
	foreignKey := filepath.Join(foreign, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	config := "# kept\n" +
		fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = \"sha256:stale\"\n\n", quote(ownKey)) +
		fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = \"sha256:approved-again\"\n\n", quote(movedKey)) +
		fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = \"sha256:stale\"\n", quote(foreignKey))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	stale := map[string]string{ownKey: "sha256:stale", movedKey: "sha256:stale", foreignKey: "sha256:stale"}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexHome, "duolog-stale-approvals.json"), string(data))

	lines, err := Trust{}.AdoptWorktrees(own, os.Environ())
	if err != nil || len(lines) != 1 || !strings.Contains(lines[0], "PreToolUse") {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	want, err := hookHash(generated, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	after := mustRead(t, filepath.Join(codexHome, "config.toml"))
	if wantText := strings.Replace(config, `"sha256:stale"`, quote(want), 1); after != wantText {
		t.Fatalf("config:\n%s\nwant:\n%s", after, wantText)
	}
	left := install.StaleApprovalsIn(os.Getenv("HOME"), os.Environ())
	if _, ok := left[ownKey]; ok || left[movedKey] != "sha256:stale" || left[foreignKey] != "sha256:stale" {
		t.Fatalf("stale file = %v", left)
	}
	if s, err := hooks(own); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("renewed gate = %+v, %v", s, err)
	}
}

// withForeignGroup returns the hooks document data with a PreToolUse group of
// one foreign handler running command added after duolog's.
func withForeignGroup(t *testing.T, data []byte, command string) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	hooks := doc["hooks"].(map[string]any)
	hooks["PreToolUse"] = append(hooks["PreToolUse"].([]any), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}})
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Install approves again each of duolog's own hooks that it changed where the
// person had approved exactly the hook it replaced, for every event, and
// leaves a foreign hook's approval and an approval of another hash alone.
func TestRenewApprovalsAfterInstall(t *testing.T) {
	codexHome := adoptHome(t)
	home := os.Getenv("HOME")
	root := t.TempDir()
	older, err := HooksJSON(150)
	if err != nil {
		t.Fatal(err)
	}
	current, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	old, updated := withForeignGroup(t, older, "other"), withForeignGroup(t, current, "other --changed")
	path := filepath.Join(root, ".codex", "hooks.json")
	key := func(event, place string) string { return path + ":" + event + ":" + place }
	hash := func(data []byte, event string, g int) string {
		t.Helper()
		h, err := hookHash(data, event, g, 0)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	approvals := map[string]string{
		key("pre_tool_use", "0:0"):       hash(old, "PreToolUse", 0),
		key("permission_request", "0:0"): hash(old, "PermissionRequest", 0),
		key("pre_tool_use", "1:0"):       hash(old, "PreToolUse", 1),
		key("stop", "0:0"):               "sha256:another",
	}
	config := "# the person's comment\napproval_policy = \"on-request\"\n"
	for _, k := range sortedKeys(approvals) {
		config += fmt.Sprintf("\n[hooks.state.%s]\ntrusted_hash = %s\n", quote(k), quote(approvals[k]))
	}
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	writeFile(t, path, string(updated))

	lines, err := Installer{}.RenewApprovals(home, path, old, updated)
	if err != nil || len(lines) != 2 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	var cfg adoptConfig
	after := mustRead(t, filepath.Join(codexHome, "config.toml"))
	if err := toml.Unmarshal([]byte(after), &cfg); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		key("pre_tool_use", "0:0"):       hash(updated, "PreToolUse", 0),
		key("permission_request", "0:0"): hash(updated, "PermissionRequest", 0),
		key("pre_tool_use", "1:0"):       approvals[key("pre_tool_use", "1:0")],
		key("stop", "0:0"):               "sha256:another",
	}
	for k, h := range want {
		if got := cfg.Hooks.State[k]["trusted_hash"]; got != h {
			t.Errorf("%s = %v, want %s", k, got, h)
		}
	}
	if !strings.HasPrefix(after, "# the person's comment\napproval_policy = \"on-request\"\n") {
		t.Fatalf("head changed:\n%s", after)
	}
	if backup := mustRead(t, filepath.Join(codexHome, "config.toml"+configBackupSuffix)); backup != config {
		t.Fatalf("backup = %q", backup)
	}
	// The gate changed, so the foreign approval of the file is stale; the
	// renewed gate is not.
	stale := install.StaleApprovals(home)
	if _, ok := stale[key("pre_tool_use", "0:0")]; ok || stale[key("pre_tool_use", "1:0")] == "" {
		t.Fatalf("stale = %v", stale)
	}
	if unrenewable := install.UnrenewableApprovalsIn(home, os.Environ()); len(unrenewable) != 1 || unrenewable[key("pre_tool_use", "1:0")] == "" {
		t.Fatalf("unrenewable = %v", unrenewable)
	}
	if s, err := hooks(root); err != nil || !s.Trusted || s.Unproven != "" {
		t.Fatalf("gate after install = %+v, %v", s, err)
	}
}

// An approval of a hook that was not duolog's at its place stays as it is,
// even when duolog's hook is there now.
func TestRenewApprovalsNeedsDuologsOldHook(t *testing.T) {
	codexHome := adoptHome(t)
	root := t.TempDir()
	current, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	old := []byte(strings.ReplaceAll(string(current), "duolog gate --agent", "duolog gate --old --agent"))
	path := filepath.Join(root, ".codex", "hooks.json")
	approved, err := hookHash(old, "PreToolUse", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("[hooks.state.%s]\ntrusted_hash = %s\n", quote(path+":pre_tool_use:0:0"), quote(approved))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	writeFile(t, path, string(current))
	lines, err := Installer{}.RenewApprovals(os.Getenv("HOME"), path, old, current)
	if err != nil || len(lines) != 0 || mustRead(t, filepath.Join(codexHome, "config.toml")) != config {
		t.Fatalf("renewed a foreign approval: %q, %v", lines, err)
	}
	if s, err := hooks(root); err != nil || !strings.Contains(s.Unproven, "approve it again in /hooks") {
		t.Fatalf("not stale: %+v, %v", s, err)
	}
}

// rehash changes only the trusted_hash line it is asked to, and nothing when
// the line carries anything else.
func TestRehashEditsOnlyTheHash(t *testing.T) {
	data := []byte("a = 1\r\n[hooks.state.\"/r/.codex/hooks.json:stop:0:0\"]\r\n  trusted_hash = \"sha256:old\"\r\nenabled = true\r\n")
	edited, done, ok := rehash(data, []hashChange{{key: "/r/.codex/hooks.json:stop:0:0", old: "sha256:old", hash: "sha256:new"}})
	if !ok || len(done) != 1 || string(edited) != strings.Replace(string(data), "sha256:old", "sha256:new", 1) {
		t.Fatalf("edited = %q, %v", edited, ok)
	}
	commented := []byte("[hooks.state.\"/r/.codex/hooks.json:stop:0:0\"]\ntrusted_hash = \"sha256:old\" # mine\n")
	if _, _, ok := rehash(commented, []hashChange{{key: "/r/.codex/hooks.json:stop:0:0", old: "sha256:old", hash: "sha256:new"}}); ok {
		t.Fatal("rehash edited a commented line")
	}
	other := []byte("[hooks.state.\"/r/.codex/hooks.json:stop:0:0\"]\ntrusted_hash = \"sha256:else\"\n")
	if _, _, ok := rehash(other, []hashChange{{key: "/r/.codex/hooks.json:stop:0:0", old: "sha256:old", hash: "sha256:new"}}); ok {
		t.Fatal("rehash edited another hash")
	}
}
