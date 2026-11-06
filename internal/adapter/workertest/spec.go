package workertest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/testbin"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

const goodAnswer = `{"done":true,"summary":"Implemented.","files":["a.go"],"needs":[]}`

// Spec prepares one case of the worker contract for the named agent: a spec
// with the free worker process of its route, and the fake agent binary on that
// process's PATH that behaves as the case says. The cases are "unknown model",
// "binary missing", "no answer", "done", "quota" and "login".
func Spec(t *testing.T, agent, name string) run.Spec {
	t.Helper()
	model, effort := "gpt-6-luna", "low"
	if agent == "claude" {
		model, effort = "opus", "high"
	}
	s := run.Spec{
		Route:   coreroute.Route{Target: config.Target{Agent: agent, Model: model, Effort: effort}, Sandbox: "read-only", Context: "minimal"},
		Mode:    mode.Plan,
		TaskDir: t.TempDir(), Cwd: t.TempDir(),
		Stdin: []byte("<stdin>\nImplement the task.\n</stdin>"),
	}
	switch name {
	case "unknown model":
		s.Route.Model = ""
		testbin.Install(t, agent, testbin.Scenario{})
	case "binary missing":
		// Nothing is installed, and the process's PATH holds only an empty folder.
		t.Setenv("PATH", t.TempDir())
	case "no answer":
		testbin.Install(t, agent, testbin.Scenario{Exit: 1})
	case "done":
		sc := testbin.Scenario{}
		if agent == "claude" {
			sc.Stdout = `{"structured_output":` + goodAnswer + `}`
		} else {
			sc.Writes = map[string]string{filepath.Join(s.TaskDir, "answer.json"): goodAnswer}
		}
		testbin.Install(t, agent, sc)
	case "quota":
		testbin.Install(t, agent, testbin.Scenario{Exit: 1, Stderr: "usage limit resets 3am (Europe/London). Try again at 8:48 AM."})
	case "login":
		testbin.Install(t, agent, testbin.Scenario{Exit: 1, Stderr: "Not logged in. Please run /login"})
	default:
		t.Fatalf("unknown case %q", name)
	}
	in := workerenv.ProcessInput{Agent: agent, Shape: workerenv.Worker, Variant: workerenv.Free, Config: &config.Config{},
		Environ: os.Environ(), LeadID: "lead-123", Role: "implement", StateDir: project.StateRoot(s.Cwd),
		Minimal: true, Launch: For(agent).Launch(s)}
	p, err := workerenv.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	s.Process = &p
	return s
}
