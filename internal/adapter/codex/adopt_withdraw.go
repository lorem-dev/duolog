package codex

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/store"
)

// The comment that AdoptWorktree writes above its tables:
// commentPrefix, the worktree, commentMiddle, the source root, commentSuffix.
// It marks the tables as duolog's, so that duolog can take them out again.
const (
	commentPrefix = "# duolog: "
	commentMiddle = " is a linked worktree of "
	commentSuffix = ", which Codex trusts with duolog's hooks approved."
	// commentEnd closes the block: no table after it is duolog's.
	commentEnd = "# duolog: end"
)

// recheck takes out the trust that duolog wrote for the worktree, with the
// approvals it copied there, once the worktree's .codex tree differs from the
// tree of the root whose trust it copied. done is true when it found such a
// trust; err then wraps agent.ErrWorktreeChanged and says why, and lines say
// what was taken out.
func (a adopter) recheck() (lines []string, done bool, err error) {
	// A config that cannot be read holds no trust to take out.
	var w withdrawal
	ok := false
	if data, readErr := store.ReadFile(a.path); readErr == nil {
		w, ok = a.withdrawal(data)
	}
	if !ok {
		return nil, false, nil
	}
	lines, err = a.change(func(data []byte) (configChange, bool, error) {
		w, ok := a.withdrawal(data)
		if !ok {
			return configChange{}, false, nil
		}
		edited, ok := w.apply(data)
		if !ok {
			return configChange{}, false, fmt.Errorf("duolog could not take its tables for %s out of %s; remove them yourself", a.dir, a.path)
		}
		return configChange{edited: edited, lines: w.lines}, true, nil
	})
	why := fmt.Errorf("%w: %s is not the same in %s as in %s, and Codex loads it in a folder it trusts; Codex does not run there until you have checked it and trusted the folder yourself (/hooks there)",
		agent.ErrWorktreeChanged, w.name, a.dir, w.from)
	return lines, true, errors.Join(why, err)
}

// withdrawal is what recheck takes out of the config: duolog's blocks for
// dir, one of which trusts it, and the path of the .codex tree that differs
// from from's.
type withdrawal struct {
	dir, from, name string
	blocks          []duologBlock
}

// withdrawal finds duolog's blocks for the worktree in data. ok is true when
// one of them trusts it and its .codex tree differs from the source root's.
func (a adopter) withdrawal(data []byte) (withdrawal, bool) {
	var cfg adoptConfig
	if toml.Unmarshal(data, &cfg) != nil {
		return withdrawal{}, false
	}
	w := withdrawal{dir: a.dir, blocks: duologBlocks(data, a.dir, cfg.Hooks.State)}
	for _, b := range w.blocks {
		if b.trustKey != "" {
			w.from = b.from
			break
		}
	}
	if w.from == "" {
		return withdrawal{}, false
	}
	name, same := sameCodexTree(w.from, a.dir)
	if same {
		return withdrawal{}, false
	}
	w.name = name
	return w, true
}

// tables are the trust and approvals of w's blocks, as one adoption.
func (w withdrawal) tables() adoption {
	p := adoption{dir: w.dir, from: w.from}
	for _, b := range w.blocks {
		if b.trustKey != "" {
			p.trustKey = b.trustKey
		}
		p.approvals = append(p.approvals, b.approvals...)
	}
	return p
}

// apply cuts w's blocks out of data. ok is false unless the result reads as
// data less exactly their tables.
func (w withdrawal) apply(data []byte) ([]byte, bool) {
	var edited []byte
	last := 0
	for _, b := range w.blocks {
		edited = append(edited, data[last:b.start]...)
		last = b.end
	}
	edited = append(edited, data[last:]...)
	var before, after map[string]any
	if toml.Unmarshal(data, &before) != nil || toml.Unmarshal(edited, &after) != nil {
		return nil, false
	}
	if after == nil {
		after = map[string]any{}
	}
	if !w.tables().remove(before, after) || !reflect.DeepEqual(before, after) {
		return nil, false
	}
	return edited, true
}

// lines say what the withdrawal took out of the config file at path.
func (w withdrawal) lines(path string) []string {
	p := w.tables()
	var out []string
	if p.trustKey != "" {
		out = append(out, fmt.Sprintf("Codex no longer trusts the linked worktree %s: %s is not the same as in %s (%s)", w.dir, w.name, w.from, path))
	}
	for _, a := range p.approvals {
		out = append(out, fmt.Sprintf("Codex no longer runs duolog's %s hook %s in %s (%s)", a.event, a.position, w.dir, path))
	}
	return out
}

// duologBlock is a run of tables in config.toml that AdoptWorktree wrote for
// one worktree: its comment, with the blank line before it, and the tables
// after it, from byte start to byte end. from is the source root the comment
// names.
type duologBlock struct {
	start, end int
	from       string
	trustKey   string
	approvals  []approvalCopy
}

// configLine is one line of the config file: its bytes from start to end,
// line end included, and its text without the line end.
type configLine struct {
	start, end int
	text       string
}

func configLines(data []byte) []configLine {
	var lines []configLine
	for start := 0; start < len(data); {
		end := len(data)
		if i := bytes.IndexByte(data[start:], '\n'); i >= 0 {
			end = start + i + 1
		}
		text := strings.TrimSuffix(strings.TrimSuffix(string(data[start:end]), "\n"), "\r")
		lines = append(lines, configLine{start: start, end: end, text: text})
		start = end
	}
	return lines
}

