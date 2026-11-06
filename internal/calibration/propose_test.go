package calibration

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
)

// confirmedVerdict is a calibration verdict on task with original's mean
// score a and profile's b, and the person's confirmation.
func confirmedVerdict(id, run, task, original, profile string, a, b int) []journal.Verdict {
	return []journal.Verdict{
		{Kind: "verdict", ID: id, Run: run, Task: task, Class: "implement", Original: original, Profile: profile, Source: "calibration",
			Scores: []journal.Score{{Criterion: "correct", Original: a, Profile: b}}},
		{Kind: "decision", ID: id, Decision: "confirm"},
	}
}

func TestSummarizeByFamily(t *testing.T) {
	// codex/sol/medium: 3 results, 2 passed, 2 confirmed (2.0, 3.0)
	// row == {Agent "codex", Family "sol", Effort "medium", Tasks 3, TasksPassed 2, Judged 2, JudgedQuality 2.5, Date "2026-11-04", DuologVersion "1.3.0"}
	at := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	result := func(run, task, entry string, passed bool, when time.Time) journal.CalibrationResult {
		return journal.CalibrationResult{Kind: "result", Time: when, Run: run, Task: task, Class: "implement", Entry: entry,
			Verification: "hidden", Passed: passed, Source: "calibration"}
	}
	cs := []journal.CalibrationResult{
		// An earlier run of the class does not count.
		result("cal-0", "t0", "codex/sol/medium", false, at.Add(-time.Hour)),
		result("cal-1", "t1", "codex/sol/medium", true, at),
		result("cal-1", "t1", "claude/opus/high", true, at),
		result("cal-1", "t2", "codex/sol/medium", true, at),
		result("cal-1", "t2", "claude/opus/high", true, at),
		result("cal-1", "t3", "codex/sol/medium", false, at),
	}
	var vs []journal.Verdict
	vs = append(vs, confirmedVerdict("v1", "cal-1", "t1", "claude/opus/high", "codex/sol/medium", 3, 2)...)
	vs = append(vs, confirmedVerdict("v2", "cal-1", "t2", "claude/opus/high", "codex/sol/medium", 1, 3)...)
	// A verdict the person has not decided does not count.
	vs = append(vs, journal.Verdict{Kind: "verdict", ID: "v3", Run: "cal-1", Task: "t3", Class: "implement", Original: "claude/opus/high",
		Profile: "codex/sol/medium", Source: "calibration", Scores: []journal.Score{{Criterion: "correct", Original: 1, Profile: 0}}})

	now := time.Date(2026, 11, 5, 1, 30, 0, 0, time.FixedZone("east", 3*60*60))
	got := Summarize(testConfig(t), cs, vs, now, "1.3.0")
	rows := got["implement"]
	i := slices.IndexFunc(rows, func(r EvidenceRow) bool { return r.Agent == "codex" })
	if i < 0 {
		t.Fatalf("no codex row in %+v", got)
	}
	row := rows[i]
	want := EvidenceRow{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 3, TasksPassed: 2, Judged: 2, Evidence: 3, Date: "2026-11-04", DuologVersion: "1.3.0"}
	if row.JudgedQuality == nil || *row.JudgedQuality != 2.5 {
		t.Fatalf("JudgedQuality = %v, want 2.5", row.JudgedQuality)
	}
	row.JudgedQuality = nil
	// now is 2026-11-04 22:30 in UTC.
	if row != want {
		t.Fatalf("row = %+v, want %+v", row, want)
	}
	j := slices.IndexFunc(rows, func(r EvidenceRow) bool { return r.Agent == "claude" })
	if j < 0 || rows[j].Tasks != 2 || rows[j].TasksPassed != 2 || rows[j].Judged != 2 || rows[j].JudgedQuality == nil || *rows[j].JudgedQuality != 2 {
		t.Fatalf("claude row = %+v", rows)
	}
	if len(rows) != 2 || len(got) != 1 {
		t.Fatalf("Summarize = %+v, want two rows of implement", got)
	}

	// Without a judged task the quality stays empty.
	unjudged := Summarize(testConfig(t), cs, nil, now, "1.3.0")["implement"]
	for _, r := range unjudged {
		if r.Judged != 0 || r.JudgedQuality != nil {
			t.Fatalf("row without verdicts = %+v", r)
		}
	}
}

