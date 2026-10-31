package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/models"
)

// probe uses the empty-call form from the live verification bench, with
// read-only access and an explicit output file for Codex.
func probe(ctx context.Context, agent string, cfg *config.Config, entries []catalogEntry) error {
	dir, err := os.MkdirTemp("", "duolog-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	var args []string
	if agent == "codex" {
		list := make([]models.Entry, 0, len(entries))
		for _, entry := range entries {
			list = append(list, entry.Entry)
		}
		model, ok := models.Latest(cfg.Models["luna"].Family, list)
		if !ok {
			return fmt.Errorf("no visible model for luna probe")
		}
		args = []string{"exec", "--json", "--skip-git-repo-check", "--ignore-user-config", "-m", model, "-c", "model_reasoning_effort=low", "-c", "project_doc_max_bytes=0", "-c", "check_for_update_on_startup=false", "-s", "read-only", "--ephemeral", "-o", filepath.Join(dir, "answer.json"), "Reply with the word ok."}
	} else {
		args = []string{"-p", "--model", "haiku", "--effort", "low", "--no-session-persistence", "--output-format", "json", "--setting-sources", "project,local", "--strict-mcp-config", "--permission-mode", "plan", "--", "Reply with the word ok."}
	}
	cmd := exec.CommandContext(ctx, agent, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("")
	// Prevent inherited lead identity from making the probe look like a lead.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DUOLOG_DEPTH=") && !strings.HasPrefix(value, "DUOLOG_LEAD=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "DUOLOG_DEPTH=1", "DUOLOG_LEAD=")
	output, err := cmd.Output()
	if err != nil {
		return err
	}
	if agent == "codex" {
		answer, err := os.ReadFile(filepath.Join(dir, "answer.json"))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(answer)) != "ok" {
			return fmt.Errorf("missing expected answer")
		}
	} else {
		var result struct {
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return err
		}
		if result.IsError || strings.TrimSpace(result.Result) != "ok" {
			return fmt.Errorf("missing expected answer")
		}
	}
	return nil
}
