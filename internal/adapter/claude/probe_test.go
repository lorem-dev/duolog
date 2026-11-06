package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/doctor/doctortest"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// probeProcess is the process a probe runs as: the claude binary on the
// test's PATH, with that PATH in its environment.
func probeProcess() corelaunch.Process {
	return corelaunch.Process{Agent: "claude", Prefix: []string{"claude"}, Env: os.Environ()}
}

func TestProbeContract(t *testing.T) {
	doctortest.ProbeContract(t, func(t *testing.T, present bool) (doctortest.Probe, corelaunch.Process) {
		if present {
			testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
				"--version": {Stdout: "9.9.9 (fake)\n"},
				"-p":        {Stdout: `{"is_error":false,"result":"ok","usage":{"input_tokens":80,"cache_read_input_tokens":20,"output_tokens":5}}`},
			}})
		} else {
			t.Setenv("PATH", t.TempDir())
		}
		return Probe{}, probeProcess()
	})
}

func TestProbeModel(t *testing.T) {
	if model, err := (Probe{}).Model(nil, nil); err != nil || model != "haiku" {
		t.Fatalf("Model = %q, %v, want haiku", model, err)
	}
}

// The probe is the empty call of the live bench, in plan mode, in the folder
// it was given, and its process is the one the gate built.
func TestProbeArgvAndProcess(t *testing.T) {
	bin := testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"is_error":false,"result":"ok"}`})
	dir := t.TempDir()
	p := probeProcess()
	p.Prefix = []string{"claude", "--settings", `{"fastMode":false}`}
	p.Env = append(p.Env, "DUOLOG_DEPTH=1")
	if _, err := (Probe{}).Probe(context.Background(), &config.Config{}, p, dir, "haiku", func(int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var args []string
	data, err := os.ReadFile(bin + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	if args[0] != "--settings" || !strings.Contains(args[1], `"fastMode":false`) || !slices.Contains(args, "plan") || !slices.Contains(args, "haiku") {
		t.Fatalf("claude probe argv: %q", args)
	}
	var env []string
	data, err = os.ReadFile(bin + ".env.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "DUOLOG_DEPTH=1") {
		t.Fatalf("probe env lacks the process's own: %q", env)
	}
	cwd, err := os.ReadFile(bin + ".cwd")
	// The folder may be spelled through a link (/private on macOS), so the
	// last element, which is unique to the test, is compared.
	if err != nil || filepath.Base(strings.TrimSpace(string(cwd))) != filepath.Base(dir) {
		t.Fatalf("probe cwd = %q, %v, want %s", cwd, err, dir)
	}
}

func TestReadProbeUsage(t *testing.T) {
	out := []byte(`{"usage":{"input_tokens":0,"cache_creation_input_tokens":2000000,"cache_read_input_tokens":0,"output_tokens":0,` +
		`"cache_creation":{"ephemeral_1h_input_tokens":1000000}},"total_cost_usd":0.0123}`)
	u := readProbeUsage(out)
	if !u.Known || u.Input != 2000000 || u.Write != 2000000 || u.WriteHour != 1000000 || u.CostUSD == nil || *u.CostUSD != 0.0123 {
		t.Fatalf("usage = %+v", u)
	}
	if u := readProbeUsage([]byte(`{"usage":{"input_tokens":-1}}`)); u.Known {
		t.Fatalf("negative tokens read as known: %+v", u)
	}
	if u := readProbeUsage([]byte(`not json`)); u.Known {
		t.Fatalf("garbage read as known: %+v", u)
	}
}

func TestProbeWrongAnswer(t *testing.T) {
	testbin.Install(t, "claude", testbin.Scenario{Stdout: `{"is_error":false,"result":"no"}`})
	if _, err := (Probe{}).Probe(context.Background(), nil, probeProcess(), t.TempDir(), "haiku", func(int) error { return nil }); err == nil {
		t.Fatal("a wrong answer passed the probe")
	}
}