func TestBuildProjectPartHasNoPersonalData(t *testing.T) {
	const identity = "Robin Sample"
	s := Survey{
		Facts:        SurveyFacts{Languages: []string{"go"}, Build: []string{"go build ./..."}, Test: []string{"go vet ./...", "go test ./..."}, Components: []string{"cmd", "internal"}},
		Files:        420,
		Commits:      300,
		HistoryDays:  200,
		MedianLines:  40,
		CheckSeconds: 12.5,
		Checks:       map[string][]string{"implement": {"go vet ./...", "go test ./..."}},
		Notes:        []string{"history by " + identity + " <robin@example.invalid>", "quota and token figures stay personal"},
	}
	task := ManifestTask{ID: "t1", Statement: "Add a Value function that returns its number.", StatementSource: "plan", Class: "implement",
		Verification: "hidden", Sources: []string{"commit:abc123"}, Hidden: []string{"value_test.go"}, Features: map[string]string{"output": "code"}}
	m := Manifest{
		Seed:         7,
		Tasks:        []ManifestTask{task},
		Counts:       map[string]ClassCount{"implement": {Candidates: 5, Selected: 1}},
		Excluded:     []Exclusion{{ID: "c9", Reason: "bot commit by " + identity}},
		Estimates:    map[string]Estimate{"t1": {Files: 1, Lines: 20, TokensLow: 80000, TokensHigh: 250000, Minutes: 8}},
		Verification: map[string]string{"t1": "hidden"},
		RepoFiles:    420,
		HistoryDays:  200,
		CheckSeconds: 12.5,
	}
	evidence := map[string][]EvidenceRow{"implement": {
		{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 3, TasksPassed: 2, Judged: 2, JudgedQuality: quality(2.5), Date: "2026-11-05", DuologVersion: "1.3.0"},
	}}
	p := BuildProjectPart(s, m, evidence)
	if p.Version != 1 {
		t.Fatalf("Version = %d, want 1", p.Version)
	}
	if !reflect.DeepEqual(p.Tasks, []ManifestTask{task}) || !reflect.DeepEqual(p.Evidence, evidence) || !reflect.DeepEqual(p.Survey, s.Facts) {
		t.Fatalf("project part = %+v", p)
	}
	if err := ValidateProject(p); err != nil {
		t.Fatal(err)
	}
	data, err := RenderProject(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"usd", "token", "quota", "@", identity} {
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(leak)) {
			t.Errorf("project part holds %q:\n%s", leak, data)
		}
	}
}

func TestBuildProjectPartKeepsPassingTests(t *testing.T) {
	s := Survey{
		Facts:  SurveyFacts{Languages: []string{"go"}, Test: []string{"go vet ./...", "go test ./...", "npm test"}},
		Failed: []string{"npm test"},
	}
	p := BuildProjectPart(s, Manifest{}, nil)
	if want := []string{"go vet ./...", "go test ./..."}; !reflect.DeepEqual(p.Survey.Test, want) {
		t.Fatalf("Survey.Test = %v, want %v", p.Survey.Test, want)
	}
	if want := []string{"go vet ./...", "go test ./...", "npm test"}; !reflect.DeepEqual(s.Facts.Test, want) {
		t.Fatalf("BuildProjectPart changed the survey: %v", s.Facts.Test)
	}
}

func TestBuildProjectPartLeavesVersionPinsOut(t *testing.T) {
	evidence := map[string][]EvidenceRow{"implement": {
		{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 3, Date: "2026-11-05", DuologVersion: "1.3.0"},
		{Agent: "codex", Family: "gpt-6.1-sol", Effort: "medium", Tasks: 3, Date: "2026-11-05", DuologVersion: "1.3.0"},
	}}
	p := BuildProjectPart(Survey{}, Manifest{}, evidence)
	if rows := p.Evidence["implement"]; len(rows) != 1 || rows[0].Family != "sol" {
		t.Fatalf("Evidence = %+v, want the sol row only", p.Evidence)
	}
	if err := ValidateProject(p); err != nil {
		t.Fatal(err)
	}
}

