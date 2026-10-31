package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const offerMarker = "<!-- duolog -->"

// InstallOffer is the project instruction shared by installation and doctor.
const InstallOffer = "<!-- duolog --> If `.duolog/config.yaml` exists and `duolog` is not on PATH, offer the user this install, and run it only if they agree: `curl -fsSL https://github.com/lorem-dev/duolog/releases/latest/download/install.sh | sh && duolog install --scope project`"

type instructionState struct {
	Created         bool   `json:"created,omitempty"`
	SeparatorPrefix string `json:"separator_prefix,omitempty"`
	AddedNewline    bool   `json:"added_newline,omitempty"`
}

// InstructionPaths chooses the existing Claude instruction file, or its default.
func InstructionPaths(root string) []string {
	claude := "CLAUDE.md"
	if _, err := os.Stat(filepath.Join(root, ".claude", "CLAUDE.md")); err == nil {
		claude = filepath.Join(".claude", "CLAUDE.md")
	}
	return []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, claude)}
}

func stripOffer(data []byte) []byte {
	var out []byte
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte(offerMarker)) {
			out = append(out, line...)
		}
	}
	return out
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
			prefix = append(prefix, []byte(InstallOffer+"\n")...)
			return append(prefix, data[offset:]...), added
		}
		previous = text
	}
	return append([]byte(InstallOffer+"\n"), data...), false
}

func instructionEdits(root string) ([]edit, error) {
	statePath := filepath.Join(root, ".duolog", "instruction-files.json")
	state, err := readInstructionState(statePath)
	if err != nil {
		return nil, err
	}
	var edits []edit
	for _, path := range InstructionPaths(root) {
		rel, _ := filepath.Rel(root, path)
		old, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		entry, known := state[rel]
		if !known {
			entry.Created = os.IsNotExist(err)
		}
		clean := stripOffer(old)
		if !bytes.Equal(clean, old) {
			clean = removeSeparator(clean, entry)
		}
		data, added := insertOffer(clean)
		entry.AddedNewline = added
		entry.SeparatorPrefix = ""
		if added {
			entry.SeparatorPrefix = string(clean)
		}
		state[rel] = entry
		edits = append(edits, edit{path, data})
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(edits, edit{statePath, append(data, '\n')}), nil
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

func removeInstructions(root string) error {
	statePath := filepath.Join(root, ".duolog", "instruction-files.json")
	state, err := readInstructionState(statePath)
	if err != nil {
		return err
	}
	// Include both possible Claude locations, including a previously selected one.
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", filepath.Join(".claude", "CLAUDE.md")} {
		path := filepath.Join(root, rel)
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		clean := stripOffer(data)
		if bytes.Equal(clean, data) {
			continue
		}
		entry := state[rel]
		clean = removeSeparator(clean, entry)
		if len(clean) == 0 && entry.Created {
			err = os.Remove(path)
		} else {
			_, err = apply(edit{path, clean})
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
