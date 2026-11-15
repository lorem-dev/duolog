package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/trusthome"
)

// hashChange is a new trusted_hash for the approval key, which holds old.
type hashChange struct {
	key, old, hash string
	event, place   string
}

// RenewApprovals runs after duolog install replaced old with updated in the
// Codex hooks file at path, under home. Codex skips a hook whose approval
// hash no longer matches it. For each approval of that file whose handler,
// in old, was in the form duolog install writes and whose hash is exactly
// that handler's (ownApproval), and whose handler at the same place in
// updated is in that form for the same event, it writes the hash of the new
// handler: the person approved what duolog wrote there, and duolog wrote its
// successor. Every other approval stays as it is. When the gate command
// changed, the gate approvals of the file that were not renewed are recorded
// as stale, as before, and those that did not approve duolog's own hook as it
// wrote it are recorded as never to be renewed (install.MarkStaleApprovalsFor).
// Renewal is best-effort: when it fails, a warning line says so and the stale
// marking still runs. The error is only that of the stale marking.
func (i Installer) RenewApprovals(home, path string, old, updated []byte) ([]string, error) {
	before, okOld := parseHooksDoc(old)
	after, okNew := parseHooksDoc(updated)
	var lines []string
	var renewed []hashChange
	if okOld && okNew {
		var err error
		lines, renewed, err = reapprove(configPath(trusthome.Codex(home)), path, before, after)
		if err != nil {
			lines = append(lines, fmt.Sprintf("warning: Codex: duolog could not renew the approvals of its changed hooks in %s: %v; approve them in /hooks", path, err))
		}
	}
	if !i.GateChanged(old, updated) {
		return lines, nil
	}
	fresh := map[string]string{}
	for _, c := range renewed {
		fresh[c.key] = c.hash
	}
	renewable := func(key, hash string) bool {
		_, spelled, place, ok := splitApproval(key)
		event := codexEvents()[spelled]
		return okOld && ok && event != "" && ownApproval(before, event, place, hash)
	}
	return lines, install.MarkStaleApprovalsFor(home, path, fresh, renewable)
}

// reapprove writes the new hashes RenewApprovals describes to the config file
// at config and returns the lines and the changes it wrote.
func reapprove(config, hooksPath string, before, after hooksDoc) ([]string, []hashChange, error) {
	var done []hashChange
	lines, err := adopter{path: config}.change(func(data []byte) (configChange, bool, error) {
		done = nil
		edited, changed, ok := rehash(data, renewals(data, hooksPath, before, after))
		if !ok {
			return configChange{}, false, nil
		}
		done = changed
		return configChange{edited: edited, lines: func(path string) []string {
			out := make([]string, 0, len(changed))
			for _, c := range changed {
				out = append(out, fmt.Sprintf("Codex runs duolog's changed %s hook %s in %s: duolog renewed the approval you gave the hook it replaced (%s)",
					c.event, c.place, filepath.Dir(filepath.Dir(hooksPath)), path))
			}
			return out
		}}, true, nil
	})
	if err != nil {
		done = nil
	}
	return lines, done, err
}

// renewals lists the approvals of the hooks file at hooksPath in the config
// data that RenewApprovals renews from before to after.
func renewals(data []byte, hooksPath string, before, after hooksDoc) []hashChange {
	var cfg adoptConfig
	if toml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	var changes []hashChange
	for _, key := range sortedKeys(cfg.Hooks.State) {
		path, spelled, place, ok := splitApproval(key)
		event := codexEvents()[spelled]
		if !ok || event == "" || !samePath(path, hooksPath) {
			continue
		}
		_, hash, approved := approvalEntry(cfg.Hooks.State[key])
		g, h := positionOf(place)
		if !approved || !ownApproval(before, event, place, hash) || !approvable(after, event, g, h) {
			continue
		}
		now, err := hookHash(after.data, event, g, h)
		if err != nil || now == hash {
			continue
		}
		changes = append(changes, hashChange{key: key, old: hash, hash: now, event: event, place: place})
	}
	return changes
}

// approvable reports whether doc holds at group g, handler h of event a
// handler in the form duolog install writes: a foreground command that runs
// bare duolog from PATH, in a group that covers every tool. The dev launcher
// form never counts here: Codex hashes only the command, not the script it
// runs, and install never writes that form. Adoption keeps its own rules for
// it (ownHandler).
func approvable(doc hooksDoc, event string, g, h int) bool {
	if !hasHandler(doc, event, g, h) {
		return false
	}
	group := doc.groups[event][g]
	if !group.CoversEveryTool() {
		return false
	}
	handler := group.Hooks[h]
	if handler.Type != "command" || handler.Async || handler.AsyncRewake {
		return false
	}
	return ownCodexHandler(event, handler.Command, handler.CommandWindows, handler.Args, false)
}

