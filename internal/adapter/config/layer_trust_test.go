package config

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestProjectModelsOnlyRaisePrices is review finding C1: prices gate money,
// so a committed config may raise a price but never lower one, re-point an
// alias at another agent or family, or price a new alias.
func TestProjectModelsOnlyRaisePrices(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "paid_runs: {codex: {allow: true, daily_cap_usd: 5}}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), `models:
  sol: {input: 0.000001, cached: 0.000001, output: 0.000001}
  opus: {input: 9, output: 20}
  terra: {family: luna}
  haiku: {agent: codex}
  cheap: {agent: codex, family: sol, input: 0.000001, cached: 0, output: 0.000001}
roles:
  plan:
    use: {agent: codex, model: cheap}
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	sol := cfg.Models["sol"]
	if sol.Input != 2 || sol.Cached != .1 || sol.Output != 10 {
		t.Errorf("sol = %+v, want the user prices 2, 0.1, 10", sol)
	}
	if opus := cfg.Models["opus"]; opus.Input != 9 || opus.Output != 20 {
		t.Errorf("opus = %+v, want the raised input 9", opus)
	}
	if terra := cfg.Models["terra"]; terra.Family != "terra" {
		t.Errorf("terra family = %q, want terra", terra.Family)
	}
	if haiku := cfg.Models["haiku"]; haiku.Agent != "claude" {
		t.Errorf("haiku agent = %q, want claude", haiku.Agent)
	}
	if cheap := cfg.Models["cheap"]; cheap.Input != 0 || cheap.Cached != 0 || cheap.Output != 0 || cheap.Agent != "codex" {
		t.Errorf("new alias = %+v, want it kept without prices", cheap)
	}
	want := []string{
		"models.cheap.input", "models.cheap.output",
		"models.haiku.agent",
		"models.sol.input", "models.sol.cached", "models.sol.output",
		"models.terra.family",
	}
	got := noticeKeys(cfg.Notices)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("notice keys = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.yaml") {
			t.Errorf("notice %+v does not name the project file", n)
		}
	}

	// The person's own layers may lower prices.
	root = isolatedRoot(t)
	writeConfig(t, userConfigPath(t), "models: {sol: {input: 1}}\n")
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "models: {terra: {output: 4}}\n")
	if cfg, err = Load(root); err != nil {
		t.Fatal(err)
	}
	if cfg.Models["sol"].Input != 1 || cfg.Models["terra"].Output != 4 || len(cfg.Notices) != 0 {
		t.Errorf("own layers: sol %+v terra %+v notices %v", cfg.Models["sol"], cfg.Models["terra"], cfg.Notices)
	}
}

