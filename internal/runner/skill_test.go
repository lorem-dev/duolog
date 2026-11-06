package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/testbin"
)

func TestSkillRunUsesRawAnswersAndUsage(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			scenario := testbin.Scenario{Answer: `{"s1":"YES"}`, Stdout: "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":100,\"cached_input_tokens\":20,\"output_tokens\":10}}\n"}
			if agent == "claude" {
				scenario.Stdout = `{"type":"result","result":"{\"s1\":\"YES\"}","usage":{"input_tokens":80,"cache_read_input_tokens":20,"output_tokens":10}}`
			}
			fake := testbin.Install(t, agent, scenario)
			s := rebuilt(t, Spec{Route: router.Route{Target: config.Target{Agent: agent, Model: "test", Effort: "high"}}, TaskDir: t.TempDir(), Cwd: t.TempDir(), Stdin: []byte("the skill prompt"), SkillTest: true})
			result, err := Run(context.Background(), s)
			if err != nil || result.RawAnswer != `{"s1":"YES"}` || result.Usage.Input != 100 || result.Usage.Cached != 20 || result.Usage.Output != 10 {
				t.Fatalf("%+v %v", result, err)
			}
			data, err := os.ReadFile(fake + ".stdin")
			if err != nil || string(data) != "the skill prompt" {
				t.Fatalf("stdin %q %v", data, err)
			}
			argv := strings.Join(Argv(s), "|")
			wants := []string{"--ephemeral", "--sandbox|read-only", "--skip-git-repo-check", "-o|" + filepath.Join(s.TaskDir, "answer.json")}
			if agent == "claude" {
				wants = []string{"--tools||", "--strict-mcp-config", "--no-session-persistence", "--disable-slash-commands"}
			}
			for _, want := range wants {
				if !strings.Contains(argv, want) {
					t.Errorf("missing %s in %s", want, argv)
				}
			}
		})
	}
}

func TestSkillRunKeepsFailedAnswerAndUsage(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			scenario := testbin.Scenario{Answer: `{"s1":"yes"}`, Stdout: "{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":100,\"output_tokens\":10}}\n", Exit: 1}
			if agent == "claude" {
				scenario.Stdout = `{"result":"{\"s1\":\"yes\"}","usage":{"input_tokens":100,"output_tokens":10}}`
			}
			testbin.Install(t, agent, scenario)
			result, err := Run(context.Background(), rebuilt(t, Spec{Route: router.Route{Target: config.Target{Agent: agent, Model: "test", Effort: "low"}}, TaskDir: t.TempDir(), Cwd: t.TempDir(), SkillTest: true}))
			if err == nil || result.RawAnswer != `{"s1":"yes"}` || result.Usage.Input != 100 {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}
