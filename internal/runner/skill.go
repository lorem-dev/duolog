package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func skillArgv(s Spec) []string {
	switch s.Route.Agent {
	case "claude":
		return []string{"claude", "-p", "--model", s.Route.Model, "--effort", s.Route.Effort, "--tools", "", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands", "--output-format", "json", "--settings", claudeWorkerSettings("", true)}
	case "codex":
		args := []string{"codex", "exec", "--sandbox", "read-only", "--skip-git-repo-check", "--ephemeral", "-m", s.Route.Model, "-c", fmt.Sprintf("model_reasoning_effort=%q", s.Route.Effort), "-c", "project_doc_max_bytes=0", "-c", "skills.include_instructions=false", "-c", "features.shell_tool=false", "-c", "features.apply_patch_freeform=false", "-c", `web_search="disabled"`}
		if s.MCP != nil {
			args = append(args, s.MCP.CodexArgs...)
		}
		return append(args, "--json", "-o", filepath.Join(s.TaskDir, "answer.json"), "-")
	}
	return nil
}

func skillResult(s Spec, stdoutPath, answerPath string, result Result) (Result, error) {
	if s.Route.Agent == "codex" {
		data, err := os.ReadFile(answerPath)
		result.RawAnswer = string(data)
		return result, err
	}
	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		return result, err
	}
	var output struct {
		Result  string      `json:"result"`
		IsError bool        `json:"is_error"`
		Usage   claudeUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		result.RawAnswer = string(data)
		return result, err
	}
	result.RawAnswer = output.Result
	result.Usage = output.Usage.tokens()
	if output.IsError {
		return result, fmt.Errorf("Claude returned an error: %s", output.Result) //nolint:staticcheck // ST1005: Claude is a product name
	}
	return result, nil
}
