package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/install"
)

// currentStaleFixture writes duolog's hooks file in a new project and a
// config whose gate approval holds the hash of that very hook, recorded as
// stale: a later install wrote the approved form again. It returns the
// project root, the approval key, the hash and the config text.
func currentStaleFixture(t *testing.T) (root, key, hash, config string) {
	t.Helper()
	codexHome := adoptHome(t)
	root = t.TempDir()
	generated, err := HooksJSON(install.GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	writeHook(t, root, string(generated))
	if hash, err = hookHash(generated, "PreToolUse", 0, 0); err != nil {
		t.Fatal(err)
	}
	key = filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:0:0"
	config = fmt.Sprintf("[projects.%s]\ntrust_level = \"trusted\"\n\n[hooks.state.%s]\ntrusted_hash = %s\n", quote(root), quote(key), quote(hash))
	writeFile(t, filepath.Join(codexHome, "config.toml"), config)
	data, err := json.Marshal(map[string]string{key: hash})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexHome, "duolog-stale-approvals.json"), string(data))
	return root, key, hash, config
}

// A stale record whose hash matches the hook now at its place does not make
// the gate unapproved: Codex runs that hook as it is.
func TestStaleRecordOfCurrentHookIsApproved(t *testing.T) {
	root, _, _, _ := currentStaleFixture(t)
	if s, err := hooks(root); err != nil || !s.Trusted || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("gate = %+v %v %q", s, err, (Trust{}).GateProblem(s, true))
	}
}

// The sweep drops such a record and writes nothing to the config, while a
// record whose hash does not match stays.
func TestSweepDropsStaleRecordOfCurrentHook(t *testing.T) {
	root, key, _, config := currentStaleFixture(t)
	codexHome := os.Getenv("CODEX_HOME")
	other := filepath.Join(root, ".codex", "hooks.json") + ":pre_tool_use:5:0"
	data, err := json.Marshal(map[string]string{key: mustStale(t)[key], other: "sha256:gone"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(codexHome, "duolog-stale-approvals.json"), string(data))
	lines, err := Trust{}.AdoptWorktrees(root, os.Environ())
	if err != nil || len(lines) != 0 {
		t.Fatalf("lines = %q, %v", lines, err)
	}
	if after := mustRead(t, filepath.Join(codexHome, "config.toml")); after != config {
		t.Fatalf("config changed:\n%s", after)
	}
	left := mustStale(t)
	if _, ok := left[key]; ok || left[other] != "sha256:gone" {
		t.Fatalf("stale file = %v", left)
	}
}

// Install does not record as stale an approval whose hash matches the hook
// it writes, as when it puts back the form the person approved.
func TestInstallKeepsApprovalOfTheHookItWritesBack(t *testing.T) {
	root, key, _, config := currentStaleFixture(t)
	codexHome := os.Getenv("CODEX_HOME")
	if err := os.Remove(filepath.Join(codexHome, "duolog-stale-approvals.json")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".codex", "hooks.json")
	updated := []byte(mustRead(t, path))
	older, err := HooksJSON(150)
	if err != nil {
		t.Fatal(err)
	}
	if !(Installer{}).GateChanged(older, updated) {
		t.Fatal("fixture did not change the gate")
	}
	if _, err := (Installer{}).RenewApprovals(os.Getenv("HOME"), path, older, updated); err != nil {
		t.Fatal(err)
	}
	if _, ok := mustStale(t)[key]; ok {
		t.Fatal("the approval of the hook install wrote back was marked stale")
	}
	if after := mustRead(t, filepath.Join(codexHome, "config.toml")); after != config {
		t.Fatalf("config changed:\n%s", after)
	}
	if s, err := hooks(root); err != nil || (Trust{}).GateProblem(s, true) != "" {
		t.Fatalf("gate = %+v %v", s, err)
	}
}

func mustStale(t *testing.T) map[string]string {
	t.Helper()
	return install.StaleApprovalsIn(os.Getenv("HOME"), os.Environ())
}
