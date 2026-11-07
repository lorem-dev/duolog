package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/agent"
)

type instructionState struct {
	Created         bool   `json:"created,omitempty"`
	SeparatorPrefix string `json:"separator_prefix,omitempty"`
	AddedNewline    bool   `json:"added_newline,omitempty"`
}

// instructionPaths are the instruction files of every registered agent in the
// project at root: the file each reads now, and every place one could have
// been written. Both are listed by base name, so AGENTS.md comes before
// CLAUDE.md, and keep each agent's own order for the same name.
func (d Deps) instructionPaths(root string) (current, all []string) {
	for _, a := range d.Agents {
		file, files := a.Installer.InstructionFiles(root)
		current = append(current, file)
		for _, f := range files {
			if !contains(all, f) {
				all = append(all, f)
			}
		}
	}
	byName := func(list []string) {
		sort.SliceStable(list, func(i, j int) bool { return filepath.Base(list[i]) < filepath.Base(list[j]) })
	}
	byName(current)
	byName(all)
	return current, all
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// rebuild returns data with duolog's block replaced by block: in place when
// the file has one, else where the first one-line offer of an earlier version
// stood. Any other such line goes. placed is false when neither was there,
// and the caller then inserts the block. An empty block removes them all.
func rebuild(data, block []byte) (out []byte, placed bool, err error) {
	start, end, found, err := agent.FindBlock(data)
	if err != nil {
		return nil, false, err
	}
	if found && end > 0 && data[end-1] != '\n' {
		block = bytes.TrimRight(block, "\n")
	}
	offset := 0
	placed = found
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		switch {
		case found && offset == start:
			out = append(out, block...)
		case found && offset > start && offset < end:
		case bytes.HasPrefix(line, []byte(agent.LegacyOfferMarker)):
			if !placed {
				out = append(out, block...)
				placed = true
			}
		default:
			out = append(out, line...)
		}
		offset += len(line)
	}
	return out, placed, nil
}

func insertOffer(data []byte) ([]byte, bool) {
	offset := 0
	fenced := false
	fence := ""
	previous := ""
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		if strings.HasPrefix(text, "```") || strings.HasPrefix(text, "~~~") {
			if !fenced {
				fenced = true
				fence = text[:3]
			} else if strings.HasPrefix(text, fence) {
				fenced = false
			}
		}
		offset += len(line)
		heading := strings.TrimLeft(string(line), " ")
		n := len(heading) - len(strings.TrimLeft(heading, "#"))
		atx := n >= 1 && n <= 6 && (len(heading) == n || heading[n] == ' ' || heading[n] == '\t' || heading[n] == '\n' || heading[n] == '\r')
		setext := previous != "" && text != "" && (strings.Trim(text, "=") == "" || strings.Trim(text, "-") == "")
		if !fenced && len(string(line))-len(heading) <= 3 && (atx || setext) {
			prefix := append([]byte(nil), data[:offset]...)
			added := len(prefix) > 0 && prefix[len(prefix)-1] != '\n'
			if added {
				prefix = append(prefix, '\n')
			}
			prefix = append(prefix, []byte(agent.Block)...)
			return append(prefix, data[offset:]...), added
		}
		previous = text
	}
	return append([]byte(agent.Block), data...), false
}

func (d Deps) instructionEdits(root string) ([]edit, error) {
	statePath := filepath.Join(root, ".duolog", "instruction-files.json")
	state, err := readInstructionState(statePath)
	if err != nil {
		return nil, err
	}
	var edits []edit
	paths, _ := d.instructionPaths(root)
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		old, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		entry, known := state[rel]
		if !known {
			entry.Created = os.IsNotExist(err)
		}
		data, placed, err := rebuild(old, []byte(agent.Block))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if !placed {
			var added bool
			data, added = insertOffer(old)
			entry.AddedNewline = added
			entry.SeparatorPrefix = ""
			if added {
				entry.SeparatorPrefix = string(old)
			}
		}
		state[rel] = entry
		edits = append(edits, edit{path: path, data: data})
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(edits, edit{path: statePath, data: append(data, '\n')}), nil
}

func readInstructionState(path string) (map[string]instructionState, error) {
	state := map[string]instructionState{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("instruction ownership: %w", err)
	}
	if state == nil {
		state = map[string]instructionState{}
	}
	return state, nil
}

func (d Deps) removeInstructions(root string) error {
	statePath := filepath.Join(root, ".duolog", "instruction-files.json")
	state, err := readInstructionState(statePath)
	if err != nil {
		return err
	}
	// Every possible location, including a previously selected one.
	_, all := d.instructionPaths(root)
	// Read every file first: unpaired markers in one leave all of them as they are.
	type removal struct {
		path  string
		clean []byte
		entry instructionState
	}
	var removals []removal
	for _, path := range all {
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		clean, _, err := rebuild(data, nil)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !bytes.Equal(clean, data) {
			entry := state[rel]
			removals = append(removals, removal{path, removeSeparator(clean, entry), entry})
		}
	}
	for _, r := range removals {
		var err error
		if len(r.clean) == 0 && r.entry.Created {
			err = os.Remove(r.path)
		} else {
			_, err = apply(edit{path: r.path, data: r.clean, backup: projectBackup(root, r.path)})
		}
		if err != nil {
			return err
		}
	}
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Remove only the separator inserted after a heading without a final newline.
func removeSeparator(data []byte, entry instructionState) []byte {
	prefix := []byte(entry.SeparatorPrefix)
	if entry.AddedNewline && bytes.HasPrefix(data, append(append([]byte(nil), prefix...), '\n')) {
		return append(append([]byte(nil), prefix...), data[len(prefix)+1:]...)
	}
	return data
}