func TestSurveyRecordsFailedChecks(t *testing.T) {
	root := goProject(t)
	s, err := RunSurvey(t.Context(), root, testConfig(t), func(_ context.Context, dir string, commands []string) []journal.Check {
		var out []journal.Check
		for _, c := range commands {
			exit := 0
			if c == "go vet ./..." {
				exit = 1
			}
			out = append(out, journal.Check{Command: c, Exit: exit, Seconds: 1})
		}
		return out
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Failed, []string{"go vet ./..."}) {
		t.Fatalf("Failed = %v, want go vet", s.Failed)
	}
	if p := BuildProjectPart(s, Manifest{}, nil); !reflect.DeepEqual(p.Survey.Test, []string{"go test ./..."}) {
		t.Fatalf("project Test = %v", p.Survey.Test)
	}
}

func TestSeedStatsFromProjectPart(t *testing.T) {
	// Tasks 3, Passed 2, Judged 2, JQ 2.5, Evidence 3: Quality 5.0/3, Weight 3, CalibrationTasks 3
	cfg := testConfig(t)
	row := EvidenceRow{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 3, TasksPassed: 2, Judged: 2, JudgedQuality: quality(2.5), Evidence: 3, Date: "2026-11-05", DuologVersion: "1.3.0"}
	p := ProjectPart{Version: 1, Evidence: map[string][]EvidenceRow{
		"tests": {row},
		// Three passes no other entry matched: evidence without a verdict.
		"skeleton.plan": {{Agent: "claude", Family: "opus", Effort: "high", Tasks: 3, TasksPassed: 3, Evidence: 3, Date: "2026-11-05", DuologVersion: "1.3.0"}},
		"unknown_class": {row},
	}}
	st := SeedStats(cfg, p)
	s, ok := st["tests"]["codex/sol/medium"]
	if !ok {
		t.Fatalf("SeedStats = %+v, want tests codex/sol/medium", st)
	}
	if math.Abs(s.Quality-5.0/3) > 1e-9 || s.Weight != 3 || s.CalibrationTasks != 3 {
		t.Fatalf("stat = %+v, want quality 5/3, weight 3, calibration tasks 3", s)
	}
	// Passed and unjudged adds tasks but no quality.
	if s := st["skeleton.plan"]["claude/opus/high"]; s.Weight != 0 || s.CalibrationTasks != 3 {
		t.Fatalf("skeleton.plan stat = %+v", s)
	}
	if _, ok := st["unknown_class"]; ok {
		t.Fatalf("SeedStats keeps a ladder the config lacks: %+v", st)
	}
}

func TestSeedStatsUsesTheConfiguredAlias(t *testing.T) {
	cfg := testConfig(t)
	cfg.Models["sol-fast"] = config.Model{Agent: "codex", Family: "sol"}
	class := cfg.Learning.Classes["tests"]
	class.Ladder = []config.LadderEntry{{Agent: "codex", Model: "sol-fast", Effort: "medium", Prior: 2.9}}
	cfg.Learning.Classes["tests"] = class
	p := ProjectPart{Version: 1, Evidence: map[string][]EvidenceRow{"tests": {
		{Agent: "codex", Family: "sol", Effort: "medium", Tasks: 2, TasksPassed: 1, Evidence: 2, Date: "2026-11-05", DuologVersion: "1.3.0"},
	}}}
	st := SeedStats(cfg, p)
	if s, ok := st["tests"]["codex/sol-fast/medium"]; !ok || s.CalibrationTasks != 2 || s.Weight != 1 || s.Quality != 0 {
		t.Fatalf("SeedStats = %+v, want the row under codex/sol-fast/medium", st)
	}
}

// aliasConfig has the project alias sol-fast of the sol family on the tests
// ladder.
func aliasConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.Models["sol-fast"] = config.Model{Agent: "codex", Family: "sol"}
	class := cfg.Learning.Classes["tests"]
	class.Ladder = []config.LadderEntry{
		{Agent: "codex", Model: "sol-fast", Effort: "medium", Prior: 2.9},
		{Agent: "claude", Model: "opus", Effort: "high", Prior: 2.8},
	}
	cfg.Learning.Classes["tests"] = class
	return cfg
}

func TestSummarizeRecordsAliasFamily(t *testing.T) {
	cfg := aliasConfig(t)
	cs := []journal.CalibrationResult{{Kind: "result", Time: testTime, Run: "cal-1", Task: "t1", Class: "tests", Ladder: "tests",
		Entry: "codex/sol-fast/medium", Verification: "hidden", Source: "calibration"}}
	evidence := Summarize(cfg, cs, nil, testTime, "1.3.0")
	if rows := evidence["tests"]; len(rows) != 1 || rows[0].Family != "sol" {
		t.Fatalf("Summarize = %+v, want the sol family", evidence)
	}
	p := BuildProjectPart(Survey{}, Manifest{}, evidence)
	if rows := p.Evidence["tests"]; len(rows) != 1 || rows[0].Family != "sol" {
		t.Fatalf("project evidence = %+v, want the sol row", p.Evidence)
	}
	if s, ok := SeedStats(cfg, p)["tests"]["codex/sol-fast/medium"]; !ok || s.CalibrationTasks != 1 {
		t.Fatalf("seed = %+v, want the row back under the alias", SeedStats(cfg, p))
	}
}

