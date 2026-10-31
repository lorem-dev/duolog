package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/testbin"
)

const catalog = `{"models":[{"slug":"gpt-6.1-sol","visibility":"list","supported_reasoning_levels":[{"effort":"low","description":"d"},{"effort":"medium","description":"d"},{"effort":"high","description":"d"}]},{"slug":"gpt-6-luna","visibility":"list","supported_reasoning_levels":[{"effort":"low","description":"d"}]}]}`

func setup(t *testing.T) (*config.Config, Options, string) {
	t.Helper()
	home := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(key, home)
	}
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	codex := testbin.Install(t, "codex", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version":    {Stdout: "codex-cli 0.165.0\n"},
		"debug models": {Stdout: catalog},
		"exec":         {Writes: map[string]string{"answer.json": "ok\n"}},
	}})
	claude := testbin.Install(t, "claude", testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version": {Stdout: "2.1.312 (Claude Code)\n"},
		"-p":        {Stdout: `{"is_error":false,"result":"ok"}`},
	}})
	// Keep all subprocesses isolated from real agents and Rex.
	t.Setenv("PATH", filepath.Dir(claude)+string(os.PathListSeparator)+filepath.Dir(codex))
	return cfg, Options{Root: root, Registry: reg}, codex
}
func findings(t *testing.T, cfg *config.Config, opts Options) string {
	t.Helper()
	var text []string
	for _, f := range Run(context.Background(), cfg, opts) {
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
	requireFinding(t, Run(context.Background(), cfg, opts), "Alias terra has no visible model", "tests", "review_intermediate", "docs")
}
func TestDoctorFlagsOldPin(t *testing.T) {
	cfg, opts, _ := setup(t)
	r := cfg.Roles["implement"]
	r.Use.Model = "gpt-6-sol"
	cfg.Roles["implement"] = r
	requireFinding(t, Run(context.Background(), cfg, opts), "Role implement pins gpt-6-sol", "newer model gpt-6.1-sol")
}
func TestDoctorWarnsOnNewerAgentVersion(t *testing.T) {
	cfg, opts, codex := setup(t)
	data, _ := json.Marshal(testbin.Scenario{Stdout: "codex-cli 0.166.0\n"})
	if err := os.WriteFile(codex+".json", data, 0600); err != nil {
		t.Fatal(err)
	}
	contains(t, findings(t, cfg, opts), "0.166.0", "0.165.0", "newer")
}
func TestDoctorListsPanesOfEndedLead(t *testing.T) {
	cfg, opts, _ := setup(t)
	opts.Panes = []Pane{{ID: "pane-a", Lead: "ended"}, {ID: "pane-b", Lead: "ended"}}
	got := findings(t, cfg, opts)
	contains(t, got, "pane-a", "pane-b", "duolog peer close pane-a", "duolog peer close pane-b")
}
func TestDoctorFlagsMissingBinary(t *testing.T) {
	cfg, opts, _ := setup(t)
	t.Setenv("PATH", t.TempDir())
	contains(t, findings(t, cfg, opts), "codex", "claude", "missing", "Rex unavailable")
}
func TestDoctorFlagsUnsupportedEffort(t *testing.T) {
	cfg, opts, _ := setup(t)
	r := cfg.Roles["implement"]
	r.Use.Effort = "ultra"
	cfg.Roles["implement"] = r
	requireFinding(t, Run(context.Background(), cfg, opts), "Role implement model gpt-6.1-sol", "does not support effort ultra")
}
func TestProbeClearsUnavailable(t *testing.T) {
	cfg, opts, codex := setup(t)
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
	cwd, err := os.ReadFile(codex + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if string(cwd) == opts.Root {
		t.Fatal("probe used project folder")
	}
	if _, err := os.Stat(string(cwd)); !os.IsNotExist(err) {
		t.Fatal("probe folder not cleaned")
	}
	args, err := os.ReadFile(codex + ".args.json")
	if err != nil {
		t.Fatal(err)
	}
	contains(t, string(args), "exec", "--skip-git-repo-check", "-m", "gpt-6-luna", "read-only")
	stdin, err := os.ReadFile(codex + ".stdin")
	if err != nil || len(stdin) != 0 {
		t.Fatalf("stdin=%q err=%v", stdin, err)
	}
}
func TestProbeOnlyWithFlag(t *testing.T) {
	cfg, opts, codex := setup(t)
	findings(t, cfg, opts)
	data, err := os.ReadFile(codex + ".calls.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"exec\"") {
		t.Fatal(string(data))
	}
}
func TestProbeDoesNotClearOnEmptyAnswer(t *testing.T) {
	cfg, opts, codex := setup(t)
	data, _ := json.Marshal(testbin.Scenario{Responses: map[string]testbin.Scenario{"--version": {Stdout: "codex-cli 0.165.0"}, "debug models": {Stdout: catalog}, "exec": {}}})
	if err := os.WriteFile(codex+".json", data, 0600); err != nil {
		t.Fatal(err)
	}
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

// The retained catalog fixture has the shape of real `codex debug models`:
// each level is an object with an "effort" key. The default roles must pass.
func TestDoctorReadsSavedCatalogFixture(t *testing.T) {
	cfg, opts, codex := setup(t)
	fixture, err := os.ReadFile("../../scripts/bench/fixtures/models/codex-debug-models.trimmed.json")
	if err != nil {
		t.Fatal(err)
	}
	scenario := testbin.Scenario{Responses: map[string]testbin.Scenario{
		"--version":    {Stdout: "codex-cli 0.165.0"},
		"debug models": {Stdout: string(fixture)},
	}}
	data, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex+".json", data, 0600); err != nil {
		t.Fatal(err)
	}
	got := findings(t, cfg, opts)
	if strings.Contains(got, "Codex catalog:") || strings.Contains(got, "does not support effort") || strings.Contains(got, "has no visible model") {
		t.Fatal(got)
	}
}

func TestDoctorFlagsMissingDuolog(t *testing.T) {
	cfg, opts, _ := setup(t)
	old := capability.Home
	capability.Home = os.Getenv("HOME")
	t.Cleanup(func() { capability.Home = old })
	got := findings(t, cfg, opts)
	contains(t, got, "claude has no duolog hooks", "codex has no duolog hooks")
	if err := os.MkdirAll(filepath.Join(opts.Root, ".codex"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.Root, ".codex", "hooks.json"), []byte(`{"command":"duolog gate"}`), 0600); err != nil {
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
			for _, path := range install.InstructionPaths(opts.Root) {
				if err := os.WriteFile(path, []byte(install.InstallOffer+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
		}
		count := 0
		for _, finding := range Run(context.Background(), cfg, opts) {
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
