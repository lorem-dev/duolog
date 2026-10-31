package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/mode"
)

func codexArgv(s Spec) []string {
	args := []string{"codex", "exec"}
	// Sandbox belongs to exec; its resume subcommand does not accept it.
	args = append(args, mode.Args("codex", s.Mode, s.Route.Sandbox)...)
	if s.ThreadID != "" {
		args = append(args, "resume", s.ThreadID)
	}
	args = append(args, "-m", s.Route.Model)
	if s.Route.Effort != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", s.Route.Effort))
	}
	args = append(args, "-c", "check_for_update_on_startup=false")
	if s.Route.Context == "minimal" {
		args = append(args, "-c", "project_doc_max_bytes=0")
	}
	if s.ThreadID == "" && !s.Persistent {
		args = append(args, "--ephemeral")
	}
	args = append(args, "-o", filepath.Join(s.TaskDir, "answer.json"), "--json", taskInstruction)
	return args
}

func codexEvent(data []byte, result *Result) {
	var event struct {
		Type     string `json:"type"`
		ThreadID string `json:"thread_id"`
		Usage    struct {
			Input  int64 `json:"input_tokens"`
			Cached int64 `json:"cached_input_tokens"`
			Output int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(data, &event) != nil {
		return
	}
	switch event.Type {
	case "thread.started":
		result.SessionID = event.ThreadID
	case "turn.completed":
		result.Usage.Input += event.Usage.Input
		result.Usage.Cached += event.Usage.Cached
		result.Usage.Output += event.Usage.Output
	}
}

// codexAnswer reads the final message tolerantly: it takes the last JSON
// object that has the four answer keys, fenced or not. When none parses the
// result is not done, with the start of the message as the summary.
func codexAnswer(message []byte, answerPath string, result Result) (Result, error) {
	type answer struct {
		Done    *bool    `json:"done"`
		Summary *string  `json:"summary"`
		Files   []string `json:"files"`
		Needs   []string `json:"needs"`
	}
	var found *answer
	for i := 0; i < len(message); i++ {
		if message[i] != '{' {
			continue
		}
		var parsed answer
		decoder := json.NewDecoder(bytes.NewReader(message[i:]))
		if decoder.Decode(&parsed) != nil {
			continue
		}
		if parsed.Done == nil || parsed.Summary == nil || parsed.Files == nil || parsed.Needs == nil {
			continue
		}
		found = &parsed
		i += int(decoder.InputOffset()) - 1
	}
	if found == nil {
		text := strings.Join(strings.Fields(string(message)), " ")
		if len(text) > 200 {
			text = strings.ToValidUTF8(text[:200], "")
		}
		result.Done = false
		result.Summary = text
		return result, fmt.Errorf("invalid answer: no JSON object with done, summary, files and needs in the final message; see %s", filepath.Join(filepath.Dir(answerPath), "stdout.log"))
	}
	clean, err := json.Marshal(struct {
		Done    bool     `json:"done"`
		Summary string   `json:"summary"`
		Files   []string `json:"files"`
		Needs   []string `json:"needs"`
	}{*found.Done, *found.Summary, found.Files, found.Needs})
	if err == nil {
		err = os.WriteFile(answerPath, clean, 0600)
	}
	if err != nil {
		return result, err
	}
	result.Done, result.Summary, result.Files, result.Needs = *found.Done, *found.Summary, found.Files, found.Needs
	return result, nil
}
