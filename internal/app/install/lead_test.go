package install

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leadDeps are the real installers with a lead window of plan, and an
// environment that names only the test's home: no value of the real shell
// counts. reads counts the window reads.
func leadDeps(t *testing.T, home string, w LeadWindow, environ ...string) (Deps, *int) {
	t.Helper()
	reads := 0
	d := testDeps()
	d.LeadWindow = func(string) (LeadWindow, error) { reads++; return w, nil }
	d.Environ = append([]string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")}, environ...)
	return d, &reads
}

func localSettings(root string) string { return filepath.Join(root, ".claude", "settings.local.json") }

// localEnv reads the env block of the local settings, nil when the file is gone.
func localEnv(t *testing.T, root string) (map[string]any, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(localSettings(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	env, _ := doc["env"].(map[string]any)
	return doc, env
}

func leadRun(t *testing.T, d Deps, o Options) Report {
	t.Helper()
	r, err := d.Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func claudeStatus(t *testing.T, d Deps, o Options) Status {
	t.Helper()
	statuses, err := d.Check(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		if s.Agent == "claude" {
			return s
		}
	}
	t.Fatal("no claude status")
	return Status{}
}

func hasNote(r Report, parts ...string) bool {
	for _, n := range r.Notes {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(n, p)
		}
		if all {
			return true
		}
	}
	return false
}

func TestInstallSetsTheLeadWindowOfThePlanAndUninstallRemovesIt(t *testing.T) {
	home, root := setup(t)
	d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	r := leadRun(t, d, o)
	if _, env := localEnv(t, root); env[CompactVar] != "500000" {
		t.Fatalf("env %v", env)
	}
	if !hasNote(r, "claude lead", "500000", "plan max", ".claude/settings.local.json") {
		t.Fatalf("notes %q", r.Notes)
	}
	s := claudeStatus(t, d, o)
	if s.Stale() || s.Lead == nil || s.Lead.Window != 500000 || s.Lead.Missing {
		t.Fatalf("status %+v lead %+v", s, s.Lead)
	}
	if line := d.LeadLine(root, home); !strings.Contains(line, "500000 tokens for plan max, set by duolog") {
		t.Fatalf("lead line %q", line)
	}
	// A second install changes nothing.
	if r := leadRun(t, d, o); len(r.Files) != 0 {
		t.Fatalf("second install wrote %v", r.Files)
	}
	if err := d.Remove(o); err != nil {
		t.Fatal(err)
	}
	// install created the file, so uninstall removes it with the marker.
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatalf("local settings left: %v", err)
	}
	if _, err := os.Stat(markerPath(root)); !os.IsNotExist(err) {
		t.Fatalf("marker left: %v", err)
	}
}

func TestLeadWindowKeepsTheOtherLocalSettings(t *testing.T) {
	home, root := setup(t)
	write(t, localSettings(root), `{"permissions":{"allow":["Bash(ls)"]},"env":{"KEEP":"1"}}`)
	d, _ := leadDeps(t, home, LeadWindow{Window: 250000, Plan: "pro"})
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	leadRun(t, d, o)
	doc, env := localEnv(t, root)
	if env["KEEP"] != "1" || env[CompactVar] != "250000" || doc["permissions"] == nil {
		t.Fatalf("doc %v", doc)
	}
	if err := d.Remove(o); err != nil {
		t.Fatal(err)
	}
	doc, env = localEnv(t, root)
	if _, ok := env[CompactVar]; ok || env["KEEP"] != "1" || doc["permissions"] == nil {
		t.Fatalf("after uninstall %v", doc)
	}
}

func TestLeadWindowFollowsAPlanChange(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
	leadRun(t, d, o)
	d, _ = leadDeps(t, home, LeadWindow{Window: 250000, Plan: "pro"})
	leadRun(t, d, o)
	if _, env := localEnv(t, root); env[CompactVar] != "250000" {
		t.Fatalf("env %v", env)
	}
	// Window 0, Claude's own default, takes duolog's value out again.
	d, _ = leadDeps(t, home, LeadWindow{Window: 0, Plan: "team"})
	r := leadRun(t, d, o)
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatalf("local settings left after window 0: %v", err)
	}
	if !hasNote(r, "Claude's own default", "plan team") {
		t.Fatalf("notes %q", r.Notes)
	}
	if s := claudeStatus(t, d, o); s.Stale() || s.Lead == nil || s.Lead.Missing {
		t.Fatalf("status %+v lead %+v", s, s.Lead)
	}
}

// A value the person set stays, wherever it is: the local file, another
// settings file, or the environment when no settings file sets one. The
// window is not read, and a value install wrote earlier goes, since the
// local file would apply over theirs.
func TestLeadWindowKeepsThePersonsOwnValue(t *testing.T) {
	for _, c := range []struct {
		name, file, where string
		environ           []string
	}{
		{name: "local", file: ".claude/settings.local.json", where: ".claude/settings.local.json"},
		{name: "project", file: ".claude/settings.json", where: ".claude/settings.json"},
		{name: "user", file: "HOME/.claude/settings.json", where: "settings.json"},
		{name: "environment", environ: []string{CompactVar + "=300000"}, where: "the environment"},
	} {
		t.Run(c.name, func(t *testing.T) {
			home, root := setup(t)
			o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
			if c.file != "" && c.name != "local" {
				// duolog's value from an earlier install.
				d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
				leadRun(t, d, o)
			}
			if c.file != "" {
				path := filepath.Join(root, c.file)
				if rest, ok := strings.CutPrefix(c.file, "HOME/"); ok {
					path = filepath.Join(home, rest)
				}
				write(t, path, `{"env":{"`+CompactVar+`":"300000"}}`)
			}
			d, reads := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"}, c.environ...)
			r := leadRun(t, d, o)
			if *reads != 0 {
				t.Fatal("the window was read although the person set one")
			}
			if !hasNote(r, CompactVar+"=300000", c.where, "duolog keeps it") {
				t.Fatalf("notes %q", r.Notes)
			}
			_, env := localEnv(t, root)
			want := ""
			if c.name == "local" {
				want = "300000"
			}
			if got, _ := env[CompactVar].(string); got != want {
				t.Fatalf("local value %q, want %q", got, want)
			}
			if s := claudeStatus(t, d, o); s.Stale() || s.Lead == nil || s.Lead.Own == "" {
				t.Fatalf("status %+v lead %+v", s, s.Lead)
			}
			if err := d.Remove(o); err != nil {
				t.Fatal(err)
			}
			if c.file != "" {
				path := filepath.Join(root, c.file)
				if rest, ok := strings.CutPrefix(c.file, "HOME/"); ok {
					path = filepath.Join(home, rest)
				}
				if !strings.Contains(read(t, path), "300000") {
					t.Fatal("uninstall removed the person's value")
				}
			}
		})
	}
}

// A value the person changed after install is theirs: uninstall keeps it.
func TestUninstallKeepsALeadValueThePersonChanged(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
	leadRun(t, d, o)
	write(t, localSettings(root), `{"env":{"`+CompactVar+`":"400000"}}`)
	if err := d.Remove(o); err != nil {
		t.Fatal(err)
	}
	if _, env := localEnv(t, root); env[CompactVar] != "400000" {
		t.Fatalf("env %v", env)
	}
}

// The environment of a lead's shell holds its settings' value, so a value
// there counts as the person's only when no settings file sets one.
func TestLeadWindowIgnoresTheEnvironmentOverItsOwnValue(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
	leadRun(t, d, o)
	d, _ = leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"}, CompactVar+"=500000")
	leadRun(t, d, o)
	if _, env := localEnv(t, root); env[CompactVar] != "500000" {
		t.Fatalf("env %v", env)
	}
}

func TestInstallCheckNamesAMissingLeadWindow(t *testing.T) {
	home, root := setup(t)
	o := Options{Scope: "project", Agent: "claude", Root: root, Home: home}
	// An install by a duolog that set no lead window.
	if _, err := testDeps().Run(o); err != nil {
		t.Fatal(err)
	}
	d, _ := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
	s := claudeStatus(t, d, o)
	if !s.Stale() || !strings.Contains(s.Line(root), "lead compact window not set in .claude/settings.local.json") {
		t.Fatalf("line %q", s.Line(root))
	}
	if line := d.LeadLine(root, home); line != "" {
		t.Fatalf("lead line %q", line)
	}
	// The plain deps check no lead window, and hooks alone none either.
	if s := claudeStatus(t, testDeps(), o); s.Stale() || s.Lead != nil {
		t.Fatalf("plain deps: %+v", s)
	}
	o.HooksOnly = true
	if s := claudeStatus(t, d, o); s.Lead != nil {
		t.Fatalf("hooks only: %+v", s.Lead)
	}
}

func TestLeadWindowErrorIsANote(t *testing.T) {
	home, root := setup(t)
	d, _ := leadDeps(t, home, LeadWindow{})
	d.LeadWindow = func(string) (LeadWindow, error) { return LeadWindow{}, errors.New("config broken") }
	r := leadRun(t, d, Options{Scope: "project", Agent: "claude", Root: root, Home: home})
	if !hasNote(r, "compact window not set: config broken") {
		t.Fatalf("notes %q", r.Notes)
	}
	if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
		t.Fatal("local settings written")
	}
}

func TestUserScopeAndCodexLeaveTheLeadAlone(t *testing.T) {
	for _, o := range []Options{{Scope: "user", Agent: "claude"}, {Scope: "project", Agent: "codex"}} {
		home, root := setup(t)
		o.Root, o.Home = root, home
		d, reads := leadDeps(t, home, LeadWindow{Window: 500000, Plan: "max"})
		leadRun(t, d, o)
		if *reads != 0 {
			t.Fatalf("%+v: window read", o)
		}
		if _, err := os.Stat(localSettings(root)); !os.IsNotExist(err) {
			t.Fatalf("%+v: local settings written", o)
		}
	}
}
