// Package install holds what the agents' installers share and what the
// readers of their settings need: the shape of a hooks document, the merge of
// duolog's hooks into a settings file, how duolog's own handlers are
// recognised, and the read side of Claude's settings, plugin and Codex's
// approvals. The installation itself is app/install, which asks each agent's
// Installer for its hooks.
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
)

// Handler is one hook command of a hooks document, as both agents write it.
type Handler struct {
	Type           string   `json:"type"`
	Command        string   `json:"command"`
	CommandWindows string   `json:"commandWindows,omitempty"`
	Args           []string `json:"args,omitempty"`
	Timeout        int      `json:"timeout,omitempty"`
	OnFailure      string   `json:"onFailure,omitempty"`
	AsyncRewake    bool     `json:"asyncRewake,omitempty"`
}

// Group is one entry of an event in a hooks document.
type Group struct {
	Hooks []Handler `json:"hooks"`
}

// Event is one named event with its groups.
type Event struct {
	Name   string
	Groups []Group
}

// Document renders the hooks document of one agent from its events, as the
// plugin ships it. modules, when given, are the plugin's hooks modules (the
// Claude mod), written under "modules"; a settings file takes none.
func Document(events []Event, modules ...string) ([]byte, error) {
	hooks := map[string][]Group{}
	for _, e := range events {
		hooks[e.Name] = e.Groups
	}
	doc := map[string]any{"hooks": hooks}
	if len(modules) > 0 {
		doc["modules"] = modules
	}
	return Encode(doc)
}

// Encode indents v without HTML escaping, so commands stay readable.
func Encode(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// withoutOurHandlers splits one group of a settings file. It returns the group
// with duolog's handlers removed, and whether it held any of them. rest is nil
// when nothing else is left. A foreign handler keeps its matcher and every field.
func withoutOurHandlers(raw json.RawMessage) (rest json.RawMessage, ours bool, err error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return raw, false, nil //nolint:nilerr // a foreign entry we cannot read is kept as it is
	}
	var handlers []json.RawMessage
	if json.Unmarshal(fields["hooks"], &handlers) != nil {
		return raw, false, nil //nolint:nilerr // a foreign entry we cannot read is kept as it is
	}
	var kept []json.RawMessage
	for _, item := range handlers {
		var h Handler
		if json.Unmarshal(item, &h) == nil && (IsOurCommand(h.Command) || IsOurCommand(h.CommandWindows)) {
			ours = true
			continue
		}
		kept = append(kept, item)
	}
	if !ours {
		return raw, false, nil
	}
	if len(kept) == 0 {
		return nil, true, nil
	}
	if fields["hooks"], err = Encode(kept); err != nil {
		return nil, false, err
	}
	rest, err = Encode(fields)
	return rest, true, err
}

// Merge returns the settings file whose bytes are existing (nil for a missing
// file) with our hook groups replaced by ours (or removed when ours is nil).
// Foreign keys and entries keep their order; our groups are replaced in place
// so a second run gives the same bytes.
func Merge(existing, ours []byte) ([]byte, error) {
	top := map[string]json.RawMessage{}
	if existing != nil {
		if err := json.Unmarshal(existing, &top); err != nil || top == nil {
			return nil, fmt.Errorf("not a JSON object, nothing changed: %v", err) //nolint:errorlint // err is nil when top is null, and %w would print %!w(<nil>)
		}
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := decodeHookGroups(raw, hooks); err != nil {
			return nil, fmt.Errorf("bad \"hooks\" value, nothing changed: %w", err)
		}
	}
	want := map[string][]json.RawMessage{}
	if ours != nil {
		var doc struct {
			Hooks map[string][]json.RawMessage `json:"hooks"`
		}
		if err := json.Unmarshal(ours, &doc); err != nil {
			return nil, err
		}
		want = doc.Hooks
	}
	names := map[string]bool{}
	for name := range hooks {
		names[name] = true
	}
	for name := range want {
		names[name] = true
	}
	for name := range names {
		var merged []json.RawMessage
		pending := want[name]
		for _, raw := range hooks[name] {
			rest, ours, err := withoutOurHandlers(raw)
			if err != nil {
				return nil, err
			}
			if rest != nil {
				merged = append(merged, rest)
			}
			if ours && rest == nil && len(pending) > 0 {
				merged = append(merged, pending[0])
				pending = pending[1:]
			}
		}
		merged = append(merged, pending...)
		if len(merged) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = merged
		}
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		raw, err := Encode(hooks)
		if err != nil {
			return nil, err
		}
		top["hooks"] = raw
	}
	return Encode(top)
}

