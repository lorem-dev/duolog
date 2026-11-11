package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/install"
)

// TestConfiguredInstallRelease permits automatic plugin setup only for the
// release version printed by duolog version, at both installation scopes.
func TestConfiguredInstallRelease(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	for _, tc := range []struct {
		version string
		release bool
	}{
		{"1.8.2", true},
		{"v1.8.2", true},
		{"dev", false},
		{"1.8.2-dev", false},
		{"1.8.2-4-gabcdef", false},
	} {
		version = tc.version
		for _, scope := range []string{"user", "project"} {
			d, err := configuredInstall(testDeps(t), install.Options{Scope: scope, Root: t.TempDir()})
			if err != nil || d.Release != tc.release {
				t.Fatalf("version %s scope %s: release %v error %v", version, scope, d.Release, err)
			}
		}
	}
}

// TestInstallPluginFlags checks that --no-plugin reaches both install and
// check, and that check reports missing plugins without starting commands.
func TestInstallPluginFlags(t *testing.T) {
	for _, check := range []bool{false, true} {
		for _, noPlugin := range []bool{false, true} {
			home, root := t.TempDir(), t.TempDir()
			t.Chdir(root)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			d := testDeps(t)
			d.Install.Available = func(string) bool { return true }
			calls := 0
			d.Install.RunPlugin = func(context.Context, string, []string, string, ...string) ([]byte, error) {
				calls++
				return nil, context.DeadlineExceeded
			}
			cmd := newRootCommand(d)
			var out bytes.Buffer
			cmd.SetOut(&out)
			args := []string{"install", "--scope", "user", "--agent", "codex"}
			if check {
				args = append(args, "--check")
			}
			if noPlugin {
				args = append(args, "--no-plugin")
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			wantCalls := 0
			if !check && !noPlugin {
				wantCalls = 1
			}
			if calls != wantCalls || strings.Contains(out.String(), "codex plugin marketplace add lorem-dev/duolog") == noPlugin {
				t.Fatalf("check %v no-plugin %v: calls %d, output %s", check, noPlugin, calls, out.String())
			}
		}
	}
}