// gitRepo makes root a Git repository; it skips the test without git.
func gitRepo(t *testing.T, root string, args ...[]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	for _, a := range append([][]string{{"init", "-q"}}, args...) {
		cmd := exec.Command("git", append([]string{"-C", root}, a...)...)
		cmd.Env = append(cmd.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
}

// A local layer is the person's own only by what the repository of its folder
// tracks. A caller's GIT_DIR must not make a tracked layer look untracked,
// which would let it turn spending on, or the other way round.
func TestSharedLocalIgnoresGitVariablesOfTheCaller(t *testing.T) {
	track := []string{"add", "-f", localFile}
	repo := func(tracked bool) string {
		root := filepath.Join(t.TempDir(), "repo")
		writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "limits: {}\n")
		if tracked {
			gitRepo(t, root, track)
		} else {
			gitRepo(t, root)
		}
		return root
	}
	for _, tc := range []struct {
		name    string
		tracked bool
		want    string
	}{
		{"a tracked layer stays shared", true, trackedLocal},
		{"an untracked layer stays the person's", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, other := repo(tc.tracked), repo(!tc.tracked)
			t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
			if got := sharedLocal(root); got != tc.want {
				t.Fatalf("sharedLocal = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLocalConfigOutsideGitIsShared is re-review finding N3: outside a Git
// checkout, as in a downloaded archive, nothing proves that a
// .duolog/config.local.yaml is the person's own, so it may only tighten. Its
// opt-ins are ignored with notices that say why and where the person's own
// opt-ins go.
func TestLocalConfigOutsideGitIsShared(t *testing.T) {
	root := archiveRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), `paid_runs:
  codex: {allow: true, daily_cap_usd: 500}
limits:
  max_total_sessions: 50
calibration:
  max_weekly_share: 0.9
`)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.PaidRuns.Agent("codex"); c.Allow || c.DailyCapUSD != 0 {
		t.Errorf("local opt-in outside Git applied: %+v", c)
	}
	if cfg.Limits.MaxTotalSessions != 10 || cfg.Calibration.MaxWeeklyShare != .1 {
		t.Errorf("limits %d, weekly share %v: the local file raised them", cfg.Limits.MaxTotalSessions, cfg.Calibration.MaxWeeklyShare)
	}
	keys := noticeKeys(cfg.Notices)
	for _, want := range []string{"paid_runs.codex.allow", "paid_runs.codex.daily_cap_usd", "limits.max_total_sessions", localFile} {
		if !slices.Contains(keys, want) {
			t.Errorf("no notice for %s in %v", want, keys)
		}
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, "outside a Git checkout") {
			t.Errorf("notice %+v does not say why", n)
		}
		if n.Key == localFile && !strings.Contains(n.Reason, "user config") {
			t.Errorf("notice %+v does not say where the person's own opt-ins go", n)
		}
	}

	// A tightening local file outside Git still applies, without notices.
	root = archiveRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "limits: {max_total_sessions: 4}\n")
	if cfg, err = Load(root); err != nil {
		t.Fatal(err)
	}
	if cfg.Limits.MaxTotalSessions != 4 || len(cfg.Notices) != 0 {
		t.Errorf("tightening local outside Git: limit %d notices %v", cfg.Limits.MaxTotalSessions, cfg.Notices)
	}
}

// TestTrackedLocalConfigIsShared is review finding I2: a committed
// .duolog/config.local.yaml reaches everyone who clones the repository, so it
// gets the shared file's rules: it may not turn spending on, raise a cap or
// a calibration share, lower a price or lift a session limit.
func TestTrackedLocalConfigIsShared(t *testing.T) {
	root := isolatedRoot(t)
	local := filepath.Join(root, ".duolog", "config.local.yaml")
	writeConfig(t, local, `paid_runs:
  codex: {allow: true, daily_cap_usd: 500, calibrate: true}
models:
  sol: {input: 0.000001}
limits:
  max_total_sessions: 50
calibration:
  max_weekly_share: 0.9
`)
	gitRepo(t, root, []string{"add", "-f", ".duolog/config.local.yaml"})
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.PaidRuns.Agent("codex"); c.Allow || c.DailyCapUSD != 0 || c.Calibrate {
		t.Errorf("tracked local opt-in applied: %+v", c)
	}
	if cfg.Models["sol"].Input != 2 {
		t.Errorf("tracked local lowered sol input to %v", cfg.Models["sol"].Input)
	}
	if cfg.Limits.MaxTotalSessions != 10 {
		t.Errorf("max_total_sessions = %d, want 10", cfg.Limits.MaxTotalSessions)
	}
	if cfg.Calibration.MaxWeeklyShare != .1 || len(cfg.Calibration.Ignored) != 1 ||
		!strings.Contains(cfg.Calibration.Ignored[0], ".duolog/config.local.yaml") {
		t.Errorf("calibration = %v ignored %q, want 0.1 and one note naming the local file",
			cfg.Calibration.MaxWeeklyShare, cfg.Calibration.Ignored)
	}
	want := []string{"limits.max_total_sessions", "models.sol.input",
		"paid_runs.codex.allow", "paid_runs.codex.calibrate", "paid_runs.codex.daily_cap_usd"}
	got := noticeKeys(cfg.Notices)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("notice keys = %v, want %v", got, want)
	}
	for _, n := range cfg.Notices {
		if !strings.Contains(n.Reason, ".duolog/config.local.yaml") || !strings.Contains(n.Reason, "Git tracks") {
			t.Errorf("notice %+v does not say Git tracks the local file", n)
		}
	}

	// Untracked in a repository, the local file is the person's own.
	root = isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.local.yaml"), "paid_runs: {codex: {allow: true, daily_cap_usd: 5}}\n")
	gitRepo(t, root)
	if cfg, err = Load(root); err != nil {
		t.Fatal(err)
	}
	if c := cfg.PaidRuns.Agent("codex"); !c.Allow || c.DailyCapUSD != 5 || len(cfg.Notices) != 0 {
		t.Errorf("untracked local = %+v notices %v, want allowed with cap 5", c, cfg.Notices)
	}
}
