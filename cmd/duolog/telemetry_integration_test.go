package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/telemetry"
	"github.com/lorem-dev/duolog/internal/testbin"
)

// e2eSentinel stands for free text: a folder name, a task, a vendor answer,
// an error, a command. None of it may reach a segment, raw or compressed.
const e2eSentinel = "sentinelfreetext4e"

// e2eLead is the one lead of the project; answerGateAsks answers for it.
const e2eLead = "lead-123456"

// e2eGit runs git in dir with a fixed identity and no signing.
func e2eGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s %v", args, out, err)
	}
}

// gitOnlyDir is a folder with git and no agent or rex in it, for a PATH the
// test replaces: duolog finds a project's shared state through git.
func gitOnlyDir(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(git)
	if runtime.GOOS != "windows" {
		dir = t.TempDir()
		if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	for _, name := range []string{"claude", "codex", "rex"} {
		if matches, _ := filepath.Glob(filepath.Join(dir, name+".*")); len(matches) > 0 {
			t.Fatalf("%s sits beside git in %s", name, dir)
		}
	}
	return dir
}

// e2eSessionStart runs a lead's SessionStart hook in cwd and returns all it
// printed.
func e2eSessionStart(t *testing.T, agent, cwd string) string {
	t.Helper()
	chdir(t, cwd)
	input, _ := json.Marshal(map[string]string{"session_id": "lead-e2e-" + agent, "cwd": cwd, "hook_event_name": "SessionStart"})
	cmd := newRootCommand(testDeps(t))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(bytes.NewReader(input))
	cmd.SetArgs([]string{"hook", "SessionStart", "--agent", agent})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

var statusProjectID = regexp.MustCompile(`(?m)^project id: ([0-9a-f]{16})$`)

// e2eProjectID is the project id duolog telemetry status prints in dir.
func e2eProjectID(t *testing.T, dir string) string {
	t.Helper()
	chdir(t, dir)
	out, _, err := telemetryCommand(t, "", "status")
	m := statusProjectID.FindStringSubmatch(out)
	if err != nil || m == nil {
		t.Fatalf("status in %s: %v\n%s", dir, err, out)
	}
	return m[1]
}

// segmentLines is every line of every segment under the telemetry root, a
// compressed one decompressed, by partition.
func segmentLines(t *testing.T, telemetryRoot string) map[string][]string {
	t.Helper()
	lines := map[string][]string{}
	err := filepath.WalkDir(filepath.Join(telemetryRoot, "v1"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(d.Name(), ".ndjson") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".gz") {
			z, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return err
			}
			if data, err = io.ReadAll(z); err != nil {
				return err
			}
		}
		partition := filepath.Base(filepath.Dir(path))
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if line != "" {
				lines[partition] = append(lines[partition], line)
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return lines
}

// chdir moves the process to dir until t ends, as t.Chdir does, but keeps no
// handle on the folder it leaves. t.Chdir holds the old folder open to come
// back to it, so each call leaves one more handle until the test ends. A test
// that changes folders often then holds the checkout open, and Windows refuses
// to rename or remove a folder with an open handle. This one comes back by path.
// Like t.Chdir it sets PWD on POSIX, so os.Getwd keeps the spelling of dir.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("PWD", abs)
	}
	// Cleanups run last first, so a folder the test moved or removed meanwhile
	// is skipped and an earlier one, finally the starting folder, is restored.
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("chdir back to %s: %v", old, err)
		}
	})
}

