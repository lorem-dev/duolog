package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/doctor/doctortest"
	"github.com/lorem-dev/duolog/internal/app/launch/launchtest"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const catalog = `{"models":[{"slug":"gpt-6.1-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low","description":"d"},{"effort":"medium","description":"d"},{"effort":"high","description":"d"}]},{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":[{"effort":"low","description":"d"}]}]}`

// agents are the fake probes of one test, as setup leaves them in testDeps.
type agents struct{ claude, codex *doctortest.Fake }

// setup returns a config, options and fake probes that find both agents
// installed: claude 2.1.312, codex 0.165.0 with the catalog above, and a
// probe that answers ok and reports no usage. The fakes are in testDeps until
// the test ends.
func setup(t *testing.T) (*config.Config, Options, *agents) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	a := &agents{claude: doctortest.NewFake(), codex: doctortest.NewFake()}
	a.claude.Version, a.claude.ModelName, a.claude.Usage = "2.1.312 (Claude Code)\n", "haiku", Usage{}
	a.codex.Version, a.codex.Catalog, a.codex.ModelName, a.codex.Usage = "codex-cli 0.165.0\n", catalog, "gpt-6-luna", Usage{}
	saved := testDeps
	testDeps.Probes = map[string]Probe{"claude": a.claude, "codex": a.codex}
	testDeps.Available = func(agent string) bool { return !a.byName(agent).Missing }
	t.Cleanup(func() { testDeps = saved })
	// Keep all subprocesses isolated from real agents and Rex: a PATH with
	// nothing but the fake rex.
	t.Setenv("PATH", filepath.Dir(fakeRex(t)))
	return cfg, Options{Root: root, Registry: reg}, a
}

func (a *agents) byName(agent string) *doctortest.Fake {
	if agent == "codex" {
		return a.codex
	}
	return a.claude
}

// fakeRex puts a fake rex first on PATH, so no doctor test can find the
// machine's own. Call it after the test sets its PATH.
func fakeRex(t *testing.T) string {
	t.Helper()
	return testbin.Install(t, "rex", testbin.Scenario{Stdout: `{"blocks":[]}`})
}

// Every doctor test finds the fake rex first on PATH, and doctor never runs
// it.
func TestDoctorTestsFindFakeRex(t *testing.T) {
	cfg, opts, _ := setup(t)
	bin, err := exec.LookPath("rex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin + ".json"); err != nil {
		t.Fatalf("rex in PATH is %s, not the fake: %v", bin, err)
	}
	findings(t, cfg, opts)
	if _, err := os.Stat(bin + ".calls.ndjson"); !os.IsNotExist(err) {
		t.Fatalf("doctor ran rex: %v", err)
	}
}
func findings(t *testing.T, cfg *config.Config, opts Options) string {
	t.Helper()
	var text []string
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		text = append(text, f.Message)
	}
	return strings.Join(text, "\n")
}
func contains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing %q", got, want)
		}
	}
}

