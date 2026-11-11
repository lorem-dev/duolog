package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakePluginInstall records a successful CLI install in the real adapter's
// state files. No test starts a Claude or Codex process.
func fakePluginInstall(t *testing.T, agent, home string) {
	t.Helper()
	if agent == "codex" {
		write(t, filepath.Join(home, ".codex", "config.toml"), "[plugins.\"duolog@duolog\"]\nenabled = true\n")
		return
	}
	path := filepath.Join(home, ".claude", "plugins", "cache", "duolog")
	hooks, err := PluginHooksJSON("claude", GateTimeout)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(path, "hooks", "hooks.json"), string(hooks))
	registry := map[string]any{"plugins": map[string]any{"duolog@duolog": []any{map[string]any{"scope": "user", "installPath": path}}}}
	data, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), string(data))
	write(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"duolog@duolog":true}}`)
}

// TestInstallPlugins covers both scopes and agents, missing marketplaces,
// state rereads and the absence of duplicate Claude hooks and skills.
func TestInstallPlugins(t *testing.T) {
	for _, scope := range []string{"project", "user"} {
		for _, name := range []string{"claude", "codex"} {
			for _, listed := range []bool{false, true} {
				t.Run(scope+"/"+name+"/listed="+stringBool(listed), func(t *testing.T) {
					home, root := setup(t)
					d := testDeps()
					d.Available, d.Release = func(string) bool { return true }, true
					var calls []string
					d.RunPlugin = func(ctx context.Context, dir string, env []string, agent string, args ...string) ([]byte, error) {
						checkPluginCommand(t, ctx, dir, root, env)
						if agent != name {
							t.Fatalf("ran unselected agent %s", agent)
						}
						command := strings.Join(args, " ")
						calls = append(calls, command)
						if command == "plugin marketplace list" {
							if listed {
								return []byte("duolog\n"), nil
							}
							return nil, nil
						}
						if strings.HasSuffix(command, "duolog@duolog") {
							fakePluginInstall(t, agent, home)
						}
						return nil, nil
					}
					o := Options{Scope: scope, Agent: name, Home: home, Root: root}
					r, err := d.Run(o)
					if err != nil {
						t.Fatal(err)
					}
					want := []string{"plugin marketplace list"}
					if !listed {
						want = append(want, "plugin marketplace add lorem-dev/duolog")
					}
					verb := "install"
					if name == "codex" {
						verb = "add"
					}
					want = append(want, "plugin "+verb+" duolog@duolog")
					if !slices.Equal(calls, want) {
						t.Fatalf("commands %v, want %v", calls, want)
					}
					assertPluginFiles(t, d, o, r)
					calls = nil
					if _, err := d.Run(o); err != nil || len(calls) != 0 {
						t.Fatalf("repeat install: commands %v, error %v", calls, err)
					}
				})
			}
		}
	}
}

// stringBool names the two marketplace list cases in test output.
func stringBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// checkPluginCommand verifies that every injected command has the requested
// project, environment and its own one-minute deadline.
func checkPluginCommand(t *testing.T, ctx context.Context, dir, root string, environ []string) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if left := time.Until(deadline); !ok || left <= 59*time.Second || left > time.Minute {
		t.Fatalf("command deadline %v, present %v", deadline, ok)
	}
	if dir != root || !slices.Equal(environ, os.Environ()) {
		t.Fatalf("command directory %q or environment differs", dir)
	}
}

// assertPluginFiles checks that Claude uses the freshly installed plugin's
// hooks and skills while Codex still receives its native hooks.
func assertPluginFiles(t *testing.T, d Deps, o Options, r Report) {
	t.Helper()
	a := d.Agents[0]
	if o.Agent == "codex" {
		a = d.Agents[1]
	}
	base := o.Root
	if o.Scope == "user" {
		base = o.Home
	}
	path := a.Installer.HookFile(o.Scope, base, o.Home)
	data, _ := os.ReadFile(path)
	parts := a.Installer.(HookParts)
	hooks, err := parts.OurHooks(data)
	if err != nil || (len(hooks) > 0) != (o.Agent == "codex") {
		t.Fatalf("%s hooks %v, error %v", o.Agent, hooks, err)
	}
	if o.Agent == "claude" {
		if _, err := os.Stat(installedSkillPath(o, "", "claude")); !os.IsNotExist(err) {
			t.Fatalf("plugin install wrote duplicate skill: %v", err)
		}
		if !strings.Contains(strings.Join(r.Notes, "\n"), "plugin is enabled") {
			t.Fatalf("plugin state was not reread: %v", r.Notes)
		}
	}
}

// TestPluginFailuresKeepNativeInstall verifies every failed command and
// timeout leaves native installation working with the exact manual commands.
func TestPluginFailuresKeepNativeInstall(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		for _, scope := range []string{"project", "user"} {
			for failed := 1; failed <= 3; failed++ {
				for _, failure := range []error{errors.New("failed"), context.DeadlineExceeded} {
					home, root := setup(t)
					d := testDeps()
					d.Release, d.Available = true, func(string) bool { return true }
					calls := 0
					d.RunPlugin = func(context.Context, string, []string, string, ...string) ([]byte, error) {
						calls++
						if calls == failed {
							return nil, failure
						}
						return nil, nil
					}
					o := Options{Scope: scope, Agent: name, Home: home, Root: root}
					r, err := d.Run(o)
					if err != nil || calls != failed {
						t.Fatalf("%s %s command %d: calls %d, error %v", name, scope, failed, calls, err)
					}
					assertPluginNote(t, name, strings.Join(r.Notes, "\n"))
					if _, err := os.Stat(installedSkillPath(o, "", name)); err != nil {
						t.Fatalf("native fallback skill missing: %v", err)
					}
				}
			}
		}
	}
}

// assertPluginNote checks both manual commands for an absent plugin.
func assertPluginNote(t *testing.T, name, note string) {
	t.Helper()
	verb := "install"
	if name == "codex" {
		verb = "add"
	}
	for _, want := range []string{name + " plugin marketplace add lorem-dev/duolog", name + " plugin " + verb + " duolog@duolog"} {
		if !strings.Contains(note, want) {
			t.Fatalf("missing %q in %q", want, note)
		}
	}
}

// TestPluginSetupSkipsCommands covers development builds, opt-out, missing
// CLIs and read-only checks, including their notes and unchanged files.
func TestPluginSetupSkipsCommands(t *testing.T) {
	for _, mode := range []string{"development", "no-plugin", "missing-cli", "check"} {
		t.Run(mode, func(t *testing.T) {
			home, root := setup(t)
			d := testDeps()
			d.Release = mode != "development"
			d.Available = func(string) bool { return mode != "missing-cli" }
			d.RunPlugin = func(context.Context, string, []string, string, ...string) ([]byte, error) {
				t.Fatal("plugin command ran")
				return nil, nil
			}
			o := Options{Home: home, Root: root, NoPlugin: mode == "no-plugin"}
			var notes []string
			if mode == "check" {
				beforeHome, beforeRoot := snapshot(t, home), snapshot(t, root)
				statuses, err := d.Check(o)
				if err != nil {
					t.Fatal(err)
				}
				for _, s := range statuses {
					notes = append(notes, s.Lines(root)...)
				}
				if !equal(beforeHome, snapshot(t, home)) || !equal(beforeRoot, snapshot(t, root)) {
					t.Fatal("check changed files")
				}
			} else {
				r, err := d.Run(o)
				if err != nil {
					t.Fatal(err)
				}
				notes = r.Notes
			}
			text := strings.Join(notes, "\n")
			if mode == "development" || mode == "check" {
				for _, name := range []string{"claude", "codex"} {
					assertPluginNote(t, name, text)
				}
			} else if strings.Contains(text, "marketplace add") {
				t.Fatalf("unexpected plugin note: %s", text)
			}
		})
	}
}