// ownApproval reports whether hash approves, at place of event, the handler
// that the hooks document doc holds there in the form duolog install writes:
// the person approved exactly what duolog wrote.
func ownApproval(doc hooksDoc, event, place, hash string) bool {
	g, h := positionOf(place)
	if !approvable(doc, event, g, h) {
		return false
	}
	was, err := hookHash(doc.data, event, g, h)
	return err == nil && was == hash
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// rehash writes in data, in place, the new trusted_hash of each change whose
// approval table holds a line trusted_hash = <old> and nothing else on it. It
// changes no other byte. done lists the changes it made; ok is false when it
// made none, or unless the result reads as data with exactly those hashes
// changed.
func rehash(data []byte, changes []hashChange) (edited []byte, done []hashChange, ok bool) {
	if len(changes) == 0 {
		return nil, nil, false
	}
	want := map[string]hashChange{}
	for _, c := range changes {
		want[c.key] = c
	}
	last, current := 0, ""
	for _, l := range configLines(data) {
		if strings.HasPrefix(strings.TrimSpace(l.text), "[") {
			current = approvalHeader(l.text)
			continue
		}
		c, wanted := want[current]
		if current == "" || !wanted || !hashLine(l.text, c.old) {
			continue
		}
		indent := l.text[:len(l.text)-len(strings.TrimLeft(l.text, " \t"))]
		edited = append(edited, data[last:l.start]...)
		edited = append(edited, indent+"trusted_hash = "+quote(c.hash)...)
		last = l.start + len(l.text)
		done = append(done, c)
		delete(want, current)
	}
	if len(done) == 0 {
		return nil, nil, false
	}
	edited = append(edited, data[last:]...)
	if !onlyHashes(data, edited, done) {
		return nil, nil, false
	}
	return edited, done, true
}

// onlyHashes reports whether edited reads as data with the trusted_hash of
// each change in done changed from old to hash, and nothing else.
func onlyHashes(data, edited []byte, done []hashChange) bool {
	var before, after map[string]any
	if toml.Unmarshal(data, &before) != nil || toml.Unmarshal(edited, &after) != nil {
		return false
	}
	hooks, _ := before["hooks"].(map[string]any)
	state, _ := hooks["state"].(map[string]any)
	for _, c := range done {
		entry, _ := state[c.key].(map[string]any)
		if entry == nil || entry["trusted_hash"] != c.old {
			return false
		}
		entry["trusted_hash"] = c.hash
	}
	return reflect.DeepEqual(before, after)
}

// approvalHeader returns the approval key of the table header line, a table
// [hooks.state."<key>"], or "" for any other line.
func approvalHeader(line string) string {
	var m map[string]any
	if toml.Unmarshal([]byte(line), &m) != nil || len(m) != 1 {
		return ""
	}
	hooks, _ := m["hooks"].(map[string]any)
	state, _ := hooks["state"].(map[string]any)
	if len(hooks) != 1 || len(state) != 1 {
		return ""
	}
	for key, v := range state {
		if table, ok := v.(map[string]any); ok && len(table) == 0 {
			return key
		}
	}
	return ""
}

// hashLine reports whether line sets trusted_hash to hash and holds nothing
// else, not even a comment.
func hashLine(line, hash string) bool {
	if strings.Contains(line, "#") {
		return false
	}
	var m map[string]any
	return toml.Unmarshal([]byte(line), &m) == nil && len(m) == 1 && m["trusted_hash"] == hash
}

// renewStale writes, for each approval that duolog install recorded as stale
// (install.StaleApprovalsIn), the hash of the hook that is now at its place,
// when the config still holds the recorded hash and that hook is one duolog
// writes, in the form install writes, for the same event: the recorded hash
// shows that the person approved the hook there before duolog's install
// replaced it. A key that install recorded as never to be renewed
// (install.UnrenewableApprovalsIn), because its approval was not of duolog's
// own hook as duolog wrote it, stays stale. It then drops the renewed keys
// from the stale file. config is the config file, home and environ select the
// Codex home.
func renewStale(ctx context.Context, config, home string, environ []string) ([]string, error) {
	stale := install.StaleApprovalsIn(home, environ)
	for key, hash := range install.UnrenewableApprovalsIn(home, environ) {
		if stale[key] == hash {
			delete(stale, key)
		}
	}
	if len(stale) == 0 {
		return nil, nil
	}
	var done []hashChange
	lines, err := adopter{ctx: ctx, path: config}.change(func(data []byte) (configChange, bool, error) {
		done = nil
		edited, changed, ok := rehash(data, staleRenewals(data, stale))
		if !ok {
			return configChange{}, false, nil
		}
		done = changed
		return configChange{edited: edited, lines: func(path string) []string {
			out := make([]string, 0, len(changed))
			for _, c := range changed {
				hooksPath, _, _, _ := splitApproval(c.key)
				out = append(out, fmt.Sprintf("Codex runs duolog's %s hook %s in %s again: duolog renewed the approval that its install had made stale (%s)",
					c.event, c.place, filepath.Dir(filepath.Dir(hooksPath)), path))
			}
			return out
		}}, true, nil
	})
	if err != nil || len(done) == 0 {
		return lines, err
	}
	renewed := map[string]string{}
	for _, c := range done {
		renewed[c.key] = c.old
	}
	return lines, install.DropStaleApprovals(home, environ, renewed)
}

// staleRenewals lists the stale approvals in the config data that renewStale
// renews.
func staleRenewals(data []byte, stale map[string]string) []hashChange {
	var cfg adoptConfig
	if toml.Unmarshal(data, &cfg) != nil {
		return nil
	}
	var changes []hashChange
	for _, key := range sortedKeys(stale) {
		_, hash, approved := approvalEntry(cfg.Hooks.State[key])
		path, spelled, place, ok := splitApproval(key)
		event := codexEvents()[spelled]
		if !approved || hash != stale[key] || !ok || event == "" || !plainPath(path) || !filepath.IsAbs(path) {
			continue
		}
		doc, read := readHooksDoc(path)
		g, h := positionOf(place)
		if !read || !approvable(doc, event, g, h) {
			continue
		}
		now, err := hookHash(doc.data, event, g, h)
		if err != nil || now == hash {
			continue
		}
		changes = append(changes, hashChange{key: key, old: hash, hash: now, event: event, place: place})
	}
	return changes
}
