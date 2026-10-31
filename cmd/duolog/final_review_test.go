package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func generatedCommand(t *testing.T, agent, event string) testbin.HookCommand {
	t.Helper()
	return generatedCommandForOS(t, agent, runtime.GOOS, event)
}

func generatedCommandForOS(t *testing.T, agent, goos, event string) testbin.HookCommand {
	t.Helper()
	data, err := install.HooksJSON(agent, goos, 150)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command, CommandWindows string
				Args                    []string
			}
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	h := doc.Hooks[event][0].Hooks[0]
	if goos == "windows" && h.CommandWindows != "" {
		return testbin.HookCommand{Command: h.CommandWindows}
	}
	return testbin.HookCommand{Command: h.Command, Args: h.Args}
}

// This test drives install -> generated hook -> session -> delegate -> runner ->
// fake agent -> the generated gate command. No model or terminal is started.
func TestGeneratedHooksAndDocumentedDelegate(t *testing.T) {
	_, source, _, _ := runtime.Caller(0)
	repo := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "duolog")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/duolog")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", output, err)
	}
	for _, leadAgent := range []string{"claude", "codex"} {
		t.Run(leadAgent, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Setenv("USERPROFILE", os.Getenv("HOME"))
			t.Setenv("CODEX_HOME", "")
			t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
			t.Setenv("DUOLOG_DEPTH", "")
			t.Setenv("DUOLOG_LEAD", "")
			t.Setenv("DUOLOG_ROLE", "")
			root := t.TempDir()
			t.Chdir(root)
			home := os.Getenv("HOME")
			// User scope must initialize a project that has no .duolog yet.
			if _, err := install.Run(install.Options{Scope: "user", Agent: "both", Root: root, Home: home}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			transcript := filepath.Join(root, "lead.jsonl")
			text := "{\"type\":\"assistant\",\"message\":{\"model\":\"haiku\"},\"effort\":\"low\"}\n"
			if leadAgent == "codex" {
				text = "{\"type\":\"session_meta\",\"payload\":{\"originator\":\"codex-tui\"}}\n{\"type\":\"turn_context\",\"payload\":{\"model\":\"gpt-6-sol\",\"effort\":\"low\",\"approval_policy\":\"never\",\"sandbox_policy\":{\"type\":\"danger-full-access\"}}}\n"
			}
			if err := os.WriteFile(transcript, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(map[string]string{"session_id": "lead-real-guard", "permission_mode": "bypassPermissions", "transcript_path": transcript, "cwd": root})
			invokeHook := func(event string) []byte {
				t.Helper()
				command := generatedCommand(t, leadAgent, event)
				cmd := testbin.HookProcess(command.Command, command.Args...)
				cmd.Stdin = bytes.NewReader(input)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %s %v", event, output, err)
				}
				return output
			}
			if output := invokeHook("SessionStart"); !bytes.Contains(output, []byte("lead-real-guard")) {
				t.Fatalf("registration: %s", output)
			}
			invokeHook("UserPromptSubmit")
			reg, err := session.Open(filepath.Join(root, ".duolog"))
			if err != nil {
				t.Fatal(err)
			}
			lead, ok, err := reg.Lead("lead-real-guard")
			if err != nil || !ok || lead.PID != os.Getpid() {
				t.Fatalf("lead did not survive the next generated hook: %+v %v %v", lead, ok, err)
			}
			// Approvals use the documented key shape; the event spelling has no saved
			// config.toml fixture and remains a concern for live verification.
			key := filepath.Join(home, ".codex", "hooks.json") + ":PreToolUse:0:0"
			settings := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n[hooks.state.%q]\ntrusted_hash = \"fixture\"\n", root, key)
			if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(settings), 0600); err != nil {
				t.Fatal(err)
			}
			task := "## Goal\nDo work.\n## Read\n- .env\n## Write\n- out.txt\n## Limits\nStay focused.\n## Check\nInspect result.\n"
			if err := os.WriteFile("task.md", []byte(task), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(".env", []byte("SECRET_SHOULD_NOT_BE_IN_STDIN"), 0600); err != nil {
				t.Fatal(err)
			}
			workerAgent := "codex"
			if leadAgent == "codex" {
				workerAgent = "claude"
			}
			for _, tc := range []struct{ role, kind, input, want string }{
				{"docs", "Write", `{"file_path":"out.txt"}`, ""},
				{"review_intermediate", "Bash", `{"command":"sed -i x out.txt"}`, "read-only role"},
				{"docs", "UnverifiedTool", `{}`, "lead is waiting"},
			} {
				// Route these test roles to the opposite provider, retaining their real
				// sandbox and session policies. This covers both providers symmetrically.
				cfg := fmt.Sprintf("version: 1\nroles:\n  docs:\n    context: project\n    use: {agent: %s, model: sonnet}\n  review_intermediate:\n    use: {agent: %s, model: sonnet}\n", workerAgent, workerAgent)
				if workerAgent == "codex" {
					cfg = strings.ReplaceAll(cfg, "model: sonnet", "model: gpt-6-sol")
				}
				if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte(cfg), 0600); err != nil {
					t.Fatal(err)
				}
				tool := tc.kind
				if workerAgent == "codex" && tool == "Write" {
					tool = "apply_patch"
				}
				toolInput := tc.input
				if tool == "apply_patch" {
					toolInput = `{"patch":"*** Begin Patch\n*** Add File: out.txt\n+x\n*** End Patch"}`
				}
				hookInput := fmt.Sprintf(`{"tool_name":%q,"cwd":%q,"tool_input":%s}`, tool, root, toolInput)
				answer := `{"done":true,"summary":"Finished.","files":[],"needs":[]}`
				if tc.want != "" {
					answer = `{"done":false,"summary":"Needs lead decision.","files":[],"needs":["approval"]}`
				}
				hook := generatedCommand(t, workerAgent, "PreToolUse")
				hook.Input = hookInput
				scenario := testbin.Scenario{Hooks: []testbin.HookCommand{hook}, Answer: answer}
				if workerAgent == "claude" {
					scenario.Stdout = `{"structured_output":` + answer + `}`
				}
				fake := testbin.Install(t, workerAgent, scenario)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				cmd := exec.CommandContext(ctx, binary, "delegate", tc.role, "--task", "task.md")
				cmd.Env = append(os.Environ(), "PATH="+strings.ReplaceAll(os.Getenv("PATH"), binDir+string(os.PathListSeparator), ""))
				output, runErr := cmd.CombinedOutput()
				cancel()
				if tc.want == "" && runErr != nil {
					t.Fatalf("delegate: %s %v", output, runErr)
				}
				if tc.want != "" && (cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3) {
					t.Fatalf("needs exit: %s %v", output, runErr)
				}
				env, _ := os.ReadFile(fake + ".env.json")
				var workerPairs []string
				if err := json.Unmarshal(env, &workerPairs); err != nil {
					t.Fatal(err)
				}
				foundPath := false
				for _, pair := range workerPairs {
					if strings.HasPrefix(pair, "PATH=") {
						foundPath = true
						if strings.TrimPrefix(pair, "PATH=") != binDir && !strings.HasPrefix(pair, "PATH="+binDir+string(os.PathListSeparator)) {
							t.Fatal("worker PATH does not start with executable directory", pair)
						}
					}
				}
				if !foundPath {
					t.Fatal("worker has no PATH")
				}
				for _, want := range []string{"DUOLOG_ROLE=" + tc.role, "DUOLOG_LEAD=lead-real-guard"} {
					if !bytes.Contains(env, []byte(want)) {
						t.Fatalf("missing %s in worker env", want)
					}
				}
				stdin, _ := os.ReadFile(fake + ".stdin")
				if bytes.Contains(stdin, []byte("SECRET_SHOULD_NOT_BE_IN_STDIN")) || !bytes.Contains(stdin, []byte("Not inlined by read policy")) {
					t.Fatalf("read policy not enforced in stdin: %s", stdin)
				}
				args, _ := os.ReadFile(fake + ".args.json")
				if !bytes.Contains(args, []byte("low")) {
					t.Fatalf("lost effort cap: %s", args)
				}
				var gateResult struct {
					Stdout, Stderr string
					Code           int
				}
				data, err := os.ReadFile(fake + ".hook-0.json")
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &gateResult); err != nil {
					t.Fatal(err)
				}
				if gateResult.Code != 0 || gateResult.Stderr != "" || !strings.Contains(gateResult.Stdout, tc.want) {
					t.Fatalf("gate: %+v want %q", gateResult, tc.want)
				}
				if tc.want == "" && gateResult.Stdout != "" {
					t.Fatalf("write role denied: %+v", gateResult)
				}
				if tc.want != "" && !strings.Contains(gateResult.Stdout, "deny") {
					t.Fatalf("read-only or ask not denied: %+v", gateResult)
				}
				if waiting, err := reg.IsWaiting(lead.ID); err != nil || waiting {
					t.Fatalf("wait marker leaked: %v %v", waiting, err)
				}
				if hookOutput := invokeHook("PostToolUse"); bytes.Contains(hookOutput, []byte(`\"k\":\"answer\"`)) {
					t.Fatalf("answer delivered twice: %s", hookOutput)
				}
			}
		})
	}
}

