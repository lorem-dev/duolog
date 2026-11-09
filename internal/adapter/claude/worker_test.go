package claude

import (
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/adapter/kit"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
)

// TestAnswerSchemaKeepsDecisions: the schema Claude is asked to answer in names
// the worker's decisions, as Codex's instruction does.
func TestAnswerSchemaKeepsDecisions(t *testing.T) {
	if !strings.Contains(answerSchema, `"decisions"`) {
		t.Fatal("the answer format misses decisions")
	}
}

// TestWorkerAndPeerGetTheBatchReadsRule: a Claude worker and a Claude peer
// start with the same appended system prompt, which tells them to batch reads
// and not to use inline scripts. A skill test run is left as it was.
func TestWorkerAndPeerGetTheBatchReadsRule(t *testing.T) {
	rule := kit.BatchReadsRule
	for _, part := range []string{"one Bash call", "joined by ;", "node -e", "python -c"} {
		if !strings.Contains(rule, part) {
			t.Fatalf("rule misses %q: %q", part, rule)
		}
	}
	p := corelaunch.Process{Prefix: []string{"claude"}, Launch: []string{"-p"}}
	r := route.Route{Target: config.Target{Agent: "claude", Model: "opus"}}
	argvs := map[string][]string{
		"worker": Worker{}.Argv(run.Spec{Route: r, Process: &p}),
		"peer":   Pane{}.Argv(r, mode.Ask, p, ""),
	}
	for name, args := range argvs {
		i := slices.Index(args, "--append-system-prompt")
		if i < 0 || i+1 >= len(args) || args[i+1] != kit.BatchReadsRule {
			t.Errorf("%s: %v", name, args)
		}
	}
	skill := Worker{}.Argv(run.Spec{Route: r, Process: &p, SkillTest: true})
	if slices.Contains(skill, "--append-system-prompt") {
		t.Errorf("a skill test run changed: %v", skill)
	}
}