// under reports whether path is dir or inside it.
func under(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestTelemetryEndToEnd runs local telemetry through duolog's own commands
// and hooks in a temporary home: a project with a linked worktree, one lead,
// fake agents, a fake quota and a fake rex. It follows one store from the
// first notice to purge --all and checks what reached disk at each step.
func TestTelemetryEndToEnd(t *testing.T) {
	realHome := os.Getenv("HOME")
	telemetryRoot := telemetryHome(t)
	clearModelMarkers(t)
	home := os.Getenv("HOME")
	balanceRoot, err := telemetry.BalanceRoot(telemetryLocation())
	if err != nil {
		t.Fatal(err)
	}

	// No real user folder: the home, the user config and both stores are
	// temporary, and the agents' own config folders follow the fake home.
	userConfig, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{home, userConfig, telemetryRoot, balanceRoot, os.Getenv("LOCALAPPDATA")} {
		if !inTempDir(dir) || realHome != "" && !inTempDir(realHome) && under(realHome, dir) {
			t.Fatalf("%s is not a temporary folder", dir)
		}
	}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_STATE_HOME", "REX_BIN_DIR", "REX_SESSION"} {
		if os.Getenv(name) != "" {
			t.Fatalf("%s is set", name)
		}
	}

	// Balance keeps its own state beside telemetry. Its stand-in must come
	// through every prune and purge as it is.
	if err := os.MkdirAll(balanceRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(balanceRoot, "state.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	balanceBefore := treeState(t, balanceRoot)

	// A Git project with a linked worktree; both folder names carry the
	// sentinel.
	// PATH holds git and the fakes only: no real agent or rex can start. The
	// fake is built first, while go is still on PATH.
	testbin.Install(t, "e2e-build", testbin.Scenario{})
	gitDir := gitOnlyDir(t)
	t.Setenv("PATH", gitDir)
	// The read-only Claude worker starts only with its gate command on PATH.
	testbin.Install(t, "duolog", testbin.Scenario{})
	for _, name := range []string{"claude", "codex", "rex"} {
		if found, err := exec.LookPath(name); err == nil {
			t.Fatalf("%s found at %s", name, found)
		}
	}
	base := t.TempDir()
	checkout := filepath.Join(base, "checkout-"+e2eSentinel)
	worktree := filepath.Join(base, "tree-"+e2eSentinel)
	if err := os.MkdirAll(checkout, 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"a.go": "package a\n", ".gitignore": ".duolog/\ntask.md\n"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e2eGit(t, checkout, "init", "-q")
	e2eGit(t, checkout, "add", "a.go", ".gitignore")
	e2eGit(t, checkout, "commit", "-q", "-m", "start")
	e2eGit(t, checkout, "worktree", "add", "-q", "-b", "side", worktree)
	chdir(t, checkout)
	s, err := store.Open(checkout)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	lead := func(m mode.Mode) {
		t.Helper()
		if err := reg.RegisterLead(session.Lead{ID: e2eLead, Target: config.Target{Agent: "codex", Model: "sol", Effort: "high"}, Mode: m, PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	lead(mode.Plan)
	projectConfig := filepath.Join(s.Dir, "config.yaml")
	writeProjectConfig := func(extra string) {
		t.Helper()
		if err := os.WriteFile(projectConfig, []byte("policy:\n  ask_timeout_seconds: 1\n"+extra), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeProjectConfig("")
	taskBody := "# Task: " + e2eSentinel + "\n\n## Goal\nDo " + e2eSentinel + ".\n\n## Read\n- a.go\n\n## Write\nNone.\n\n## Limits\nNo changes.\n\n## Check\nReview.\n"
	for _, dir := range []string{checkout, worktree} {
		if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte(taskBody), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// The notice comes once, at the first collecting session start, from
	// whichever agent or worktree starts later.
	if out := e2eSessionStart(t, "claude", checkout); strings.Count(out, noticeText) != 1 {
		t.Fatalf("first session start:\n%s", out)
	}
	for _, start := range []struct{ agent, cwd string }{{"codex", checkout}, {"claude", worktree}, {"claude", checkout}} {
		if out := e2eSessionStart(t, start.agent, start.cwd); strings.Contains(out, noticeText) {
			t.Fatalf("notice again for %s in %s:\n%s", start.agent, start.cwd, out)
		}
	}

	// Attempts: a successful one in the checkout and a failed one in the
	// worktree, each one run event in the one partition both share.
	installWorker := func(worker testbin.Scenario) {
		t.Helper()
		testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{"auth status": {Stdout: `{"loggedIn":true}`}, "-p": worker}})
	}
	succeeded := testbin.Scenario{
		Stdout: `{"session_id":"` + e2eSentinel + `-session","structured_output":{"done":true,"summary":"` + e2eSentinel + ` answer","files":["a.go"],"needs":[]},"usage":{"input_tokens":100,"cache_read_input_tokens":40,"output_tokens":10}}`,
		Stderr: e2eSentinel + " stderr\n",
	}
	failed := testbin.Scenario{Exit: 1, Stderr: e2eSentinel + " error at /" + e2eSentinel + "/path\n"}
	installWorker(testbin.Scenario{Sequence: []testbin.Scenario{succeeded, failed}})
	delegateIn := func(dir string) (string, error) {
		t.Helper()
		chdir(t, dir)
		return executeBusCommand(t, "delegate", "review_final", "--task", filepath.Join(dir, "task.md"), "--lead", e2eLead, "--thread", "topic")
	}
	if out, err := delegateIn(checkout); err != nil || !strings.Contains(out, "done=true") {
		t.Fatalf("delegation: %q %v", out, err)
	}
	if out, err := delegateIn(worktree); err == nil {
		t.Fatalf("a failed worker succeeded: %q", out)
	}
	chdir(t, checkout)
	attempts := runs(t, checkout)
	if len(attempts) != 2 {
		t.Fatalf("run events: %+v", attempts)
	}
	if fromTree := runs(t, worktree); len(fromTree) != 2 || fromTree[1].Run != attempts[1].Run {
		t.Fatalf("the worktree reads another partition: %+v", fromTree)
	}
	done, failure := attempts[0], attempts[1]
	if done.Outcome != "done" || done.Err != "none" || !done.UsageKnown || done.In != 140 || done.Cached != 40 || done.Out != 10 || done.Placement != "worker" || done.Agent != "claude" {
		t.Fatalf("successful attempt: %+v", done)
	}
	if failure.Outcome != "failed" || failure.Err != "exit" || failure.UsageKnown || failure.Run == done.Run {
		t.Fatalf("failed attempt: %+v", failure)
	}

	// One gate event per ask: an allowed read asks nothing, and each of two
	// commands the lead answers is one event.
	lead(mode.Ask)
	if !t.Run("gate asks", func(t *testing.T) {
		t.Setenv("DUOLOG_DEPTH", "1")
		t.Setenv("DUOLOG_LEAD", e2eLead)
		t.Setenv("DUOLOG_ROLE", "implement")
		answerGateAsks(t, reg, checkout, gate.Allow)
		if out := gateHook(t, "claude", "Read", map[string]string{"file_path": filepath.Join(checkout, "a.go")}, checkout); out != "" {
			t.Fatalf("read: %s", out)
		}
		for _, command := range []string{"custom-command --token=" + e2eSentinel, "other-command " + e2eSentinel} {
			if out := gateHook(t, "codex", "Bash", map[string]string{"command": command}, checkout); out != "" {
				t.Fatalf("%s: %s", command, out)
			}
		}
		events := gateEvents(t, checkout)
		if len(events) != 2 {
			t.Fatalf("gate events: %+v", events)
		}
		for _, g := range events {
			if g.Kind != "exec" || g.RuleClass != "on_unmatched" || g.Answer != "allow" || g.By != "lead" {
				t.Fatalf("gate event %+v", g)
			}
		}
	}) {
		t.FailNow()
	}

	// The peer lifecycle: open, send, wait and close, with the task as one
	// run placed on the peer.
	peerRuns := 0
	if !t.Run("peer lifecycle", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("live peer sessions are unavailable on Windows")
		}
		chdir(t, checkout)
		t.Setenv("REX_SESSION", "sess")
		t.Setenv("REX_BLOCK", "")
		t.Setenv("DUOLOG_LEAD", e2eLead)
		peerRex(t, claudeIdleScreen())
		id, _, _ := strings.Cut(peerLine(t, "peer", "open", "plan"), " ")
		line := "Read task.md and do it. Do not call rex. Reply \"done\"."
		peerRex(t, claudeIdleScreen(), claudeIdleScreen(), claudeHolding(line), claudeAtWork())
		peerLine(t, "peer", "send", id, filepath.Join(checkout, "task.md"))
		peerRex(t, claudeAtWork(), claudeIdleScreen(), claudeIdleScreen())
		if out := peerLine(t, "peer", "wait", id); !strings.Contains(out, "task finished") {
			t.Fatal(out)
		}
		peerLine(t, "peer", "close", id)
		peers, all := peerEvents(t, checkout)
		if got, want := peerOps(peers), []string{"open claude done", "send claude done", "close claude done"}; !slices.Equal(got, want) {
			t.Fatalf("peer events %q, want %q", got, want)
		}
		for _, r := range all {
			if r.Placement == "peer" {
				peerRuns++
			}
		}
		if peerRuns != 1 || len(all) != 3 {
			t.Fatalf("runs: %+v", all)
		}
	}) {
		t.FailNow()
	}

	// Account events: two quota reads of unchanged windows are one snapshot
	// per agent; each route the router resolves is one event, and its answer
	// is the same each time.
	chdir(t, checkout)
	restoreGate := installQuotaFakes(t, `{"loggedIn":true,"subscriptionType":"max","email":"`+e2eSentinel+`@example.com"}`, codexQuota)
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+gitDir)
	// installQuotaFakes set PATH anew; the read-only Claude worker below
	// needs its gate command on it again.
	testbin.Install(t, "duolog", testbin.Scenario{})
	for range 2 {
		if out, err := executeBusCommand(t, "quota"); err != nil {
			t.Fatal(out, err)
		}
	}
	// The delegations below admit through TestMain's gate again.
	restoreGate()
	routeAnswer := func() string {
		t.Helper()
		out, err := executeBusCommand(t, "route", "review_intermediate", "--json", "--lead", e2eLead)
		if err != nil {
			t.Fatal(out, err)
		}
		return out
	}
	route := routeAnswer()
	if again := routeAnswer(); again != route {
		t.Fatalf("route changed: %s\n%s", again, route)
	}
	cfg, err := configfile.Load(checkout)
	if err != nil {
		t.Fatal(err)
	}
	account := openAccountTelemetry(cfg, checkout)
	if account == nil {
		t.Fatal("account telemetry did not open")
	}
	t.Cleanup(func() { account.Close() })
	// Balance has not landed, so nothing in duolog resets an epoch yet: a
	// reset reaches telemetry through the observer seam balance will call.
	observeBalance(account).Reset(record.ResetObservation{Agent: "claude", Reason: "manual"})
	quotas, routes, resets := accountEvents[telemetry.Quota](t, account), accountEvents[telemetry.Route](t, account), accountEvents[telemetry.Reset](t, account)
	if len(quotas) != 2 || quotas[0].Agent == quotas[1].Agent || len(routes) != 2 || len(resets) != 1 {
		t.Fatalf("quota %+v\nroute %+v\nreset %+v", quotas, routes, resets)
	}

	// Run, gate and peer events are the project's; quota, route and reset the
	// account's.
	id := e2eProjectID(t, checkout)
	for partition, lines := range segmentLines(t, telemetryRoot) {
		for _, line := range lines {
			var e struct{ Type string }
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			projectEvent := e.Type == "run" || e.Type == "gate" || e.Type == "peer"
			want := "_account"
			if projectEvent {
				want = id
			}
			if partition != want {
				t.Fatalf("%s event in partition %s", e.Type, partition)
			}
		}
	}

	// The report names families, never a model version.
	out, _, err := telemetryCommand(t, "", "show", "--json", "--project", "all", "--since", "1d")
	if err != nil {
		t.Fatal(err)
	}
	var report telemetry.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("%v:\n%s", err, out)
	}
	total := int64(0)
	for _, d := range report.Daily {
		total += d.Runs
	}
	if total != int64(2+peerRuns) || len(report.Quota) != 2 {
		t.Fatalf("report: %s", out)
	}
	families := []string{"fable", "opus", "sonnet", "haiku", "astra", "sol", "terra", "luna", "all", "other"}
	family := regexp.MustCompile(`"family":"([^"]*)"`)
	for _, lines := range segmentLines(t, telemetryRoot) {
		for _, line := range lines {
			for _, m := range family.FindAllStringSubmatch(line, -1) {
				if !slices.Contains(families, m[1]) {
					t.Fatalf("family %q in %s", m[1], line)
				}
			}
			if strings.Contains(line, "claude-") || strings.Contains(line, "gpt-") || strings.Contains(line, `"model"`) {
				t.Fatalf("a model name in %s", line)
			}
		}
	}

	// A project that turns its own events off still writes account events.
	writeProjectConfig("telemetry:\n  enabled: false\n")
	installWorker(succeeded)
	if out, err := delegateIn(checkout); err != nil || !strings.Contains(out, "done=true") {
		t.Fatalf("delegation with project events off: %q %v", out, err)
	}
	routeAnswer()
	writeProjectConfig("")
	if n := len(runs(t, checkout)); n != 2+peerRuns {
		t.Fatalf("project off wrote %d run events", n-2-peerRuns)
	}
	if n := len(accountEvents[telemetry.Route](t, account)); n != 3 {
		t.Fatalf("project off: %d route events, want 3", n)
	}
	// Nothing below reads through this store; Windows may refuse to purge a
	// folder another handle holds.
	account.Close()

	// Collection turned off, by the user switch or the environment, writes
	// nothing, routes the same, and still prunes at session start.
	for _, off := range []string{"switch", "env"} {
		t.Run("global off by "+off, func(t *testing.T) {
			chdir(t, checkout)
			if off == "switch" {
				if _, _, err := telemetryCommand(t, "", "off"); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, _, err := telemetryCommand(t, "", "on"); err != nil {
						t.Fatal(err)
					}
				}()
			} else {
				t.Setenv("DUOLOG_TELEMETRY", "off")
			}
			before := len(accountLines(t, telemetryRoot))
			expired := plantExpired(t, telemetryRoot, "_account", true)
			if again := routeAnswer(); again != route {
				t.Fatalf("route off: %s\nwant %s", again, route)
			}
			if out := e2eSessionStart(t, "claude", checkout); strings.Contains(out, noticeText) {
				t.Fatalf("notice while off:\n%s", out)
			}
			if exists(expired) {
				t.Fatal("session start while off left an expired segment")
			}
			if after := len(accountLines(t, telemetryRoot)); after != before {
				t.Fatalf("off wrote %d account lines", after-before)
			}
		})
	}

	// A closed day is compressed; readers still see it, and no segment, raw
	// or compressed, nor any other file holds free text or a path. Today's
	// segment is dated a day back so prune compresses bytes duolog wrote.
	chdir(t, checkout)
	today := time.Now().UTC()
	partition := filepath.Join(telemetryRoot, "v1", id)
	closed := filepath.Join(partition, today.AddDate(0, 0, -1).Format(time.DateOnly)+".ndjson")
	if err := os.Rename(filepath.Join(partition, today.Format(time.DateOnly)+".ndjson"), closed); err != nil {
		t.Fatal(err)
	}
	if out, _, err := telemetryCommand(t, "", "prune"); err != nil || !strings.Contains(out, "compressed") {
		t.Fatalf("prune: %q %v", out, err)
	}
	if exists(closed) || !exists(closed+".gz") {
		t.Fatal("the closed day was not compressed")
	}
	if n := len(runs(t, checkout)); n != 2+peerRuns {
		t.Fatalf("after compression: %d run events", n)
	}
	compressed := 0
	for _, lines := range segmentLines(t, telemetryRoot) {
		for _, line := range lines {
			if len(line)+1 > 1024 {
				t.Fatalf("line of %d bytes", len(line)+1)
			}
			if strings.Contains(line, `"type":"run"`) {
				compressed++
			}
		}
	}
	if compressed != 2+peerRuns {
		t.Fatalf("%d run lines on disk", compressed)
	}
	leaks := []string{e2eSentinel, checkout, worktree, filepath.ToSlash(checkout), filepath.ToSlash(worktree), home, "example.com", "task.md", "vendor", "stderr", "lead-e2e", e2eLead}
	for _, lines := range segmentLines(t, telemetryRoot) {
		for _, line := range lines {
			for _, leak := range leaks {
				if strings.Contains(line, leak) {
					t.Fatalf("%q in %s", leak, line)
				}
			}
		}
	}
	if text := telemetryText(t, telemetryRoot); strings.Contains(text, e2eSentinel) || strings.Contains(text, filepath.ToSlash(checkout)) {
		t.Fatal("free text in a telemetry file")
	}

	// Every worktree and every spelling of the checkout is one project; a
	// moved checkout is another.
	if got := e2eProjectID(t, worktree); got != id {
		t.Fatalf("worktree id %s, checkout %s", got, id)
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(base, "link")
		if err := os.Symlink(checkout, link); err != nil {
			t.Fatal(err)
		}
		if got := e2eProjectID(t, link); got != id {
			t.Fatalf("symlinked id %s, checkout %s", got, id)
		}
	}
	chdir(t, base)
	moved := filepath.Join(base, "moved")
	if err := os.Rename(checkout, moved); err != nil {
		t.Fatal(err)
	}
	movedID := e2eProjectID(t, moved)
	if movedID == id {
		t.Fatal("a moved checkout kept its id")
	}

	// Purge here takes this project's history only; purge --all takes every
	// partition and the salt and keeps the notice marker. Balance's state and
	// its routing stay as they were.
	seedRun(t, seedStore(t, moved, time.Now()), "codex", "done")
	chdir(t, moved)
	if _, _, err := telemetryCommand(t, "", "purge", "--yes"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(telemetryRoot, "v1", movedID)) || !exists(partition) || !exists(filepath.Join(telemetryRoot, "v1", "_account")) || !exists(filepath.Join(telemetryRoot, "salt")) {
		t.Fatal("purge here reached past this project")
	}
	if _, _, err := telemetryCommand(t, "", "purge", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	if lines := segmentLines(t, telemetryRoot); len(lines) != 0 || exists(filepath.Join(telemetryRoot, "salt")) {
		t.Fatalf("purge --all left history or the salt: %v", lines)
	}
	if !exists(filepath.Join(telemetryRoot, "notice.v1")) {
		t.Fatal("purge --all removed the notice marker")
	}
	if out := e2eSessionStart(t, "claude", moved); strings.Contains(out, noticeText) {
		t.Fatalf("notice after purge:\n%s", out)
	}
	if after := treeState(t, balanceRoot); !mapsEqual(after, balanceBefore) {
		t.Fatalf("balance state changed:\n%v\n%v", balanceBefore, after)
	}
	if again := routeAnswer(); again != route {
		t.Fatalf("route after purge: %s\nwant %s", again, route)
	}
}
