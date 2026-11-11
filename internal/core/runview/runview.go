// Package runview turns the lines of a worker's stdout log into what a person
// reads: the worker's messages, and one short line per tool call. It reads
// Claude's stream-json events and Codex's JSON events, and skips any line it
// does not know. It never keeps tool output.
package runview

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxToolRunes is the longest tool line, the marker and the name included.
const maxToolRunes = 100

// toolArguments are the input keys of a Claude tool call that name what it
// acts on, in the order they are tried.
var toolArguments = []string{"command", "file_path", "path", "notebook_path", "pattern", "url", "query"}

// Renderer renders the lines of one log, in order. It remembers the Codex
// items it has shown and that have not completed, so that an item's start and
// its end print once; it forgets an item when it completes.
type Renderer struct{ seen map[string]bool }

// NewRenderer is a Renderer that has shown the Codex items open, as a reader
// that renders a log in separate calls carries them from one call to the next.
func NewRenderer(open []string) Renderer {
	r := Renderer{}
	for _, id := range open {
		if id == "" {
			continue
		}
		if r.seen == nil {
			r.seen = map[string]bool{}
		}
		r.seen[id] = true
	}
	return r
}

// Close forgets the open Codex item id, as its completion would: a reader
// calls it for a completion it cannot render, such as one too long to read.
func (r *Renderer) Close(id string) { delete(r.seen, id) }

// Open returns the sorted ids of the Codex items shown and not completed, or
// nil.
func (r *Renderer) Open() []string {
	var ids []string
	for id := range r.seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Render returns the lines to print for one log line: none for an event it
// does not know, a line that is not JSON, or an event that carries no message
// and no tool call.
func (r *Renderer) Render(line []byte) []string {
	var event struct {
		Type    string          `json:"type"`
		Message json.RawMessage `json:"message"`
		Item    json.RawMessage `json:"item"`
	}
	if json.Unmarshal(line, &event) != nil {
		return nil
	}
	switch event.Type {
	case "assistant":
		return claudeLines(event.Message)
	case "item.started", "item.completed":
		return r.codexLines(event.Type == "item.completed", event.Item)
	}
	return nil
}

// claudeLines renders the text and tool_use blocks of an assistant message.
func claudeLines(message json.RawMessage) []string {
	var m struct {
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(message, &m) != nil {
		return nil
	}
	var out []string
	for _, raw := range m.Content {
		var block struct {
			Type  string                     `json:"type"`
			Text  string                     `json:"text"`
			Name  string                     `json:"name"`
			Input map[string]json.RawMessage `json:"input"`
		}
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		switch block.Type {
		case "text":
			if text := Clean(block.Text); text != "" {
				out = append(out, text)
			}
		case "tool_use":
			out = append(out, toolLine(block.Name, claudeTarget(block.Input)))
		}
	}
	return out
}

// claudeTarget is the first string among the known input keys of a tool call.
func claudeTarget(input map[string]json.RawMessage) string {
	for _, key := range toolArguments {
		var s string
		if json.Unmarshal(input[key], &s) == nil && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// codexLines renders a Codex item. A message prints when it completes; a
// tool call prints at the first event that names its id.
func (r *Renderer) codexLines(completed bool, raw json.RawMessage) []string {
	var item struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Text    string `json:"text"`
		Command string `json:"command"`
		Server  string `json:"server"`
		Tool    string `json:"tool"`
		Query   string `json:"query"`
		Changes []struct {
			Path string `json:"path"`
		} `json:"changes"`
	}
	if json.Unmarshal(raw, &item) != nil {
		return nil
	}
	var name, target string
	switch item.Type {
	case "agent_message":
		if text := Clean(item.Text); completed && text != "" {
			return []string{text}
		}
		return nil
	case "command_execution":
		name, target = "command", item.Command
	case "mcp_tool_call":
		name, target = "mcp", strings.Trim(item.Server+" "+item.Tool, " ")
	case "web_search":
		name, target = "web_search", item.Query
	case "file_change":
		name = "file_change"
		for _, c := range item.Changes {
			target += " " + c.Path
		}
	default:
		return nil
	}
	if r.shown(item.ID, completed) {
		return nil
	}
	return []string{toolLine(name, target)}
}

// shown reports whether the tool item id was shown before, and remembers it
// while it is open: a start marks it, its end forgets it. An item without an
// id is never shown before.
func (r *Renderer) shown(id string, completed bool) bool {
	if id == "" {
		return false
	}
	seen := r.seen[id]
	switch {
	case completed:
		delete(r.seen, id)
	case !seen:
		if r.seen == nil {
			r.seen = map[string]bool{}
		}
		r.seen[id] = true
	}
	return seen
}

// toolLine is "> name target", with the target on one line and cut so that
// the whole line has at most maxToolRunes runes.
func toolLine(name, target string) string {
	line := "> " + strings.Join(strings.Fields(Clean(name)), " ")
	target = strings.Join(strings.Fields(Clean(target)), " ")
	if target == "" {
		return line
	}
	line += " " + target
	if utf8.RuneCountInString(line) > maxToolRunes {
		line = string([]rune(line)[:maxToolRunes-3]) + "..."
	}
	return line
}

// Clean drops the control characters of text except newline and tab, C1
// controls such as U+009B included, so that a worker's output cannot move the
// cursor or retitle the person's terminal, and trims its ends.
func Clean(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == utf8.RuneError) {
			return -1
		}
		return r
	}, text))
}
