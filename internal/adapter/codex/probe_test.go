package codex

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/doctor/doctortest"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// probeProcess is the process a probe runs as: the codex binary on the test's
// PATH, with that PATH in its environment.
func probeProcess() corelaunch.Process {
	return corelaunch.Process{Agent: "codex", Prefix: []string{"codex", "-c", "check_for_update_on_startup=false"}, Env: os.Environ()}
}

func TestProbeContract(t *testing.T) {
	doctortest.ProbeContract(t, func(t *testing.T, present bool) (doctortest.Probe, corelaunch.Process) {
		if present {
			testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{
				"--version": {Stdout: "9.9.9 (fake)\n"},
				"-c":        {Stdout: `{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":5}}` + "\n", Writes: map[string]string{"answer.json": "ok\n"}},
			}})
		} else {
			t.Setenv("PATH", t.TempDir())
		}
		return Probe{}, probeProcess()
	})
}

func TestProbeModel(t *testing.T) {
	cfg := &config.Config{Models: map[string]config.Model{"luna": {Family: "luna"}}}
	visible := []route.Entry{{Slug: "gpt-6-luna", Visibility: "list"}}
	if model, err := (Probe{}).Model(cfg, visible); err != nil || model != "gpt-6-luna" {
		t.Fatalf("Model = %q, %v, want gpt-6-luna", model, err)
	}
	if _, err := (Probe{}).Model(cfg, nil); err == nil || !strings.Contains(err.Error(), "no visible model for luna probe") {
		t.Fatalf("Model with no catalog = %v, want the refusal", err)
	}
}

// The probe is the read-only empty call of the live bench, in the folder it
// was given, with the output file in that folder.
func TestProbeArgvAndAnswerFile(t *testing.T) {
	bin := testbin.Install(t, "codex", testbin.Scenario{Writes: map[string]string{"answer.json": "ok\n"}})
	dir := t.TempDir()
	if _, err := (Probe{}).Probe(context.Background(), nil, probeProcess(), dir, "gpt-6-luna", func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(bin + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"-c check_for_update_on_startup=false exec ", "--skip-git-repo-check", "-m gpt-6-luna", "read-only"} {
		if !strings.HasPrefix(line, "-c check_for_update_on_startup=false exec ") || !strings.Contains(line, want) {
			t.Fatalf("codex probe argv missing %q: %q", want, args)
		}
	}
	if strings.Contains(line, "--ignore-user-config") {
		t.Fatalf("codex probe argv ignores the user config: %q", args)
	}
	stdin, err := os.ReadFile(bin + ".stdin")
	if err != nil || len(stdin) != 0 {
		t.Fatalf("stdin=%q err=%v", stdin, err)
	}
}

// A probe whose answer file is empty fails.
func TestProbeEmptyAnswer(t *testing.T) {
	testbin.Install(t, "codex", testbin.Scenario{})
	if _, err := (Probe{}).Probe(context.Background(), nil, probeProcess(), t.TempDir(), "gpt-6-luna", func(int) error { return nil }); err == nil {
		t.Fatal("a probe with no answer passed")
	}
}

func TestReadProbeUsage(t *testing.T) {
	out := []byte(`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":5}}` + "\n" +
		`{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":5,"output_tokens":1}}` + "\n")
	u := readProbeUsage(out)
	if !u.Known || u.Input != 110 || u.Cached != 25 || u.Output != 6 {
		t.Fatalf("usage = %+v", u)
	}
	if u := readProbeUsage([]byte(`{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":2}}`)); u.Known {
		t.Fatalf("cached above input read as known: %+v", u)
	}
}
