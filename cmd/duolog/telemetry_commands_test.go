package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// telemetryCommandSetup is a duolog project with collection on in a temporary
// home, run from a person's shell: no model marks the environment. It returns
// the project root and the telemetry root.
func telemetryCommandSetup(t *testing.T) (root, telemetryRoot string) {
	t.Helper()
	telemetryRoot = telemetryHome(t)
	clearModelMarkers(t)
	root = filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	if _, err := store.Open(root); err != nil {
		t.Fatal(err)
	}
	return root, telemetryRoot
}

// noticeText is the first collecting session's notice.
const noticeText = "Telemetry is local only; turn it off with duolog telemetry off."

// clearModelMarkers removes the marks a model's tool call leaves, so a test
// run from a Claude or Codex session still reads as the person's terminal.
func clearModelMarkers(t *testing.T) {
	t.Helper()
	for _, name := range modelMarkers {
		t.Setenv(name, "")
	}
}

// asTerminal makes stdin and stdout read as the person's terminal.
func asTerminal(t *testing.T, terminal bool) {
	t.Helper()
	testDeps(t).IsTerminal = func(any) bool { return terminal }
}

func telemetryCommand(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRootCommand(testDeps(t))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"telemetry"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// seedStore opens the store of the project at root with its clock at at.
func seedStore(t *testing.T, root string, at time.Time) *telemetry.Store {
	t.Helper()
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	o := telemetryOptions(cfg, root)
	o.Now = func() time.Time { return at }
	s, err := telemetry.Open(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func seedRun(t *testing.T, s *telemetry.Store, agent, outcome string) {
	t.Helper()
	family := "sonnet"
	if agent == "codex" {
		family = "sol"
	}
	e := telemetry.Run{Role: "implement", Agent: agent, Family: family, Effort: "medium", Placement: "worker", Attempt: 1,
		In: 100, Cached: 40, Out: 10, UsageKnown: true, Secs: 3, Outcome: outcome, Err: "none", Reason: "none"}
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
}

// projectID is this project's partition under the salt on disk.
func projectID(t *testing.T, root string) string {
	t.Helper()
	st, err := seedStore(t, root, time.Now()).Status()
	if err != nil || st.ProjectID == "" {
		t.Fatalf("no project id: %+v %v", st, err)
	}
	return st.ProjectID
}

// plantExpired puts an expired segment in partition and dates the prune
// stamp stale when stale is set, so a prune that is due removes it.
func plantExpired(t *testing.T, telemetryRoot, partition string, stale bool) string {
	t.Helper()
	dir := filepath.Join(telemetryRoot, "v1", partition)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().AddDate(0, 0, -40).Format(time.DateOnly)
	path := filepath.Join(dir, day+".ndjson")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if stale {
		at := time.Now().Add(-7 * time.Hour)
		if err := os.Chtimes(filepath.Join(telemetryRoot, "prune.stamp"), at, at); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// treeState lists every file under dir with its size and mtime, and every
// directory with no time. A file added or removed shows as an entry of its
// own. On NTFS a directory's mtime can land milliseconds after the write in
// it, so a snapshot taken right after a setup that wrote files saw it change
// with no command run; the directory's own time therefore does not count.
func treeState(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "dir"
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files[path] = info.ModTime().String() + " " + strconv.FormatInt(info.Size(), 10)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func userConfigPath(t *testing.T) string {
	t.Helper()
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "duolog", "config.yaml")
}

func TestTelemetryCommands(t *testing.T) {
	t.Run("status all fields", func(t *testing.T) {
		root, telemetryRoot := telemetryCommandSetup(t)
		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		s := seedStore(t, root, yesterday)
		seedRun(t, s, "claude", "done")
		if err := s.Append(telemetry.Reset{Agent: "claude", Reason: "manual"}); err != nil {
			t.Fatal(err)
		}
		id := projectID(t, root)
		out, _, err := telemetryCommand(t, "", "status")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			"telemetry: on\n", "project events: on\n", "root: " + telemetryRoot + "\n", "project id: " + id + "\n",
			"oldest day: " + yesterday.Format(time.DateOnly) + "\n", "segments: 2\n", "partitions: 2\n", "size: ",
			"last prune: " + time.Now().UTC().Format(time.DateOnly), "retention days: 30\n", "prune every hours: 6\n",
			"max megabytes: 16\n", "dropped since process start: ", "failed since process start: ",
			"protection: mode fixed no, backup verified ", "index verified ", "refusal none\n",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("status lacks %q:\n%s", want, out)
			}
		}
	})

	t.Run("show since project and json", func(t *testing.T) {
		root, _ := telemetryCommandSetup(t)
		now := time.Now().UTC()
		seedRun(t, seedStore(t, root, now.AddDate(0, 0, -1)), "claude", "done")
		seedRun(t, seedStore(t, root, now.AddDate(0, 0, -10)), "claude", "failed")
		other := filepath.Join(t.TempDir(), "other")
		if err := os.MkdirAll(other, 0700); err != nil {
			t.Fatal(err)
		}
		seedRun(t, seedStore(t, other, now.AddDate(0, 0, -1)), "codex", "done")

		report := func(args ...string) telemetry.Report {
			t.Helper()
			out, _, err := telemetryCommand(t, "", append([]string{"show", "--json"}, args...)...)
			if err != nil {
				t.Fatal(err)
			}
			var r telemetry.Report
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatalf("%v:\n%s", err, out)
			}
			return r
		}
		rows := func(r telemetry.Report) []string {
			var got []string
			for _, d := range r.Daily {
				got = append(got, d.Agent+"/"+d.Outcome)
			}
			return got
		}
		// The default is seven days of this project.
		if got := rows(report()); !slices.Equal(got, []string{"claude/done"}) {
			t.Fatalf("default show: %v", got)
		}
		if got := rows(report("--since", "14d")); !slices.Equal(got, []string{"claude/failed", "claude/done"}) {
			t.Fatalf("since 14d: %v", got)
		}
		if got := rows(report("--project", "here")); !slices.Equal(got, []string{"claude/done"}) {
			t.Fatalf("project here: %v", got)
		}
		all := report("--project", "all")
		if got := rows(all); !slices.Contains(got, "codex/done") || !slices.Contains(got, "claude/done") || len(got) != 2 {
			t.Fatalf("project all: %v", got)
		}
		if d := all.Daily[0]; d.Runs != 1 || d.In != 100 || d.Cached != 40 || d.Out != 10 || d.Secs != 3 || d.Role != "implement" {
			t.Fatalf("aggregate: %+v", d)
		}
		if all.Quota == nil || all.Moves == nil {
			t.Fatal("json lists must be empty, not null")
		}
		out, _, err := telemetryCommand(t, "", "show")
		if err != nil || !strings.Contains(out, "claude") || !strings.Contains(out, "implement") || strings.Contains(out, "codex") {
			t.Fatalf("table: %v\n%s", err, out)
		}
	})

	t.Run("prune force", func(t *testing.T) {
		root, telemetryRoot := telemetryCommandSetup(t)
		seedRun(t, seedStore(t, root, time.Now()), "claude", "done")
		if _, _, err := telemetryCommand(t, "", "status"); err != nil {
			t.Fatal(err)
		}
		// The stamp is fresh: status leaves the segment, prune removes it.
		expired := plantExpired(t, telemetryRoot, projectID(t, root), false)
		if _, _, err := telemetryCommand(t, "", "status"); err != nil || !exists(expired) {
			t.Fatalf("status pruned before the interval: %v", err)
		}
		out, _, err := telemetryCommand(t, "", "prune")
		if err != nil || exists(expired) || !strings.Contains(out, "pruned: 1 deleted") {
			t.Fatalf("prune: %v %q", err, out)
		}
	})

	t.Run("purge here and all", func(t *testing.T) {
		root, telemetryRoot := telemetryCommandSetup(t)
		now := time.Now()
		seedRun(t, seedStore(t, root, now), "claude", "done")
		other := filepath.Join(t.TempDir(), "other")
		if err := os.MkdirAll(other, 0700); err != nil {
			t.Fatal(err)
		}
		seedRun(t, seedStore(t, other, now), "codex", "done")
		if err := seedStore(t, root, now).Append(telemetry.Reset{Agent: "claude", Reason: "manual"}); err != nil {
			t.Fatal(err)
		}
		notice := filepath.Join(telemetryRoot, "notice.v1")
		if err := os.WriteFile(notice, []byte("notice.v1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		here := filepath.Join(telemetryRoot, "v1", projectID(t, root))
		there := filepath.Join(telemetryRoot, "v1", projectID(t, other))
		account := filepath.Join(telemetryRoot, "v1", "_account")

		// Without --yes the person confirms in a terminal; redirected stdin cannot.
		asTerminal(t, false)
		if _, _, err := telemetryCommand(t, "yes\n", "purge"); err == nil || !exists(here) {
			t.Fatalf("redirected stdin purged: %v", err)
		}
		asTerminal(t, true)
		if _, _, err := telemetryCommand(t, "no\n", "purge"); err == nil || !exists(here) {
			t.Fatalf("a refusal purged: %v", err)
		}
		out, stderr, err := telemetryCommand(t, "yes\n", "purge")
		if err != nil || exists(here) || !exists(there) || !exists(account) || !strings.Contains(stderr, "this project") {
			t.Fatalf("purge here: %v %q %q", err, out, stderr)
		}
		asTerminal(t, false)
		if _, _, err := telemetryCommand(t, "", "purge", "--all", "--yes"); err != nil {
			t.Fatal(err)
		}
		if exists(there) || exists(account) || exists(filepath.Join(telemetryRoot, "salt")) {
			t.Fatal("purge --all left history or the salt")
		}
		if !exists(notice) {
			t.Fatal("purge --all removed the notice marker, so the first notice would repeat")
		}
	})

	t.Run("on and off edit the user config", func(t *testing.T) {
		root, _ := telemetryCommandSetup(t)
		path := userConfigPath(t)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		const user = "# my settings\nlearning:\n  enabled: true # keep\ntelemetry:\n  retention_days: 10\n"
		if err := os.WriteFile(path, []byte(user), 0600); err != nil {
			t.Fatal(err)
		}
		projectConfig := filepath.Join(root, ".duolog", "config.yaml")
		if err := os.WriteFile(projectConfig, []byte("coauthors:\n  enabled: false\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{false, true} {
			verb := map[bool]string{true: "on", false: "off"}[enabled]
			if _, _, err := telemetryCommand(t, "", verb); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"# my settings", "enabled: true # keep", "retention_days: 10", "enabled: " + strconv.FormatBool(enabled)} {
				if !strings.Contains(string(data), want) {
					t.Fatalf("%s lost %q:\n%s", verb, want, data)
				}
			}
			cfg, err := configfile.Load(root)
			if err != nil || cfg.Telemetry.Enabled != enabled || cfg.Telemetry.RetentionDays != 10 || !cfg.Learning.Enabled {
				t.Fatalf("%s: %+v %v", verb, cfg.Telemetry, err)
			}
			if got, _ := os.ReadFile(projectConfig); string(got) != "coauthors:\n  enabled: false\n" {
				t.Fatalf("%s changed the project config: %s", verb, got)
			}
			if info, err := os.Stat(path); err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
				t.Fatalf("user config mode: %v %v", info, err)
			}
		}
		// A missing user config is created with the switch only.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, _, err := telemetryCommand(t, "", "off"); err != nil {
			t.Fatal(err)
		}
		if cfg, err := configfile.Load(root); err != nil || cfg.Telemetry.Enabled {
			t.Fatalf("created config: %v", err)
		}
	})

	t.Run("invalid input is rejected", func(t *testing.T) {
		telemetryCommandSetup(t)
		for _, args := range [][]string{
			{"show", "--since", "0d"}, {"show", "--since", "31d"}, {"show", "--since", "7"}, {"show", "--since", "-1d"},
			{"show", "--since", "7w"}, {"show", "--since", "1d2h"}, {"show", "--since", ""}, {"show", "--project", "other"},
			{"show", "--project", ""}, {"show", "extra"}, {"purge", "--project", "there", "--yes"},
			{"purge", "--project", "here", "--all", "--yes"}, {"status", "now"}, {"prune", "--force"}, {"on", "now"},
			{"show", "--root", "/tmp"}, {"status", "--root", "/tmp"}, {"show", "--output", "x.json"}, {"show", "--json=maybe"},
		} {
			if _, _, err := telemetryCommand(t, "", args...); err == nil {
				t.Errorf("%v accepted", args)
			}
		}
	})

	t.Run("no export and no root override", func(t *testing.T) {
		_, telemetryRoot := telemetryCommandSetup(t)
		if _, _, err := telemetryCommand(t, "", "export"); err == nil {
			t.Fatal("export exists")
		}
		for _, sub := range newTelemetryCommand(testDeps(t)).Commands() {
			if sub.Name() == "export" || sub.Flags().Lookup("root") != nil || sub.Flags().Lookup("output") != nil {
				t.Fatalf("%s offers a destination", sub.Name())
			}
		}
		t.Setenv("DUOLOG_TELEMETRY_ROOT", t.TempDir())
		out, _, err := telemetryCommand(t, "", "status")
		if err != nil || !strings.Contains(out, "root: "+telemetryRoot+"\n") {
			t.Fatalf("root moved: %v\n%s", err, out)
		}
	})

	t.Run("commands maintain existing data while off", func(t *testing.T) {
		for _, args := range [][]string{{"status"}, {"show"}, {"prune"}, {"purge", "--yes"}} {
			t.Run(args[0], func(t *testing.T) {
				root, telemetryRoot := telemetryCommandSetup(t)
				seedRun(t, seedStore(t, root, time.Now()), "claude", "done")
				expired := plantExpired(t, telemetryRoot, "_account", true)
				t.Setenv("DUOLOG_TELEMETRY", "off")
				if _, _, err := telemetryCommand(t, "", args...); err != nil {
					t.Fatal(err)
				}
				if exists(expired) {
					t.Fatalf("%v left an expired segment while off", args)
				}
			})
		}
	})

	t.Run("off reads this project without creating anything", func(t *testing.T) {
		root, telemetryRoot := telemetryCommandSetup(t)
		// The seeded store holds the root open until it closes, and Windows
		// refuses to remove a folder with an open handle; the root goes below.
		seeded := seedStore(t, root, time.Now())
		seedRun(t, seeded, "claude", "done")
		if err := seeded.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, err := telemetryCommand(t, "", "prune"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DUOLOG_TELEMETRY", "off")
		before := treeState(t, telemetryRoot)
		out, _, err := telemetryCommand(t, "", "show", "--project", "here", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var r telemetry.Report
		if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Daily) != 1 || r.Daily[0].Agent != "claude" {
			t.Fatalf("off show here: %v %s", err, out)
		}
		if after := treeState(t, telemetryRoot); !mapsEqual(before, after) {
			t.Fatalf("show changed the store while off:\n%v\n%v", before, after)
		}

		// Status while off never recreates a salt that is gone.
		if err := os.Remove(filepath.Join(telemetryRoot, "salt")); err != nil {
			t.Fatal(err)
		}
		if out, _, err := telemetryCommand(t, "", "status"); err != nil || !strings.Contains(out, "telemetry: off\n") || !strings.Contains(out, "project id: none\n") {
			t.Fatalf("off status: %v\n%s", err, out)
		}
		if exists(filepath.Join(telemetryRoot, "salt")) {
			t.Fatal("status while off created a salt")
		}

		// With no root at all, nothing is created.
		if err := os.RemoveAll(telemetryRoot); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"status"}, {"show", "--project", "here"}, {"prune"}, {"purge", "--all", "--yes"}} {
			if _, _, err := telemetryCommand(t, "", args...); err != nil || exists(telemetryRoot) {
				t.Fatalf("%v while off: %v, root created %v", args, err, exists(telemetryRoot))
			}
		}
	})
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Every command closes the stores it opens. An open store holds its root, and
// Windows refuses to remove or rename a folder with an open handle, so a store
// left open breaks a purge, a prune or the person's own cleanup there. The
// counter shows a leak on every OS.
func TestCommandsCloseTheStore(t *testing.T) {
	root, telemetryRoot := telemetryCommandSetup(t)
	open := telemetry.OpenStores()

	// The counter sees a store that stays open.
	leaked := seedStore(t, root, time.Now())
	if telemetry.OpenStores() != open+1 {
		t.Fatalf("an open store is not counted: %d, want %d", telemetry.OpenStores(), open+1)
	}
	seedRun(t, leaked, "claude", "done")
	if err := leaked.Close(); err != nil || telemetry.OpenStores() != open {
		t.Fatalf("a closed store is still counted: %d, want %d, %v", telemetry.OpenStores(), open, err)
	}

	check := func(what string) {
		t.Helper()
		if got := telemetry.OpenStores(); got != open {
			t.Fatalf("%s left %d stores open", what, got-open)
		}
	}
	commands := [][]string{
		{"status"}, {"show"}, {"show", "--json", "--project", "all"}, {"prune"},
		{"purge", "--yes"}, {"purge", "--all", "--yes"}, {"off"}, {"on"},
	}
	for _, off := range []bool{false, true} {
		if off {
			t.Setenv("DUOLOG_TELEMETRY", "off")
		}
		plantExpired(t, telemetryRoot, "_account", true)
		for _, args := range commands {
			if _, _, err := telemetryCommand(t, "", args...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			check(fmt.Sprintf("telemetry %v (off %t)", args, off))
		}
		e2eSessionStart(t, "claude", root)
		check(fmt.Sprintf("session start (off %t)", off))
	}

	t.Setenv("DUOLOG_TELEMETRY", "")
	p := newDelegationProject(t, "delegating", reviewed("Reviewed."))
	if out, err := p.run("review_final"); err != nil {
		t.Fatalf("delegate: %q %v", out, err)
	}
	if len(runs(t, p.root)) != 1 {
		t.Fatal("the delegation wrote no run event, so it opened no store")
	}
	check("delegate")
}

// A worker runs no telemetry command: each refuses before it reads the
// config, opens the store or writes the user switch.
func TestTelemetryWorkerCommands(t *testing.T) {
	root, telemetryRoot := telemetryCommandSetup(t)
	seedRun(t, seedStore(t, root, time.Now()), "claude", "done")
	plantExpired(t, telemetryRoot, "_account", true)
	// A config the commands would fail to load.
	if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("telemetry: [broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := treeState(t, telemetryRoot)
	t.Setenv("DUOLOG_DEPTH", "1")
	t.Setenv("DUOLOG_ROLE", "implement")
	for _, args := range [][]string{
		{}, {"status"}, {"show"}, {"show", "--json", "--project", "all"}, {"prune"}, {"purge", "--yes"},
		{"purge", "--all", "--yes"}, {"on"}, {"off"},
	} {
		_, _, err := telemetryCommand(t, "yes\n", args...)
		if err == nil || !strings.Contains(err.Error(), "only the person") {
			t.Fatalf("worker %v: %v", args, err)
		}
	}
	if after := treeState(t, telemetryRoot); !mapsEqual(before, after) {
		t.Fatal("a worker command touched the store")
	}
	if exists(userConfigPath(t)) {
		t.Fatal("a worker command wrote the user switch")
	}
}

// Model output carries only coarse health: the hook context and a doctor
// that a model runs say on and healthy, off or unhealthy, and never a path,
// an id, a size, a day or a failure. Only doctor in the person's terminal
// shows the full line.
func TestTelemetryModelOutputIsCoarse(t *testing.T) {
	const sentinel = "pathsentinel9c"
	setup := func(t *testing.T) (root, telemetryRoot, id, day string) {
		t.Helper()
		home := filepath.Join(t.TempDir(), sentinel)
		if err := os.MkdirAll(home, 0700); err != nil {
			t.Fatal(err)
		}
		isolatedRouteConfig(t)
		for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
			t.Setenv(name, home)
		}
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		t.Setenv("DUOLOG_TELEMETRY", "")
		clearModelMarkers(t)
		codex := testbin.Install(t, "codex", testbin.Scenario{Stdout: "{}"})
		claude := testbin.Install(t, "claude", testbin.Scenario{})
		// Doctor finds this rex first, never one installed on the machine.
		rex := testbin.Install(t, "rex", testbin.Scenario{Stdout: `{"blocks":[]}`})
		t.Setenv("PATH", strings.Join([]string{filepath.Dir(codex), filepath.Dir(claude), filepath.Dir(rex)}, string(os.PathListSeparator)))
		root = filepath.Join(t.TempDir(), "project")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		t.Chdir(root)
		if _, err := store.Open(root); err != nil {
			t.Fatal(err)
		}
		var err error
		if telemetryRoot, err = telemetry.ResolveRoot(telemetryLocation()); err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().AddDate(0, 0, -3)
		seedRun(t, seedStore(t, root, at), "claude", "done")
		return root, telemetryRoot, projectID(t, root), at.Format(time.DateOnly)
	}
	sessionStart := func(t *testing.T, root string) (string, string) {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"session_id": "lead-coarse", "cwd": root, "hook_event_name": "SessionStart"})
		cmd := newRootCommand(testDeps(t))
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetIn(bytes.NewReader(input))
		cmd.SetArgs([]string{"hook", "SessionStart", "--agent", "claude"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String(), errOut.String()
	}
	doctor := func(t *testing.T) string {
		t.Helper()
		out, err := executeBusCommand(t, "doctor")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// The lines about telemetry; a temporary folder named for this test
	// does not make one.
	telemetryLines := func(text string) []string {
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "telemetry") || strings.Contains(line, ": telemetry") || strings.HasPrefix(line, "Telemetry") {
				lines = append(lines, line)
			}
		}
		return lines
	}
	// raw is everything printed, the hook's JSON included; text is what a
	// model reads as lines.
	coarseOnly := func(t *testing.T, where, raw, text string, leaks []string, want string) {
		t.Helper()
		for _, leak := range leaks {
			if strings.Contains(raw, leak) || strings.Contains(text, leak) {
				t.Fatalf("%s leaks %q:\n%s\n%s", where, leak, raw, text)
			}
		}
		for _, line := range telemetryLines(text) {
			if !strings.HasSuffix(line, want) && line != noticeText {
				t.Fatalf("%s: %q is not %q:\n%s", where, line, want, text)
			}
		}
	}
	decoded := func(t *testing.T, stdout string) string {
		t.Helper()
		if stdout == "" {
			return ""
		}
		var h struct {
			Specific struct {
				Context string `json:"additionalContext"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(stdout), &h); err != nil {
			t.Fatalf("%v: %s", err, stdout)
		}
		return h.Specific.Context
	}

	t.Run("healthy", func(t *testing.T) {
		root, telemetryRoot, id, day := setup(t)
		leaks := []string{sentinel, telemetryRoot, filepath.ToSlash(telemetryRoot), id, day, "segments", "MB", "KB", " B ", "pruned"}
		stdout, stderr := sessionStart(t, root)
		coarseOnly(t, "session start", stdout, stderr+decoded(t, stdout), leaks, "telemetry: on, healthy")
		for _, marker := range []string{"CLAUDECODE", "CODEX_SANDBOX"} {
			t.Setenv(marker, "1")
			asTerminal(t, true)
			out := doctor(t)
			coarseOnly(t, "model doctor "+marker, out, out, leaks, "telemetry: on, healthy")
			if !slices.Contains(telemetryLines(out), "info: telemetry: on, healthy") {
				t.Fatalf("model doctor has no coarse line:\n%s", out)
			}
			t.Setenv(marker, "")
		}
		// Output a model reads through a pipe is coarse too.
		asTerminal(t, false)
		out := doctor(t)
		coarseOnly(t, "piped doctor", out, out, leaks, "telemetry: on, healthy")

		asTerminal(t, true)
		full := strings.Join(telemetryLines(doctor(t)), "\n")
		if !strings.Contains(full, "oldest "+day) || !strings.Contains(full, "1 segments") || !strings.Contains(full, "since process start") {
			t.Fatalf("terminal doctor is not full:\n%s", full)
		}
	})

	t.Run("unhealthy", func(t *testing.T) {
		root, telemetryRoot, id, day := setup(t)
		// A root inside a git work tree is refused.
		if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".git"), 0700); err != nil {
			t.Fatal(err)
		}
		leaks := []string{sentinel, telemetryRoot, id, day, "git", "refused"}
		stdout, stderr := sessionStart(t, root)
		text := stderr + decoded(t, stdout)
		coarseOnly(t, "session start", stdout, text, leaks, "telemetry: unhealthy")
		if strings.Contains(text, noticeText) {
			t.Fatal("an unsafe root claims collection")
		}
		t.Setenv("CLAUDECODE", "1")
		asTerminal(t, false)
		out := doctor(t)
		coarseOnly(t, "model doctor", out, strings.Join(telemetryLines(out), "\n"), []string{sentinel, telemetryRoot, id, day}, "telemetry: unhealthy")
		for _, line := range telemetryLines(out) {
			if strings.Contains(line, "git") || strings.Contains(line, "refused") {
				t.Fatalf("model doctor names the refusal: %s", line)
			}
		}
		if !slices.Contains(telemetryLines(out), "warning: telemetry: unhealthy") {
			t.Fatalf("model doctor has no coarse line:\n%s", out)
		}
		t.Setenv("CLAUDECODE", "")
		asTerminal(t, true)
		if full := strings.Join(telemetryLines(doctor(t)), "\n"); !strings.Contains(full, "git") {
			t.Fatalf("terminal doctor hides the refusal:\n%s", full)
		}
	})

	t.Run("off", func(t *testing.T) {
		root, telemetryRoot, id, day := setup(t)
		t.Setenv("DUOLOG_TELEMETRY", "off")
		leaks := []string{sentinel, telemetryRoot, id, day}
		stdout, stderr := sessionStart(t, root)
		coarseOnly(t, "session start", stdout, stderr+decoded(t, stdout), leaks, "telemetry: off")
		t.Setenv("CLAUDECODE", "1")
		out := doctor(t)
		coarseOnly(t, "model doctor", out, out, leaks, "telemetry: off")
		if !slices.Contains(telemetryLines(out), "info: telemetry: off") {
			t.Fatalf("model doctor has no coarse line:\n%s", out)
		}
	})
}

func TestTelemetryDoctorWarnings(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	healthy := telemetry.Status{
		Enabled: true, ProjectEnabled: true, Root: "/home/user/.local/state/duolog/telemetry", ProjectID: "0123456789abcdef",
		OldestDay: "2026-10-06", Bytes: 1153434, Segments: 41, Partitions: 4, LastPrune: now.Add(-2 * time.Hour),
		RetentionDays: 30, PruneEveryHours: 6, MaxMegabytes: 16,
		Protection: telemetry.Protection{BackupVerified: true, IndexVerified: true, Refusal: "none"},
	}
	// goos is the platform doctor describes; each case sets its own.
	var goos string
	setGOOS := func(t *testing.T, name string) {
		t.Helper()
		goos = name
	}
	lines := func(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }
	warnings := func(s string) []string {
		var w []string
		for _, line := range lines(s) {
			if strings.HasPrefix(line, "warning: ") {
				w = append(w, line)
			}
		}
		return w
	}

	t.Run("healthy line", func(t *testing.T) {
		setGOOS(t, "darwin")
		got := telemetryDoctor(goos, healthy, false, true, now)
		first := lines(got)[0]
		want := "info: telemetry: on, local only, isolation gate + sandbox; 1.1 MB in 41 segments, 4 partitions, oldest 2026-10-06, pruned 2h ago; 0 dropped, 0 failed since process start"
		if !strings.HasPrefix(first, strings.TrimSuffix(want, "0 dropped, 0 failed since process start")) || !strings.HasSuffix(first, "since process start") {
			t.Fatalf("got  %q\nwant %q", first, want)
		}
		if w := warnings(got); len(w) != 0 {
			t.Fatalf("healthy store warns: %v", w)
		}
	})

	t.Run("isolation", func(t *testing.T) {
		for _, c := range []struct {
			goos    string
			sandbox bool
			want    string
		}{
			{"darwin", true, "isolation gate + sandbox"}, {"linux", true, "isolation gate + sandbox"},
			{"darwin", false, "isolation gate only"}, {"linux", false, "isolation gate only"},
			{"windows", true, "isolation gate only"}, {"windows", false, "isolation gate only"},
		} {
			setGOOS(t, c.goos)
			if got := telemetryDoctor(goos, healthy, false, c.sandbox, now); !strings.Contains(lines(got)[0], c.want) {
				t.Errorf("%s sandbox=%t: %s", c.goos, c.sandbox, got)
			}
		}
	})

	t.Run("protection warnings", func(t *testing.T) {
		setGOOS(t, "linux")
		for _, c := range []struct {
			name string
			edit func(*telemetry.Status)
			want string
		}{
			{"repaired mode", func(s *telemetry.Status) { s.Protection.ModeFixed = true }, "mode was too open"},
			{"foreign owner", func(s *telemetry.Status) { s.Protection.Refusal = "owner" }, "another user"},
			{"symlink", func(s *telemetry.Status) { s.Protection.Refusal = "symlink" }, "symlink"},
			{"cloud", func(s *telemetry.Status) { s.Protection.Refusal = "sync" }, "cloud-synced"},
			{"nfs", func(s *telemetry.Status) { s.Protection.Refusal = "nfs" }, "NFS"},
			{"git", func(s *telemetry.Status) { s.Protection.Refusal = "git" }, "git work tree"},
			{"no prune for two days", func(s *telemetry.Status) { s.LastPrune = now.Add(-49 * time.Hour) }, "not pruned for 2 days"},
			{"never pruned", func(s *telemetry.Status) { s.LastPrune = time.Time{} }, "not pruned for 2 days"},
		} {
			st := healthy
			c.edit(&st)
			w := warnings(telemetryDoctor(goos, st, false, false, now))
			if len(w) == 0 || !strings.Contains(strings.Join(w, "\n"), c.want) {
				t.Errorf("%s: %v", c.name, w)
			}
			if st.Protection.Refusal != "none" && !strings.Contains(strings.Join(w, "\n"), "collection stays off") {
				t.Errorf("%s does not say collection stopped: %v", c.name, w)
			}
		}
		// A store with no segments has nothing to prune.
		st := healthy
		st.Segments, st.LastPrune = 0, time.Time{}
		if w := warnings(telemetryDoctor(goos, st, false, false, now)); len(w) != 0 {
			t.Errorf("empty store warns: %v", w)
		}
	})

	t.Run("backup checks", func(t *testing.T) {
		for _, c := range []struct {
			goos             string
			backup, index    bool
			want, warn, none string
		}{
			{"darwin", true, true, "Time Machine exclusion verified, Spotlight exclusion verified", "", "unverified"},
			{"darwin", false, true, "Spotlight exclusion verified", "Time Machine exclusion unverified", ""},
			{"darwin", true, false, "Time Machine exclusion verified", "Spotlight exclusion unverified", ""},
			{"linux", true, false, "CACHEDIR.TAG verified for tools that honour it, not Deja Dup", "", "unverified"},
			{"linux", false, false, "", "CACHEDIR.TAG unverified", ""},
			{"windows", false, false, "Windows Search and OneDrive exclusions of AppData unverified", "", ""},
		} {
			setGOOS(t, c.goos)
			st := healthy
			st.Protection.BackupVerified, st.Protection.IndexVerified = c.backup, c.index
			got := telemetryDoctor(goos, st, false, false, now)
			backup := ""
			for _, line := range lines(got) {
				if strings.Contains(line, "telemetry backup") {
					backup += line + "\n"
				}
			}
			if c.want != "" && !strings.Contains(backup, c.want) {
				t.Errorf("%s %t/%t: %q lacks %q", c.goos, c.backup, c.index, backup, c.want)
			}
			if c.warn != "" && !strings.Contains(strings.Join(warnings(got), "\n"), c.warn) {
				t.Errorf("%s %t/%t: no warning %q in %q", c.goos, c.backup, c.index, c.warn, got)
			}
			if c.none != "" && strings.Contains(backup, c.none) {
				t.Errorf("%s %t/%t: %q", c.goos, c.backup, c.index, backup)
			}
		}
	})

	t.Run("model context is coarse", func(t *testing.T) {
		setGOOS(t, "darwin")
		st := healthy
		st.Protection.ModeFixed = true
		st.Health = telemetry.Health{Dropped: 3, Failures: 2}
		if got := telemetryDoctor(goos, st, true, true, now); got != "warning: telemetry: unhealthy\n" {
			t.Fatalf("%q", got)
		}
		if got := telemetryDoctor(goos, healthy, true, true, now); got != "info: telemetry: on, healthy\n" {
			t.Fatalf("%q", got)
		}
		off := healthy
		off.Enabled, off.ProjectEnabled = false, false
		if got := telemetryDoctor(goos, off, true, false, now); got != "info: telemetry: off\n" {
			t.Fatalf("%q", got)
		}
		refused := healthy
		refused.Protection.Refusal = "sync"
		if got := telemetryDoctor(goos, refused, true, false, now); got != "warning: telemetry: unhealthy\n" {
			t.Fatalf("%q", got)
		}
	})

	t.Run("claude sandbox", func(t *testing.T) {
		isolatedRouteConfig(t)
		root := t.TempDir()
		if claudeSandboxActive(root) {
			t.Fatal("no settings, no sandbox")
		}
		user := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
		project := filepath.Join(root, ".claude", "settings.local.json")
		for _, p := range []string{user, project} {
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(user, []byte(`{"sandbox":{"enabled":true}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if !claudeSandboxActive(root) {
			t.Fatal("the person's sandbox is on")
		}
		if err := os.WriteFile(project, []byte(`{"sandbox":{"enabled":false}}`), 0600); err != nil {
			t.Fatal(err)
		}
		if claudeSandboxActive(root) {
			t.Fatal("a local setting turned it off")
		}
	})
}