// duologBlocks finds the blocks that AdoptWorktree wrote for dir in data,
// where state is the config's hook approvals. A block holds exactly what
// AdoptWorktree writes, up to its end marker: dir's trust, first if at all,
// then approvals copied from the source root's approvals in state, each with
// the same position and hash. It stops at the first table that differs, which
// stays with everything after it, and never reaches past its end marker.
func duologBlocks(data []byte, dir string, state map[string]map[string]any) []duologBlock {
	lines := configLines(data)
	var blocks []duologBlock
	for i := 0; i < len(lines); i++ {
		from, ok := commentFrom(lines[i].text, dir)
		if !ok {
			continue
		}
		b := duologBlock{start: lines[i].start, end: lines[i].end, from: from}
		if i > 0 && lines[i-1].text == "" {
			b.start = lines[i-1].start
		}
		if next := b.takeTables(data, lines, i+1, dir, state); b.trustKey != "" || len(b.approvals) > 0 {
			blocks = append(blocks, b)
			i = next - 1
		}
	}
	return blocks
}

// takeTables takes the tables from line next on into the block, each after
// any blank lines, while they hold what AdoptWorktree writes, and the end
// marker after them. It returns the line after the last one taken.
func (b *duologBlock) takeTables(data []byte, lines []configLine, next int, dir string, state map[string]map[string]any) int {
	for {
		k := next
		for k < len(lines) && lines[k].text == "" {
			k++
		}
		if k < len(lines) && lines[k].text == commentEnd && (b.trustKey != "" || len(b.approvals) > 0) {
			b.end = lines[k].end
			return k + 1
		}
		if k == len(lines) || !tableHeader(lines[k].text) {
			return next
		}
		m := k + 1
		for m < len(lines) && tableBody(lines[m].text) {
			m++
		}
		if !b.take(data[lines[k].start:lines[m-1].end], dir, state) {
			return next
		}
		b.end = lines[m-1].end
		next = m
	}
}

// tableBody reports whether line goes on the table above it: neither blank,
// nor a comment, nor another table.
func tableBody(line string) bool {
	return line != "" && !tableHeader(line) && !strings.HasPrefix(strings.TrimSpace(line), "#")
}

// tableHeader reports whether line opens a table.
func tableHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[[")
}

// commentFrom reads duolog's comment line for dir and returns the source root
// it names.
func commentFrom(line, dir string) (string, bool) {
	body, ok := strings.CutPrefix(line, commentPrefix)
	if !ok {
		return "", false
	}
	if body, ok = strings.CutSuffix(body, commentSuffix); !ok {
		return "", false
	}
	for i := 0; i < len(body); {
		k := strings.Index(body[i:], commentMiddle)
		if k < 0 {
			break
		}
		k += i
		if from := body[k+len(commentMiddle):]; plainPath(from) && filepath.IsAbs(from) && samePath(body[:k], dir) {
			return from, true
		}
		i = k + 1
	}
	return "", false
}

// take adds the one table of segment to the block when it holds what
// AdoptWorktree writes for dir: dir's trust, or the copy of an approval in
// state.
func (b *duologBlock) take(segment []byte, dir string, state map[string]map[string]any) bool {
	var m map[string]any
	if toml.Unmarshal(segment, &m) != nil || len(m) != 1 {
		return false
	}
	if projects, ok := m["projects"].(map[string]any); ok {
		return b.takeTrust(projects, dir)
	}
	hooks, _ := m["hooks"].(map[string]any)
	return b.takeApproval(hooks, dir, state)
}

// takeTrust takes the one projects table that trusts dir, as the first table
// of the block.
func (b *duologBlock) takeTrust(projects map[string]any, dir string) bool {
	if len(projects) != 1 || b.trustKey != "" || len(b.approvals) > 0 {
		return false
	}
	for key, v := range projects {
		if !samePath(key, dir) || !reflect.DeepEqual(v, map[string]any{"trust_level": "trusted"}) {
			return false
		}
		b.trustKey = key
	}
	return true
}

// takeApproval takes the one hooks.state table that approves a duolog hook
// event in dir's hooks file as AdoptWorktree copied it: once per block, from
// an approval in state of the block's source root at the same position, with
// the same hash.
func (b *duologBlock) takeApproval(hooks map[string]any, dir string, state map[string]map[string]any) bool {
	table, _ := hooks["state"].(map[string]any)
	if len(hooks) != 1 || len(table) != 1 {
		return false
	}
	for key, v := range table {
		entry, _ := v.(map[string]any)
		path, spelled, position, ok := splitApproval(key)
		event := codexEvents()[spelled]
		if _, _, okEntry := approvalEntry(entry); !ok || !okEntry || event == "" || !samePath(path, filepath.Join(dir, ".codex", "hooks.json")) {
			return false
		}
		if b.approved(key) || !copiedFrom(state, b.from, dir, key, entry) {
			return false
		}
		b.approvals = append(b.approvals, approvalCopy{key: key, event: event, position: position, entry: entry})
	}
	return true
}

// approved reports whether the block has taken the approval key already.
func (b *duologBlock) approved(key string) bool {
	return slices.ContainsFunc(b.approvals, func(a approvalCopy) bool { return a.key == key })
}

// copiedFrom reports whether state holds an approval in from's hooks file of
// which AdoptWorktree writes key, with entry, for dir.
func copiedFrom(state map[string]map[string]any, from, dir, key string, entry map[string]any) bool {
	source := filepath.Join(from, ".codex", "hooks.json")
	for k, v := range state {
		path, spelled, position, ok := splitApproval(k)
		if ok && samePath(path, source) && copyKey(path, spelled, position, dir) == key && reflect.DeepEqual(v, entry) {
			return true
		}
	}
	return false
}
