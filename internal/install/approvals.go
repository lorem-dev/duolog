package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// staleApprovalsFile lives in the Codex home, which a worker's writable roots
// may not reach. It maps a Codex hook approval key to the trusted_hash Codex
// held for it when duolog install last changed the gate command in that
// key's hooks file: that hash approves the older command, not the new one.
const staleApprovalsFile = "duolog-stale-approvals.json"

// StaleApprovals returns the approvals duolog install made stale under home:
// approval key to the trusted_hash that no longer approves the gate. A key
// whose hash Codex has since changed is approved again. A missing or broken
// file has none.
func StaleApprovals(home string) map[string]string {
	return StaleApprovalsIn(home, os.Environ())
}

// StaleApprovalsIn is StaleApprovals in the Codex home that a process whose
// environment is environ uses.
func StaleApprovalsIn(home string, environ []string) map[string]string {
	data, err := os.ReadFile(filepath.Join(trusthome.CodexIn(home, environ), staleApprovalsFile))
	if err != nil {
		return nil
	}
	var stale map[string]string
	if json.Unmarshal(data, &stale) != nil {
		return nil
	}
	return stale
}

// ApprovalPath splits a Codex PreToolUse approval key into its hooks file and
// position (group:handler). Codex stores the snake_case event name;
// hooks.json uses PascalCase.
func ApprovalPath(key string) (path, position string, ok bool) {
	for _, event := range []string{":pre_tool_use:", ":PreToolUse:"} {
		if path, position, ok = strings.Cut(key, event); ok {
			return path, position, true
		}
	}
	return "", "", false
}

// gateCommands lists the gate handlers of a Codex hooks file with their
// positions and commands; it changes exactly when a gate command changes.
func gateCommands(data []byte) string {
	var doc struct {
		Hooks map[string][]HookGroup `json:"hooks"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	var b strings.Builder
	for i, group := range doc.Hooks["PreToolUse"] {
		for j, h := range group.Hooks {
			if group.IsGate(j) {
				fmt.Fprintf(&b, "%d:%d %q %q %q %q\n", i, j, group.Matcher, h.Command, h.CommandWindows, h.Args)
			}
		}
	}
	return b.String()
}

// markStaleApprovals records, for each Codex approval of a gate position in
// hooksPath, the trusted_hash Codex holds now, after duolog changed the gate
// command there. Entries Codex has approved again since are dropped.
func markStaleApprovals(home, hooksPath string) error {
	codexHome := trusthome.Codex(home)
	var cfg struct {
		Hooks struct {
			State map[string]struct {
				TrustedHash string `toml:"trusted_hash"`
			} `toml:"state"`
		} `toml:"hooks"`
	}
	data, err := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := toml.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("%s: %w", filepath.Join(codexHome, "config.toml"), err)
		}
	}
	stale := map[string]string{}
	for key, hash := range StaleApprovals(home) {
		if current, ok := cfg.Hooks.State[key]; ok && current.TrustedHash == hash {
			stale[key] = hash
		}
	}
	target := pathutil.Clean(pathutil.Resolved(hooksPath))
	for key, state := range cfg.Hooks.State {
		path, _, ok := ApprovalPath(key)
		if ok && state.TrustedHash != "" && pathutil.Clean(pathutil.Resolved(path)) == target {
			stale[key] = state.TrustedHash
		}
	}
	file := filepath.Join(codexHome, staleApprovalsFile)
	if len(stale) == 0 {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	out, err := json.MarshalIndent(stale, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(codexHome, "."+staleApprovalsFile+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(append(out, '\n'))
	if closeErr := tmp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return store.Rename(tmp.Name(), file)
}
