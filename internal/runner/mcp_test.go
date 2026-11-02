package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/testbin"
)

const bridgedSecret = "s3cret-bridged-value"

func bridged() *MCP {
	return &MCP{
		Names:        []string{"jira"},
		CodexArgs:    []string{"-c", `mcp_servers.jira.url="https://j.example.com"`, "-c", `mcp_servers.jira.env_http_headers={"X-Token"="DUOLOG_MCP_JIRA_1"}`},
		ClaudeConfig: []byte(`{"mcpServers":{"jira":{"type":"http","url":"https://j.example.com","headers":{"X-Token":"${DUOLOG_MCP_JIRA_1}"}}}}`),
		AllowedTools: []string{"mcp__jira__get_issue", "mcp__jira__search"},
		Env:          []string{"DUOLOG_MCP_JIRA_1=" + bridgedSecret},
	}
}

// A Claude worker reads bridged servers from a 0600 file that exists only
// during the run, and the values reach its environment, not argv or the file.
func TestClaudeWorkerGetsBridgedServers(t *testing.T) {
	s := spec(t, "claude")
	p := install(t, &s, testbin.Scenario{Stdout: `{"structured_output":` + goodAnswer + `}`})
	s.MCP = bridged()
	if _, err := Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var args, env []string
	json.Unmarshal(read(t, p+".args.json"), &args)
	json.Unmarshal(read(t, p+".env.json"), &env)
	file := ""
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--mcp-config="); ok {
			file = v
		}
	}
	if filepath.Dir(file) != s.TaskDir {
		t.Fatalf("config not in the task folder: %q", args)
	}
	containsArgs(t, args, "--strict-mcp-config", "--allowedTools=mcp__jira__get_issue,mcp__jira__search")
	containsArgs(t, env, "DUOLOG_MCP_JIRA_1="+bridgedSecret)
	if string(read(t, p+".mcp-config.json")) != string(s.MCP.ClaudeConfig) {
		t.Fatal("config differs")
	}
	if runtime.GOOS != "windows" && string(read(t, p+".mcp-config.mode")) != "-rw-------" {
		t.Fatal(string(read(t, p+".mcp-config.mode")))
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("config left behind", err)
	}
	if strings.Contains(strings.Join(args, " "), bridgedSecret) {
		t.Fatal("secret in argv")
	}
	filepath.Walk(s.TaskDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(string(read(t, path)), bridgedSecret) {
			t.Fatalf("secret in %s", path)
		}
		return nil
	})
}

// A failed run removes the file as well.
func TestClaudeBridgedConfigRemovedOnFailure(t *testing.T) {
	s := spec(t, "claude")
	install(t, &s, testbin.Scenario{Exit: 1, Stderr: "boom"})
	s.MCP = bridged()
	if _, err := Run(context.Background(), s); err == nil {
		t.Fatal("want failure")
	}
	matches, _ := filepath.Glob(filepath.Join(s.TaskDir, "mcp-*.json"))
	if len(matches) != 0 {
		t.Fatal(matches)
	}
}

// Bridged servers add to the worker's own when the task keeps them.
func TestClaudeBridgedWithOwnServers(t *testing.T) {
	s := spec(t, "claude")
	s.MCP = bridged()
	s.MCP.configPath = "/tmp/x.json"
	s.KeepMCP = true
	a := Argv(s)
	containsArgs(t, a, "--mcp-config=/tmp/x.json")
	if strings.Contains(strings.Join(a, " "), "--strict-mcp-config") {
		t.Fatal(a)
	}
	// Every list option takes one value, so none can swallow the task line.
	if a[len(a)-2] != "--" {
		t.Fatal(a)
	}
}

func TestCodexWorkerGetsBridgedServers(t *testing.T) {
	s := spec(t, "codex")
	s.Env = []string{"PATH=" + os.Getenv("PATH")}
	p := install(t, &s, testbin.Scenario{Writes: map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}})
	s.Env = []string{"PATH=" + os.Getenv("PATH")} // as env_passthrough leaves it
	s.MCP = bridged()
	if _, err := Run(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	var args, env []string
	json.Unmarshal(read(t, p+".args.json"), &args)
	json.Unmarshal(read(t, p+".env.json"), &env)
	containsArgs(t, args, s.MCP.CodexArgs...)
	containsArgs(t, env, "DUOLOG_MCP_JIRA_1="+bridgedSecret)
	if strings.Contains(strings.Join(args, " "), bridgedSecret) {
		t.Fatal("secret in argv")
	}
	if matches, _ := filepath.Glob(filepath.Join(s.TaskDir, "mcp-*.json")); len(matches) != 0 {
		t.Fatal("a Codex worker needs no file", matches)
	}
}
