package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// staleApprovalsFile lives in the Codex home, which a worker's writable roots
// may not reach. It maps a Codex hook approval key to the trusted_hash Codex
// held for it when duolog install last changed the gate command in that
// key's hooks file: that hash approves the older command, not the new one.
// It holds every such gate approval, whatever hook it approved; which of them
// duolog may renew is unrenewableApprovalsFile's business.
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
	return readApprovals(filepath.Join(trusthome.CodexIn(home, environ), staleApprovalsFile))
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

// GateCommands lists the gate handlers of a Codex hooks file with their
// positions, commands and timeouts; it changes exactly when one of those
// changes. Codex hashes the timeout into a hook's approval too.
func GateCommands(data []byte) string {
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
				timeout := "default"
				if h.Timeout != nil {
					timeout = fmt.Sprint(*h.Timeout)
				}
				fmt.Fprintf(&b, "%d:%d %q %q %q %q %s\n", i, j, group.Matcher, h.Command, h.CommandWindows, h.Args, timeout)
			}
		}
	}
	return b.String()
}

// unrenewableApprovalsFile lives next to staleApprovalsFile and has the same
// form. It holds the stale entries that duolog must never approve again by
// itself: when install marked them, their hash did not approve duolog's own
// hook as duolog wrote it at that place, such as a hook of another tool or
// an older gate guard. They stay in staleApprovalsFile too, so that trust
// never counts their hash for the new command, and an older duolog that does
// not know this file still reads them as stale. An entry of
// staleApprovalsFile that is not here, as an older duolog wrote them all, may
// be renewed, and only ever for a hook in the form duolog install writes.
const unrenewableApprovalsFile = "duolog-stale-unrenewable.json"

// UnrenewableApprovalsIn returns the stale approvals that duolog must not
// renew (unrenewableApprovalsFile) in the Codex home that home and environ
// select. A missing or broken file has none.
func UnrenewableApprovalsIn(home string, environ []string) map[string]string {
	return readApprovals(filepath.Join(trusthome.CodexIn(home, environ), unrenewableApprovalsFile))
}

// MarkStaleApprovals records, for each Codex approval of a gate position in
// hooksPath, the trusted_hash Codex holds now, after duolog changed the gate
// command there, none of them as renewable. Entries Codex has approved again
// since are dropped.
func MarkStaleApprovals(home, hooksPath string) error {
	return MarkStaleApprovalsFor(home, hooksPath, nil, nil)
}

// MarkStaleApprovalsFor records, for each Codex approval of a gate position
// in hooksPath, the trusted_hash Codex holds now, after duolog changed the
// gate command there: every such approval, whatever hook it approved, so that
// trust never counts it for the new command. fresh names the approvals that
// duolog renewed for the new command, with their new hash; they are not
// stale. renewable reports whether an approval with its hash approved
// duolog's own hook as duolog wrote it, in the hooks file before the change;
// those that do not, and all of them when renewable is nil, are also recorded
// as never to be renewed (unrenewableApprovalsFile). Entries Codex has
// approved again since are dropped from both files.
func MarkStaleApprovalsFor(home, hooksPath string, fresh map[string]string, renewable func(key, hash string) bool) error {
	codexHome := trusthome.Codex(home)
	unlock, err := lockApprovals(codexHome)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	current, err := configApprovals(codexHome)
	if err != nil {
		return err
	}
	stale := held(StaleApprovals(home), current)
	unrenewable := held(readApprovals(filepath.Join(codexHome, unrenewableApprovalsFile)), stale)
	target := pathutil.Clean(pathutil.Resolved(hooksPath))
	for key, hash := range current {
		path, _, ok := ApprovalPath(key)
		if renewed, isFresh := fresh[key]; isFresh && renewed == hash {
			delete(stale, key)
			delete(unrenewable, key)
			continue
		}
		if !ok || hash == "" || pathutil.Clean(pathutil.Resolved(path)) != target {
			continue
		}
		stale[key] = hash
		delete(unrenewable, key)
		if renewable == nil || !renewable(key, hash) {
			unrenewable[key] = hash
		}
	}
	if err := writeApprovals(codexHome, staleApprovalsFile, stale); err != nil {
		return err
	}
	return writeApprovals(codexHome, unrenewableApprovalsFile, unrenewable)
}

// configApprovals reads the trusted_hash of each hook approval in
// config.toml of codexHome; a missing file has none.
func configApprovals(codexHome string) (map[string]string, error) {
	var cfg struct {
		Hooks struct {
			State map[string]struct {
				TrustedHash string `toml:"trusted_hash"`
			} `toml:"state"`
		} `toml:"hooks"`
	}
	path := filepath.Join(codexHome, "config.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	current := map[string]string{}
	for key, state := range cfg.Hooks.State {
		current[key] = state.TrustedHash
	}
	return current, nil
}

// held returns the entries of recorded whose hash have still holds for the
// same key.
func held(recorded, have map[string]string) map[string]string {
	out := map[string]string{}
	for key, hash := range recorded {
		if current, ok := have[key]; ok && current == hash {
			out[key] = hash
		}
	}
	return out
}

// readApprovals reads a map of approval key to hash; a missing or broken file
// has none.
func readApprovals(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}

// DropStaleApprovals takes out of the stale approvals of the Codex home that
// home and environ select each key that renewed maps to the hash recorded
// for it: duolog has approved the hook at its place again.
func DropStaleApprovals(home string, environ []string, renewed map[string]string) error {
	codexHome := trusthome.CodexIn(home, environ)
	if _, err := os.Stat(filepath.Join(codexHome, staleApprovalsFile)); os.IsNotExist(err) {
		return nil
	}
	unlock, err := lockApprovals(codexHome)
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	stale := StaleApprovalsIn(home, environ)
	dropped := false
	for key, hash := range renewed {
		if recorded, ok := stale[key]; ok && recorded == hash {
			delete(stale, key)
			dropped = true
		}
	}
	if !dropped {
		return nil
	}
	return writeApprovals(codexHome, staleApprovalsFile, stale)
}

// writeApprovals replaces the file name in codexHome with stale in one
// rename, or removes it when stale is empty. The caller holds the lock of
// the stale approvals.
func writeApprovals(codexHome, name string, stale map[string]string) error {
	file := filepath.Join(codexHome, name)
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
	tmp, err := os.CreateTemp(codexHome, "."+name+".*.tmp")
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

func lockApprovals(dir string) (func() error, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return store.LockFile(filepath.Join(dir, staleApprovalsFile+".lock"))
}