// OurHooks returns duolog's own part of the settings or hooks file whose
// bytes are data (nil for a missing file): by event, each group that holds a
// duolog handler, with its other fields and only duolog's handlers, as compact
// JSON with sorted keys. Foreign handlers, groups and keys, and the layout of
// the file, are left out, so two files compare equal when duolog's hooks in
// them are the same. A file that is not a JSON object is an error, as in
// Merge.
func OurHooks(data []byte) (map[string][]string, error) {
	out := map[string][]string{}
	if data == nil {
		return out, nil
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return nil, fmt.Errorf("not a JSON object: %v", err) //nolint:errorlint // err is nil when top is null
	}
	hooks := map[string][]json.RawMessage{}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return nil, fmt.Errorf("bad \"hooks\" value: %w", err)
		}
	}
	for name, groups := range hooks {
		for _, raw := range groups {
			if part, ok := ourPart(raw); ok {
				out[name] = append(out[name], part)
			}
		}
	}
	return out, nil
}

// ourPart returns one group with only duolog's handlers, as compact JSON with
// sorted keys, and false when the group has none or cannot be read.
func ourPart(raw json.RawMessage) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var fields map[string]any
	if dec.Decode(&fields) != nil {
		return "", false
	}
	handlers, _ := fields["hooks"].([]any)
	var ours []any
	for _, item := range handlers {
		h, _ := item.(map[string]any)
		command, _ := h["command"].(string)
		windows, _ := h["commandWindows"].(string)
		if IsOurCommand(command) || IsOurCommand(windows) {
			ours = append(ours, item)
		}
	}
	if len(ours) == 0 {
		return "", false
	}
	fields["hooks"] = ours
	data, err := Encode(fields)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// IsOurCommand shares handler recognition with trust and startup checks.
func IsOurCommand(command string) bool {
	if rest, ok := devLauncherRest(command); ok {
		return hookVerb(rest)
	}
	// Generated Windows guards contain PowerShell syntax inside one argument.
	const ps = `powershell -NoProfile -NonInteractive -Command `
	if strings.HasPrefix(command, ps) {
		body := strings.TrimPrefix(command, ps)
		if strings.HasPrefix(body, `"& ([Environment]::SystemDirectory + '\where.exe') /Q ([char]36 + 'PATH:duolog'); if (`) {
			return strings.Contains(body, ") { duolog gate ") || strings.Contains(body, ") { duolog hook ")
		}
		return IsOurCommand(body)
	}
	for _, prefix := range []string{
		"if command -v duolog; then exec duolog ",
		"if (Get-Command duolog) { duolog ",
		// The Windows guard of older releases, so install replaces it.
		"if (Get-Command duolog -ErrorAction SilentlyContinue) { duolog ",
		"if command -v duolog >/dev/null 2>&1; then exec duolog ",
	} {
		if rest, ok := strings.CutPrefix(command, prefix); ok {
			return hookVerb(rest)
		}
	}
	words := strings.Fields(command)
	if len(words) > 0 && words[0] == "exec" {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	program := strings.Trim(words[0], "\"'")
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(program, `\`, "/")))
	if name != "duolog" && name != "duolog.exe" {
		return false
	}
	return len(words) == 1 || words[1] == "gate" || words[1] == "hook"
}

// devLauncherRest returns what follows the dev launcher in this repository's
// own Codex hooks, which run the checkout's duolog.
func devLauncherRest(command string) (string, bool) {
	for _, dev := range []string{
		`r=$(git rev-parse --show-toplevel 2>/dev/null); if [ -x "$r/scripts/dev/bin/duolog" ]; then exec "$r"/scripts/dev/bin/duolog `,
		`powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File scripts/dev/bin/duolog.ps1 `,
	} {
		if rest, ok := strings.CutPrefix(command, dev); ok {
			return rest, true
		}
	}
	return "", false
}

func hookVerb(command string) bool {
	verb, _, _ := strings.Cut(command, " ")
	return verb == "gate" || verb == "hook"
}

func decodeHookGroups(raw json.RawMessage, hooks map[string][]json.RawMessage) error {
	var parsed map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return err
	}
	maps.Copy(hooks, parsed)
	return nil
}