// matchingFinding keeps every required relationship in one diagnostic.
func matchingFinding(got []Finding, wants ...string) bool {
	for _, f := range got {
		matches := true
		for _, want := range wants {
			matches = matches && strings.Contains(f.Message, want)
		}
		if matches {
			return true
		}
	}
	return false
}
func requireFinding(t *testing.T, got []Finding, wants ...string) {
	t.Helper()
	if !matchingFinding(got, wants...) {
		t.Fatalf("no single finding contains %q: %+v", wants, got)
	}
}
func TestMatchingFindingRejectsUnrelatedDiagnostics(t *testing.T) {
	for _, wants := range [][]string{
		{"terra", "tests", "review_intermediate", "docs"},
		{"implement", "gpt-6-sol", "gpt-6.1-sol"},
		{"implement", "gpt-6.1-sol", "ultra"},
	} {
		var unrelated []Finding
		for _, want := range wants {
			unrelated = append(unrelated, Finding{Message: want})
		}
		if matchingFinding(unrelated, wants...) {
			t.Fatalf("accepted unrelated diagnostics: %+v", unrelated)
		}
		if !matchingFinding([]Finding{{Message: strings.Join(wants, " ")}}, wants...) {
			t.Fatal("rejected associated diagnostic")
		}
	}
}
func TestDoctorFlagsAliasWithNoModel(t *testing.T) {
	cfg, opts, _ := setup(t)
	requireFinding(t, Run(context.Background(), testDeps, cfg, opts), "Alias terra has no visible model", "docs", "tests")
}
func TestDoctorFlagsOldPin(t *testing.T) {
	cfg, opts, _ := setup(t)
	r := cfg.Roles["implement"]
	r.Use.Model = "gpt-6-sol"
	cfg.Roles["implement"] = r
	requireFinding(t, Run(context.Background(), testDeps, cfg, opts), "Role implement pins gpt-6-sol", "newer model gpt-6.1-sol")
}
func TestDoctorWarnsOnNewerAgentVersion(t *testing.T) {
	cfg, opts, a := setup(t)
	a.codex.Version = "codex-cli 0.166.0\n"
	contains(t, findings(t, cfg, opts), "0.166.0", "0.165.0", "newer")
}
func TestDoctorListsPanesOfEndedLead(t *testing.T) {
	cfg, opts, _ := setup(t)
	opts.Panes = []Pane{{ID: "pane-a", Lead: "ended"}, {ID: "pane-b", Lead: "ended"}}
	got := findings(t, cfg, opts)
	contains(t, got, "pane-a", "pane-b", "duolog peer close pane-a", "duolog peer close pane-b")
}
func TestDoctorFlagsMissingBinary(t *testing.T) {
	cfg, opts, a := setup(t)
	a.claude.Missing, a.codex.Missing = true, true
	notice := "Rex unavailable"
	if runtime.GOOS == "windows" {
		notice = "Live sessions are not available on Windows; use duolog delegate."
	}
	contains(t, findings(t, cfg, opts), "codex binary missing", "claude binary missing", notice)
}
func TestDoctorFlagsUnsupportedEffort(t *testing.T) {
	cfg, opts, _ := setup(t)
	r := cfg.Roles["implement"]
	r.Use.Effort = "ultra"
	cfg.Roles["implement"] = r
	requireFinding(t, Run(context.Background(), testDeps, cfg, opts), "Role implement model gpt-6.1-sol", "does not support effort ultra")
}
func TestProbeClearsUnavailable(t *testing.T) {
	cfg, opts, a := setup(t)
	if err := opts.Registry.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	opts.Probe = true
	got := findings(t, cfg, opts)
	contains(t, got, "codex probe succeeded", "claude probe succeeded")
	unavailable, err := opts.Registry.Unavailable()
	if err != nil {
		t.Fatal(err)
	}
	if unavailable["codex"] {
		t.Fatal("successful probe did not clear availability")
	}
	if len(a.codex.Calls) != 1 {
		t.Fatalf("codex probe calls = %d, want 1", len(a.codex.Calls))
	}
	call := a.codex.Calls[0]
	if call.Dir == opts.Root {
		t.Fatal("probe used project folder")
	}
	if !call.DirExisted {
		t.Fatal("probe folder did not exist during the call")
	}
	if _, err := os.Stat(call.Dir); !os.IsNotExist(err) {
		t.Fatal("probe folder not cleaned")
	}
	// The call is the luna model Model picked from the catalog entries.
	if call.Model != "gpt-6-luna" {
		t.Fatalf("probe model = %q", call.Model)
	}
}
func TestProbeOnlyWithFlag(t *testing.T) {
	cfg, opts, a := setup(t)
	findings(t, cfg, opts)
	if len(a.codex.Calls) != 0 || len(a.claude.Calls) != 0 {
		t.Fatalf("model calls without the flag: %+v %+v", a.codex.Calls, a.claude.Calls)
	}
}
func TestProbeDoesNotClearOnEmptyAnswer(t *testing.T) {
	cfg, opts, a := setup(t)
	a.codex.AnswerErr = errors.New("missing expected answer")
	if err := opts.Registry.MarkUnavailable("codex", time.Time{}); err != nil {
		t.Fatal(err)
	}
	opts.Probe = true
	contains(t, findings(t, cfg, opts), "codex probe failed")
	u, err := opts.Registry.Unavailable()
	if err != nil || !u["codex"] {
		t.Fatalf("%v %v", u, err)
	}
}
func TestDoctorReportsTrustAndRexWithoutCallingRex(t *testing.T) {
	cfg, opts, _ := setup(t)
	rexMessage := "Rex unavailable"
	if runtime.GOOS == "windows" {
		rexMessage = "Live sessions are not available on Windows; use duolog delegate."
	}
	contains(t, findings(t, cfg, opts), "hooks approved=false", "folder trusted=false", "changed after review cannot be detected", "Codex itself will skip", rexMessage)
}