// TestSeedStatsMatchesPersonalEvidence goes from the person's calibration
// results through the shared summary to a teammate's seed: judge-only tasks
// keep their half weight, and only tasks with evidence count toward the
// three a change needs, as in the person's own aggregation.
func TestSeedStatsMatchesPersonalEvidence(t *testing.T) {
	cfg := aliasConfig(t)
	const sol, opus = "codex/sol-fast/medium", "claude/opus/high"
	result := func(task, entry, verification string, passed bool) journal.CalibrationResult {
		return journal.CalibrationResult{Kind: "result", Time: testTime, Run: "cal-1", Task: task, Class: "tests", Ladder: "tests",
			Entry: entry, Verification: verification, Passed: passed, Source: "calibration"}
	}
	cs := []journal.CalibrationResult{
		result("t1", sol, "judge_only", true), result("t1", opus, "judge_only", true),
		result("t2", sol, "hidden", true), result("t2", opus, "hidden", true),
		result("t3", sol, "hidden", true), result("t3", opus, "hidden", true),
		result("t4", sol, "hidden", false), result("t4", opus, "hidden", true),
		result("t5", sol, "judge_only", false), result("t5", opus, "judge_only", false),
	}
	var vs []journal.Verdict
	vs = append(vs, confirmedVerdict("v1", "cal-1", "t1", opus, sol, 1, 3)...)
	vs = append(vs, confirmedVerdict("v2", "cal-1", "t2", opus, sol, 2, 2)...)
	// t3 waits for the person.
	vs = append(vs, journal.Verdict{Kind: "verdict", ID: "v3", Run: "cal-1", Task: "t3", Class: "tests", Original: opus, Profile: sol,
		Source: "calibration", Scores: []journal.Score{{Criterion: "correct", Original: 3, Profile: 0}}})

	personal := learn.AggregateAll(cfg, nil, nil, vs, cs)["tests"]
	p := BuildProjectPart(Survey{}, Manifest{}, Summarize(cfg, cs, vs, testTime, "1.3.0"))
	if err := ValidateProject(p); err != nil {
		t.Fatal(err)
	}
	data, err := RenderProject(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := stateDir(t)
	if err := os.MkdirAll(Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath(dir), data, 0o644); err != nil {
		t.Fatal(err)
	}
	shared, _, err := ReadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	seed := SeedStats(cfg, shared)["tests"]
	for _, entry := range []string{sol, opus} {
		got, want := seed[entry], personal[entry]
		if math.Abs(got.Weight-want.Weight) > 1e-9 || math.Abs(got.Quality-want.Quality) > 1e-9 || got.CalibrationTasks != want.CalibrationTasks {
			t.Errorf("%s: seed %+v, personal %+v", entry, got, want)
		}
	}
	// sol: t1 judged at half weight, t2 judged, t4 and t5 failed; t3 has no
	// decided verdict and no evidence yet.
	if s := personal[sol]; s.Weight != 3.5 || s.CalibrationTasks != 4 {
		t.Fatalf("personal sol = %+v, want weight 3.5 and 4 tasks with evidence", s)
	}
}

// TestManifestJSONIsSnakeCase keeps manifest.json, task.json and survey.json
// in the same snake_case as project.yaml.
func TestManifestJSONIsSnakeCase(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(SurveyFacts{}),
		reflect.TypeOf(ManifestTask{}),
		reflect.TypeOf(Manifest{}),
		reflect.TypeOf(ClassCount{}),
		reflect.TypeOf(Exclusion{}),
		reflect.TypeOf(Estimate{}),
		reflect.TypeOf(TaskRecord{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Anonymous {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name != strings.ToLower(name) {
				t.Errorf("%s.%s: json tag %q, want snake_case", typ.Name(), f.Name, name)
			}
			if y, ok := f.Tag.Lookup("yaml"); ok {
				if yname, _, _ := strings.Cut(y, ","); yname != name {
					t.Errorf("%s.%s: json %q, yaml %q", typ.Name(), f.Name, name, yname)
				}
			}
		}
	}
	data, err := json.Marshal(TaskRecord{ManifestTask: ManifestTask{ID: "t1", StatementSource: "plan"}, HiddenNames: []string{"TestValue"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"id":"t1"`, `"statement_source":"plan"`, `"hidden_names":["TestValue"]`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("task.json %s lacks %s", data, key)
		}
	}
}
