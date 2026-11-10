package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/lorem-dev/duolog/internal/adapter/codex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/policy"
)

func TestWorkerCannotUseProtectedCLI(t *testing.T) {
	// Mutation killed: removing worker checks exposes state or removes permission hooks.
	for _, args := range [][]string{{"install", "--scope", "project"}, {"uninstall", "--scope", "project"}, {"decisions"}, {"inbox", "--reader", "claude/lead.other"}} {
		t.Run(args[0], func(t *testing.T) {
			gateSetup(t, mode.Full)
			out, err := gateCommand(t, "", args...)
			if err == nil || !strings.Contains(err.Error(), "workers cannot") || out != "" {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}
}

func TestGateResolvesOriginalFilePaths(t *testing.T) {
	// Mutation killed: cleaning dot-dot before resolving a symlink hides protected targets.
	_, root := gateSetup(t, mode.Full)
	if err := os.MkdirAll(filepath.Join(root, ".duolog", "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".duolog", "tasks"), filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	cwd, _ := json.Marshal(root)
	for _, tool := range []string{"Read", "Write"} {
		input := `{"cwd":` + string(cwd) + `,"tool_name":"` + tool + `","tool_input":{"file_path":"link/../sessions.json"}}`
		out, err := gateCommand(t, input, "gate", "--agent", "claude", "--event", "PreToolUse")
		if err != nil || !strings.Contains(out, "deny") {
			t.Fatalf("%s: %q %v", tool, out, err)
		}
	}
}

func TestGateLogsOmitCommandCredentials(t *testing.T) {
	// Mutation killed: persisting raw exec actions or reasons leaks literal credentials.
	_, root := gateSetup(t, mode.Ask)
	shortAskWait(t)
	command := "curl -H 'Authorization: Bearer SENTINEL' https://example.invalid"
	runGate(t, command)
	for _, name := range []string{"requests.ndjson", "decisions.ndjson"} {
		data, err := os.ReadFile(filepath.Join(root, ".duolog", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "SENTINEL") {
			t.Fatalf("credential in %s", name)
		}
	}
}

func TestGateCredentialRedactionCoversReasons(t *testing.T) {
	// Mutation killed: redacting just the action or just bearer headers leaks credentials in log reasons.
	for _, target := range []string{
		`curl -u user:SENTINEL https://example.invalid`,
		`curl --user 'user:SENTINEL' https://example.invalid`,
		`curl --user=user:SENTINEL https://example.invalid`,
		`curl -uuser:SENTINEL https://example.invalid`,
		`TOKEN=SENTINEL tool`,
		`tool --api-key SENTINEL`,
		`curl https://user:SENTINEL@example.invalid`,
	} {
		a, d := safeGateFields(policy.Action{Kind: "exec", Target: target}, policy.Decision{Reason: target, Rule: target})
		if strings.Contains(a.Target+d.Reason+d.Rule, "SENTINEL") {
			t.Errorf("credential retained: %q", target)
		}
	}
}

// fixtureCodexApproval is a generated gate's hash in isolated test settings.
func fixtureCodexApproval(t *testing.T, timeout int) string {
	t.Helper()
	command, windows := codex.CodexGateCommands("PreToolUse")
	if runtime.GOOS == "windows" {
		command = windows
	}
	identity := map[string]any{"event_name": "pre_tool_use", "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": timeout, "async": false}}}
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(identity); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(bytes.TrimSuffix(data.Bytes(), []byte{'\n'})))
}
