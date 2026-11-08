package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/wiring/wiringtest"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

// mcpToolsListedUnder serves `duolog mcp` in a duolog project with the
// DUOLOG_* names of environ set, as a process started under that launch sees
// them, and returns what it printed.
func mcpToolsListedUnder(t *testing.T, environ []string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".duolog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".duolog", "config.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DUOLOG_PEER", "")
	t.Setenv("DUOLOG_DEPTH", "")
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "DUOLOG_") {
			t.Setenv(name, value)
		}
	}
	t.Chdir(dir)
	cmd := newRootCommand(newCmdDeps(wiringtest.Cmd(t)))
	var out bytes.Buffer
	cmd.SetIn(strings.NewReader(mcpListInput))
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"mcp"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// launchEnviron is the environment the launch code gives a process of shape.
func launchEnviron(t *testing.T, shape corelaunch.Shape) []string {
	t.Helper()
	p, err := workerenv.Build(corelaunch.ProcessInput{
		Agent: "claude", Shape: shape, Variant: corelaunch.Free,
		Config:  &config.Config{EnvPassthrough: []string{"PATH", "HOME"}},
		Environ: []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()},
		LeadID:  "lead-1", Role: "implement", PeerID: "peer-1", StateDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p.Env
}

func TestKeepMCPWorkerGetsNoTools(t *testing.T) {
	out := mcpToolsListedUnder(t, launchEnviron(t, corelaunch.Worker))
	if !strings.Contains(out, `"tools":[]`) || strings.Contains(out, `"name":"delegate"`) {
		t.Fatalf("worker output = %s", out)
	}
}

func TestPeerGetsNoTools(t *testing.T) {
	env := launchEnviron(t, corelaunch.Peer)
	if !strings.Contains(strings.Join(env, "\n"), "DUOLOG_PEER=peer-1") {
		t.Fatalf("peer environ lacks DUOLOG_PEER: %v", env)
	}
	out := mcpToolsListedUnder(t, env)
	if !strings.Contains(out, `"tools":[]`) || strings.Contains(out, `"name":"delegate"`) {
		t.Fatalf("peer output = %s", out)
	}
}

// A lead's own environment still gets the tools: the test above would pass
// for a server that is always off.
func TestLeadEnvironGetsTools(t *testing.T) {
	out := mcpToolsListedUnder(t, nil)
	if !strings.Contains(out, `"name":"delegate"`) {
		t.Fatalf("lead output = %s", out)
	}
}
