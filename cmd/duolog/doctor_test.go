package main

import (
	"fmt"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	"github.com/lorem-dev/duolog/internal/testbin"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRexFirst puts a fake rex first on PATH, so doctor never finds the
// machine's own rex, in PATH or in an installed Rex app. Call it after the
// test sets its PATH.
func fakeRexFirst(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, "rex", testbin.Scenario{Stdout: `{"blocks":[]}`})
}

func TestDoctorExplainsHookTrustLimit(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
	claude := testbin.Install(t, "claude", testbin.Scenario{})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	fakeRexFirst(t)
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hooks approved=false", "changed after review cannot be detected", "Codex itself will skip", "/hooks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output %q missing %q", out, want)
		}
	}
}

func TestDoctorProbeFlag(t *testing.T) {
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version":    {Stdout: "codex-cli 0.165.0"},
		"debug models": {Stdout: testModelCatalog(t, "luna")},
		"exec":         {Writes: map[string]string{"answer.json": "ok"}},
	}})
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version": {Stdout: "2.1.312 (Claude Code)"},
		"-p":        {Stdout: `{"is_error":false,"result":"ok"}`},
	}})
	t.Setenv("PATH", filepath.Dir(codex)+string(os.PathListSeparator)+filepath.Dir(claude))
	fakeRexFirst(t)
	out, err := executeBusCommand(t, "doctor", "--probe")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codex probe succeeded", "claude probe succeeded"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing %q", out, want)
		}
	}
}

func TestDoctorUsesRexInventoryAndLeadState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rex has no Windows build")
	}
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lead := range []string{"ended", "live"} {
		if err := reg.RegisterLead(session.Lead{ID: lead}); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []session.Entry{
		{ID: "peer-a", Block: "block:orphan-a", Lead: "ended"},
		{ID: "peer-b", Block: "block:orphan-b", Lead: "ended"},
		{ID: "peer-gone", Block: "block:gone", Lead: "ended"},
		{ID: "peer-live", Block: "block:live", Lead: "live"},
		// An ID that looks like a block must not be taken for one: only Block counts.
		{ID: "block:trap", Lead: "ended"},
	} {
		if err := reg.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.Close("ended"); err != nil {
		t.Fatal(err)
	}
	rex := testbin.Install(t, "rex", testbin.Scenario{Exit: 2, Responses: map[string]testbin.Scenario{
		"block ls -s session:s1 --json": {Stdout: `{"blocks":[{"block_id":"block:orphan-a","label":"shell"},{"block_id":"block:orphan-b"},{"block_id":"block:live"},{"block_id":"block:unrelated"},{"block_id":"block:trap"}]}`},
	}})
	// Rex is not in PATH, as in a real Rex session; REX_BIN_DIR finds it. Its
	// rex is a fake too, and Locate reads REX_BIN_DIR before any Rex app.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("REX_BIN_DIR", filepath.Dir(rex))
	t.Setenv("REX_SESSION", "session:s1")
	out, err := executeBusCommand(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"block:orphan-a", "block:orphan-b"} {
		if !strings.Contains(out, "Pane "+id+" belongs to ended lead ended; duolog peer close "+id) {
			t.Fatal(out)
		}
	}
	for _, id := range []string{"block:live", "block:gone", "block:unrelated", "block:trap"} {
		if strings.Contains(out, "duolog peer close "+id) {
			t.Fatal(out)
		}
	}
	calls, err := os.ReadFile(rex + ".calls.ndjson")
	if err != nil || string(calls) != "[\"block\",\"ls\",\"-s\",\"session:s1\",\"--json\"]\n" {
		t.Fatalf("calls=%s err=%v", calls, err)
	}
}

func TestDoctorRexInventoryUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rex has no Windows build")
	}
	for _, scenario := range []testbin.Scenario{
		{Exit: 4, Stderr: "socket unavailable"},
		{Stdout: "invalid JSON"},
		{Stdout: `{}`},
	} {
		t.Run(scenario.Stdout+scenario.Stderr, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Chdir(t.TempDir())
			rex := testbin.Install(t, "rex", scenario)
			t.Setenv("PATH", filepath.Dir(rex))
			t.Setenv("REX_BIN_DIR", "")
			t.Setenv("REX_SESSION", "")
			out, err := executeBusCommand(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "Rex pane inventory unavailable") {
				t.Fatal(out)
			}
		})
	}
}

func TestDoctorRexShadowWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Rex has no Windows build")
	}
	for _, tc := range []struct {
		name     string
		header   string
		symlink  bool
		override bool
		wantWarn bool
	}{
		{name: "Mach-O arm64", header: "\xcf\xfa\xed\xfe\x0c\x00\x00\x01"},
		{name: "ELF", header: "\x7fELF\x02\x01\x01\x00"},
		{name: "symlink to Mach-O", header: "\xcf\xfa\xed\xfe\x0c\x00\x00\x01", symlink: true},
		{name: "symlink to ELF", header: "\x7fELF\x02\x01\x01\x00", symlink: true},
		{name: "shell script", header: "#!/bin/sh\nexit 99\n", wantWarn: true},
		{name: "symlink to script", header: "#!/bin/sh\nexit 99\n", symlink: true, wantWarn: true},
		{name: "explicit script override", header: "#!/bin/sh\nexit 99\n", override: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolatedRouteConfig(t)
			t.Chdir(t.TempDir())
			fallback := fakeRexFirst(t)
			t.Setenv("REX_BIN_DIR", filepath.Dir(fallback))
			dir := t.TempDir()
			path := filepath.Join(dir, "rex")
			if tc.symlink {
				target := filepath.Join(t.TempDir(), "rex-cli")
				if err := os.WriteFile(target, []byte(tc.header), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, []byte(tc.header), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			if tc.override {
				t.Setenv("REX_BIN_DIR", dir)
			}
			wantBin := path
			if tc.wantWarn {
				wantBin = fallback
			}
			if bin, ok := testDeps(t).LocateRex(); !ok || bin != wantBin {
				t.Fatalf("located rex=%q (%t), want %q", bin, ok, wantBin)
			}
			out, err := executeBusCommand(t, "doctor")
			if err != nil {
				t.Fatal(err)
			}
			warning := "rex in PATH (" + path + ") is a script, not the Rex CLI; duolog skips it."
			if strings.Contains(out, warning) != tc.wantWarn {
				t.Fatalf("want shadow warning=%t, got:\n%s", tc.wantWarn, out)
			}
			if tc.wantWarn && !strings.Contains(out, "duolog uses "+fallback) {
				t.Fatalf("warning must name the selected fallback:\n%s", out)
			}
		})
	}
}

func TestCommandsCheckCurrentCheckoutTrust(t *testing.T) {
	for _, command := range []string{"doctor", "install"} {
		for _, workTrusted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/workTrusted=%t", command, workTrusted), func(t *testing.T) {
				isolatedRouteConfig(t)
				main, work := t.TempDir(), t.TempDir()
				mcpLinkedWorktree(t, main, work)
				putWorktreeFile(t, filepath.Join(main, ".duolog", "config.yaml"), "agents:\n  codex:\n    enabled: true\n")
				hooks, err := launchtest.HooksJSON("codex", 30)
				if err != nil {
					t.Fatal(err)
				}
				for _, root := range []string{main, work} {
					putWorktreeFile(t, filepath.Join(root, ".codex", "hooks.json"), string(hooks))
				}
				trusted := main
				if workTrusted {
					trusted = work
				}
				config := fmt.Sprintf("[projects.%q]\ntrust_level = \"trusted\"\n", trusted)
				for _, root := range []string{main, work} {
					config += fmt.Sprintf("[hooks.state.%q]\ntrusted_hash = \"fixture-hash\"\n", filepath.Join(root, ".codex", "hooks.json")+":PreToolUse:0:0")
				}
				path := filepath.Join(os.Getenv("HOME"), ".codex", "config.toml")
				putWorktreeFile(t, path, config)
				putWorktreeFile(t, filepath.Join(work, "nested", "file"), "")
				t.Chdir(filepath.Join(work, "nested"))
				testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
				testbin.Install(t, "claude", testbin.Scenario{})
				fakeRexFirst(t)
				args := []string{command}
				if command == "install" {
					args = append(args, "--scope", "project", "--agent", "codex")
				}
				out, err := executeBusCommand(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				note := "open codex in that folder"
				if workTrusted {
					if strings.Contains(out, note) {
						t.Fatalf("trusted worktree warned: %s", out)
					}
				} else {
					found := false
					for _, line := range strings.Split(out, "\n") {
						if strings.Contains(line, note) && strings.Contains(line, work) && strings.Contains(line, "folder trusted=false") {
							found = true
							if command == "doctor" && !strings.HasPrefix(line, "warning:") {
								t.Fatalf("not warning: %s", line)
							}
						}
					}
					if !found {
						t.Fatalf("missing worktree warning: %s", out)
					}
				}
				after, err := os.ReadFile(path)
				if err != nil || string(after) != config {
					t.Fatalf("trust changed: %s, %v", after, err)
				}
			})
		}
	}
}
