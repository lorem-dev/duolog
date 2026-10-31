package runner

import (
	"encoding/json"
	"fmt"
	"path/filepath"

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
	if s.ThreadID == "" {
		args = append(args, "--ephemeral")
	}
	args = append(args, "--output-schema", filepath.Join(s.TaskDir, "answer.schema.json"), "-o", filepath.Join(s.TaskDir, "answer.json"), "--json", taskInstruction)
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
