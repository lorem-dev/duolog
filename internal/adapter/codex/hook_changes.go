package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
)

const changedHookApproval = "Codex skips the changed hook until you approve it in /hooks; duolog cannot approve it for you."

// HookChanges describes changes to duolog's entries in the already validated
// old and merged hooks documents. A first install keeps the usual setup note.
// It reads neither Codex config nor approvals.
func (Installer) HookChanges(old, updated []byte, path string) []string {
	before, err := install.OurHooks(old)
	if err != nil || len(before) == 0 {
		return nil
	}
	after, err := install.OurHooks(updated)
	if err != nil {
		return nil
	}
	names := maps.Clone(before)
	maps.Copy(names, after)
	var notes []string
	for _, event := range slices.Sorted(maps.Keys(names)) {
		if slices.Equal(before[event], after[event]) {
			continue
		}
		notes = append(notes, hookChangeNote(event, path, before[event], after[event]))
	}
	return notes
}

func hookChangeNote(event, path string, before, after []string) string {
	if len(before) == 0 {
		return fmt.Sprintf("duolog added %s in %s: new hook. Approve it in /hooks; duolog cannot approve it for you.", event, path)
	}
	if len(after) == 0 {
		return fmt.Sprintf("duolog removed %s in %s.", event, path)
	}
	oldFields, newFields := hookFields(before), hookFields(after)
	names := maps.Clone(oldFields)
	maps.Copy(names, newFields)
	var changes []string
	for _, field := range slices.Sorted(maps.Keys(names)) {
		oldValue, newValue := hookFieldValue(oldFields, field), hookFieldValue(newFields, field)
		if oldValue != newValue {
			changes = append(changes, field+" "+oldValue+" -> "+newValue)
		}
	}
	return fmt.Sprintf("duolog changed %s in %s: %s. %s", event, path, strings.Join(changes, "; "), changedHookApproval)
}

func hookFieldValue(fields map[string]string, field string) string {
	if value, ok := fields[field]; ok {
		return value
	}
	return "(absent)"
}

// hookFields records group and handler fields under distinct paths, including
// matcher and fields this version does not write: a group's own field is
// named group.<field>, and indices tell several groups or handlers apart.
func hookFields(groups []string) map[string]string {
	fields := map[string]string{}
	for i, raw := range groups {
		var group map[string]json.RawMessage
		if json.Unmarshal([]byte(raw), &group) != nil {
			continue
		}
		var handlers []map[string]json.RawMessage
		if json.Unmarshal(group["hooks"], &handlers) != nil {
			continue
		}
		delete(group, "hooks")
		prefix := hookEntryPrefix("groups", i, len(groups))
		addHookFields(fields, prefix+"group.", group)
		for j, handler := range handlers {
			addHookFields(fields, prefix+hookEntryPrefix("hooks", j, len(handlers)), handler)
		}
	}
	return fields
}

func hookEntryPrefix(name string, index, count int) string {
	if count == 1 {
		return ""
	}
	return fmt.Sprintf("%s[%d].", name, index)
}

func addHookFields(fields map[string]string, prefix string, values map[string]json.RawMessage) {
	for name, raw := range values {
		var compact bytes.Buffer
		if json.Compact(&compact, raw) == nil {
			fields[prefix+name] = compact.String()
		}
	}
}