func TestRouteResolvesLiveLeadAndAvailability(t *testing.T) {
	isolatedRouteConfig(t)
	routeLead(t)
	reg, _ := session.Open(filepath.Join(mustGetwd(t), ".duolog"))
	lead, _ := reg.ResolveLead("")
	lead.Target = config.Target{Agent: "claude", Model: "sonnet", Effort: "low"}
	if err := reg.RegisterLead(lead); err != nil {
		t.Fatal(err)
	}
	out, err := executeBusCommand(t, "route", "docs", "--json")
	if err != nil || !strings.Contains(out, `"self":true`) || !strings.Contains(out, `"effort_capped":true`) {
		t.Fatal(out, err)
	}
	if err := reg.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	out, err = executeBusCommand(t, "route", "implement", "--json")
	if err != nil || !strings.Contains(out, `"in_lead":true`) {
		t.Fatal(out, err)
	}
}

type panicHookInput struct{}

func (panicHookInput) Read([]byte) (int, error) { panic("broken hook input") }

func TestLeadHookPanicsStaySilent(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "")
	for _, args := range [][]string{{"hook", "SessionStart", "--agent", "claude"}, {"gate", "--agent", "claude", "--event", "PreToolUse"}} {
		cmd := newRootCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetIn(panicHookInput{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil || output.Len() != 0 {
			t.Fatal(args, output.String(), err)
		}
	}
}

func TestDelegateInterruptReportsAndStopsDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows signal test pending")
	}
	_, source, _, _ := runtime.Caller(0)
	binary := filepath.Join(t.TempDir(), "duolog")
	build := exec.Command("go", "build", "-o", binary, "./cmd/duolog")
	build.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	isolatedRouteConfig(t)
	routeLead(t)
	root := mustGetwd(t)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	lead, err := reg.ResolveLead("")
	if err != nil {
		t.Fatal(err)
	}
	lead.Agent = "codex"
	lead.Model = "gpt-6-sol"
	if err := reg.RegisterLead(lead); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("roles:\n  docs:\n    context: project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	task := "## Goal\nWork.\n## Read\n## Write\n## Limits\nStay focused.\n## Check\nInspect.\n"
	if err := os.WriteFile("task.md", []byte(task), 0600); err != nil {
		t.Fatal(err)
	}
	fake := testbin.Install(t, "claude", testbin.Scenario{SpawnChild: true, SleepMS: 60000})
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "delegate", "docs", "--task", "task.md")
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(fake + ".child-pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake worker did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("interrupted delegate succeeded")
	}
	if !strings.Contains(output.String(), "role docs interrupted") {
		t.Fatal(output.String())
	}
	data, err := os.ReadFile(filepath.Join(root, ".duolog", "bus.ndjson"))
	if err != nil || !bytes.Contains(data, []byte("role docs interrupted")) {
		t.Fatal(string(data), err)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(fake + ".child-done"); !os.IsNotExist(err) {
		t.Fatal("worker descendant outlived interrupted delegate", err)
	}
}