// A Perl tool named rex in PATH hides the Rex CLI; doctor names both.
func TestDoctorNamesShadowingRex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no live sessions on Windows")
	}
	cfg, opts, _ := setup(t)
	opts.RexShadow, opts.RexBin = "/usr/local/bin/rex", "/Applications/Rex.app/rex"
	contains(t, findings(t, cfg, opts), "rex in PATH (/usr/local/bin/rex) is a script, not the Rex CLI", "duolog uses /Applications/Rex.app/rex")
	opts.RexBin = ""
	contains(t, findings(t, cfg, opts), "set REX_BIN_DIR")
}

// The retained catalog fixture has the shape of real `codex debug models`:
// each level is an object with an "effort" key. The default roles must pass.
func TestDoctorReadsSavedCatalogFixture(t *testing.T) {
	cfg, opts, a := setup(t)
	fixture, err := os.ReadFile("../../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	a.codex.Catalog = string(fixture)
	got := findings(t, cfg, opts)
	if strings.Contains(got, "Codex catalog:") || strings.Contains(got, "does not support effort") || strings.Contains(got, "has no visible model") {
		t.Fatal(got)
	}
}

func TestDoctorFlagsMissingDuolog(t *testing.T) {
	cfg, opts, _ := setup(t)
	saved := testDeps.Agents
	t.Cleanup(func() { testDeps.Agents = saved })
	testDeps.Agents = map[string]Agent{}
	for name, c := range launchtest.CapsIn(os.Getenv("HOME"), nil, "") {
		a := saved[name]
		a.Caps = c
		testDeps.Agents[name] = a
	}
	got := findings(t, cfg, opts)
	contains(t, got, "claude has no duolog hooks", "codex has no duolog hooks")
	if err := os.MkdirAll(filepath.Join(opts.Root, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.Root, ".codex", "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"duolog gate"}]}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	got = findings(t, cfg, opts)
	if strings.Contains(got, "codex has no duolog hooks") || !strings.Contains(got, "claude has no duolog hooks") {
		t.Fatal(got)
	}
}

func TestInstallOfferIsInfoOnly(t *testing.T) {
	cfg, opts, _ := setup(t)
	for _, present := range []bool{false, true} {
		if present {
			for _, path := range testDeps.instructionPaths(opts.Root) {
				if err := os.WriteFile(path, []byte(coreagent.InstallOffer+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		count := 0
		for _, finding := range Run(context.Background(), testDeps, cfg, opts) {
			if strings.Contains(finding.Message, "duolog install offer") {
				count++
				if finding.Level != "info" || !strings.Contains(finding.Message, fmt.Sprintf("present=%t", present)) {
					t.Fatalf("%+v", finding)
				}
			}
		}
		if count != 2 {
			t.Fatalf("got %d offer findings", count)
		}
	}
}

func TestInstructionBlockIsInfoOnly(t *testing.T) {
	cfg, opts, _ := setup(t)
	for _, tc := range []struct{ state, text string }{
		{"missing", "# Rules\n"},
		{"outdated", coreagent.InstructionBegin + "\nold\n" + coreagent.InstructionEnd + "\n"},
		{"current", "# Rules\n"},
	} {
		for _, path := range testDeps.instructionPaths(opts.Root) {
			text := tc.text
			if tc.state == "current" {
				text += coreagent.BlockFor(filepath.Base(path) == "CLAUDE.md")
			}
			if err := os.WriteFile(path, []byte(text), 0644); err != nil {
				t.Fatal(err)
			}
		}
		count := 0
		for _, finding := range Run(context.Background(), testDeps, cfg, opts) {
			if strings.Contains(finding.Message, "duolog instructions in") {
				count++
				if finding.Level != "info" || !strings.HasSuffix(strings.SplitN(finding.Message, "; ", 2)[0], ": "+tc.state) {
					t.Fatalf("%s: %+v", tc.state, finding)
				}
			}
		}
		if count != 2 {
			t.Fatalf("%s: got %d instruction findings", tc.state, count)
		}
	}
}

func TestInstructionBlockWithUnpairedMarkerIsWarning(t *testing.T) {
	cfg, opts, _ := setup(t)
	for _, path := range testDeps.instructionPaths(opts.Root) {
		if err := os.WriteFile(path, []byte(coreagent.InstructionBegin+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, finding := range Run(context.Background(), testDeps, cfg, opts) {
		if strings.Contains(finding.Message, "duolog instructions in") {
			count++
			if finding.Level != "warning" || !strings.Contains(finding.Message, "do not pair up") {
				t.Fatalf("%+v", finding)
			}
		}
	}
	if count != 2 {
		t.Fatalf("got %d instruction findings", count)
	}
}

func TestDoctorNeverMentionsCalibration(t *testing.T) {
	cfg, opts, _ := setup(t)
	run := filepath.Join(opts.Root, ".duolog", "calibration", "runs", "r1")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "run.json"), []byte(`{"id":"r1","status":"stopped"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The test's temporary folders carry its name, so they leave the message.
	temp := filepath.Dir(opts.Root)
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if strings.Contains(strings.ToLower(strings.ReplaceAll(f.Message, temp, "")), "calibrat") {
			t.Fatalf("%+v", f)
		}
	}
}

func TestDoctorReportsClaudeFolderTrust(t *testing.T) {
	cfg, opts, _ := setup(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".claude.json")
	secret := "sk-should-never-be-printed"
	for name, c := range map[string]struct{ body, want string }{
		"unknown":     {"", "Claude folder trust: unknown"},
		"unreadable":  {"{nope", "Claude folder trust: unknown"},
		"not trusted": {fmt.Sprintf(`{"token":%q,"projects":{}}`, secret), "accept the trust dialog"},
		"trusted":     {fmt.Sprintf(`{"token":%q,"projects":{%q:{"hasTrustDialogAccepted":true}}}`, secret, opts.Root), "Claude folder trust: trusted"},
	} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(state)
			if c.body != "" {
				if err := os.WriteFile(state, []byte(c.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var text []string
			for _, f := range Run(context.Background(), testDeps, cfg, opts) {
				text = append(text, f.Level+": "+f.Message)
			}
			got := strings.Join(text, "\n")
			contains(t, got, c.want)
			if strings.Contains(got, secret) {
				t.Fatal("doctor printed other contents of the state file")
			}
			if strings.Contains(c.want, "unknown") && (strings.Contains(got, "warning: Claude folder trust") || strings.Contains(got, "error: Claude folder trust")) {
				t.Fatal("unknown must be info")
			}
		})
	}
}

func TestDoctorWarnsOfUndecidedProjectMCPServers(t *testing.T) {
	cfg, opts, _ := setup(t)
	if err := os.WriteFile(filepath.Join(opts.Root, ".mcp.json"), []byte(`{"mcpServers":{"alpha":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var text []string
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		text = append(text, f.Level+": "+f.Message)
	}
	got := strings.Join(text, "\n")
	contains(t, got, "warning: Claude has not enabled or disabled the project MCP servers alpha")
	contains(t, got, "a Claude peer stops at the new MCP servers dialog")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state := fmt.Sprintf(`{"projects":{%q:{"enabledMcpjsonServers":["alpha"]}}}`, opts.Root)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range Run(context.Background(), testDeps, cfg, opts) {
		if strings.Contains(f.Message, "MCP servers") {
			t.Fatalf("decided server still warned: %+v", f)
		}
	}
}
