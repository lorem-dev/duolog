package calibration

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

// candidate is an in-memory candidate of class under dir, made days before
// testNow, that passed every filter.
func candidate(id, class, dir string, days int) Candidate {
	return Candidate{ID: id, Class: class, Parent: "p-" + id, Ref: "r-" + id,
		Time: testNow.AddDate(0, 0, -days), StatementSource: "plan", Statement: "Do " + id,
		Files: files(dir, 2, 40), Lines: 40, Components: []string{dir},
		Features: map[string]string{"output": "code", "scope": "1-3", "components": "1"}}
}

func validAll(ctx context.Context, c Candidate) (string, string, error) { return "hidden", "", nil }

// bigSurvey is a project large enough for round 2 and every class.
func bigSurvey() Survey {
	return Survey{Files: 500, Commits: 500, HistoryDays: 400, CheckSeconds: 2}
}

func mineAll(t *testing.T, s Survey, candidates []Candidate, seed int64) Manifest {
	t.Helper()
	m, err := Mine(context.Background(), MineInput{Cfg: testConfig(t), Survey: s, Candidates: candidates,
		Seed: seed, Now: testNow, Validate: validAll})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func taskIDs(m Manifest) []string {
	var ids []string
	for _, task := range m.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// reason returns the exclusion reason of id, or "".
func reason(m Manifest, id string) string {
	for _, x := range m.Excluded {
		if x.ID == id {
			return x.Reason
		}
	}
	return ""
}

func TestSampleStratifiedAndCapped(t *testing.T) {
	var candidates []Candidate
	large := map[string]bool{}
	for i := range 12 {
		dir := "a"
		if i >= 8 {
			dir = fmt.Sprintf("b%d", i)
		}
		c := candidate(fmt.Sprintf("k-%02d", i), "implement", dir, i*7)
		if i%2 == 1 {
			c.Features["scope"] = "4-20"
		}
		if i == 1 || i == 2 {
			c.Large = true
			large[c.ID] = true
		}
		candidates = append(candidates, c)
	}
	byID := map[string]Candidate{}
	for _, c := range candidates {
		byID[c.ID] = c
	}

	m := mineAll(t, bigSurvey(), candidates, 42)
	if len(m.Tasks) != 6 || len(m.Round2) != 3 {
		t.Fatalf("%d tasks, %d in round 2, want 6 and 3: %v", len(m.Tasks), len(m.Round2), taskIDs(m))
	}
	fromA, larges := 0, 0
	for _, id := range taskIDs(m) {
		if byID[id].Components[0] == "a" {
			fromA++
		}
		if large[id] {
			larges++
		}
	}
	if fromA > 2 {
		t.Fatalf("%d selected tasks from a/, want at most 2", fromA)
	}
	if len(m.Reserve["implement"]) != 3 {
		t.Fatalf("Reserve = %v, want 3 implement tasks", m.Reserve)
	}
	for _, id := range m.Reserve["implement"] {
		if large[id] {
			larges++
		}
	}
	if larges > 1 {
		t.Fatalf("%d large tasks, want at most 1", larges)
	}
	if count := m.Counts["implement"]; count.Selected != 6 || count.Reserve != 3 || count.Candidates != 12 {
		t.Fatalf("Counts = %+v", count)
	}

	again := mineAll(t, bigSurvey(), slices.Clone(candidates), 42)
	if !slices.Equal(taskIDs(m), taskIDs(again)) {
		t.Fatalf("same seed gave %v and %v", taskIDs(m), taskIDs(again))
	}
}

func TestSampleSkipsSmallClass(t *testing.T) {
	var candidates []Candidate
	for i := range 9 {
		candidates = append(candidates, candidate(fmt.Sprintf("k-i%d", i), "implement", fmt.Sprintf("d%d", i), 10))
	}
	candidates = append(candidates, candidate("k-d1", "docs", "docs", 10), candidate("k-d2", "docs", "guide", 10))

	m := mineAll(t, bigSurvey(), candidates, 1)
	if got := m.Counts["docs"].Skipped; got != "fewer than 3 tasks" {
		t.Fatalf("docs Skipped = %q, want fewer than 3 tasks", got)
	}
	if m.Counts["docs"].Valid != 2 || m.Counts["docs"].WindowDays != 0 {
		t.Fatalf("docs count = %+v, want 2 valid after widening to all days", m.Counts["docs"])
	}
	for _, task := range m.Tasks {
		if task.Class == "docs" {
			t.Fatalf("docs task %s selected", task.ID)
		}
	}
	if m.Counts["implement"].Skipped != "" || m.Counts["implement"].Selected != 6 {
		t.Fatalf("implement count = %+v", m.Counts["implement"])
	}
}

// TestSampleClassInFewDirectoriesFillsRoundOne has many valid tasks under
// src/ and, in one case, an old one under lib/. The valid tasks span fewer
// than 3 top-level directories, so the directory cap gives way: round 1
// takes the task from lib/ first and fills up from src/, in a small project
// and in a large one. Round 2 keeps the cap.
func TestSampleClassInFewDirectoriesFillsRoundOne(t *testing.T) {
	var src []Candidate
	for i := range 8 {
		src = append(src, candidate(fmt.Sprintf("k-%d", i), "implement", "src", 10))
	}
	small := bigSurvey()
	small.Files = 150
	for _, s := range []Survey{small, bigSurvey()} {
		for _, lib := range []bool{false, true} {
			candidates := slices.Clone(src)
			if lib {
				candidates = append(candidates, candidate("k-lib", "implement", "lib", 300))
			}
			m := mineAll(t, s, candidates, 5)
			count := m.Counts["implement"]
			if count.Skipped != "" || len(m.Tasks) != 3 || len(m.Round2) != 0 || count.Reserve != 3 {
				t.Errorf("files %d lib %v: count %+v tasks %v round 2 %v, want 3 tasks in round 1 and 3 in the reserve",
					s.Files, lib, count, taskIDs(m), m.Round2)
			}
			if lib && !slices.Contains(taskIDs(m), "k-lib") {
				t.Errorf("files %d: tasks %v, want the task from lib/ in round 1", s.Files, taskIDs(m))
			}
		}
	}
}

// TestSampleWidensUntilRoundOneIsFull has 3 recent tasks under src/, which
// fill round 1 with one and the reserve with two, and older tasks in other
// directories: the window widens to take those into round 1.
func TestSampleWidensUntilRoundOneIsFull(t *testing.T) {
	var candidates []Candidate
	for i := range 3 {
		candidates = append(candidates, candidate(fmt.Sprintf("k-new%d", i), "implement", "src", 10))
	}
	candidates = append(candidates, candidate("k-old1", "implement", "lib", 120), candidate("k-old2", "implement", "cmd", 150))
	s := bigSurvey()
	s.Files = 150

	m := mineAll(t, s, candidates, 5)
	if count := m.Counts["implement"]; count.Skipped != "" || count.WindowDays != 180 || count.Selected != 3 {
		t.Fatalf("count %+v, want 3 tasks selected after widening to 180 days", count)
	}
	if ids := taskIDs(m); !slices.Contains(ids, "k-old1") || !slices.Contains(ids, "k-old2") {
		t.Fatalf("tasks %v, want both older tasks", ids)
	}
}

// TestMineValidationBudget makes every task fail validation, as when old
// commits no longer build: mining stops validating a class at its budget and
// says so for the rest.
func TestMineValidationBudget(t *testing.T) {
	var candidates []Candidate
	for i := range 200 {
		candidates = append(candidates, candidate(fmt.Sprintf("k-%03d", i), "implement", fmt.Sprintf("d%d", i), i))
	}
	for _, s := range []Survey{{Files: 150, Commits: 500}, bigSurvey()} {
		calls := 0
		m, err := Mine(context.Background(), MineInput{Cfg: testConfig(t), Survey: s, Candidates: slices.Clone(candidates), Seed: 1, Now: testNow,
			Validate: func(ctx context.Context, c Candidate) (string, string, error) {
				calls++
				return "", "checks fail on the reference", nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		budget := ValidationBudget(s)
		if budget >= len(candidates) || calls != budget {
			t.Fatalf("files %d: %d validations, want the budget %d", s.Files, calls, budget)
		}
		reasons := map[string]int{}
		for _, x := range m.Excluded {
			reasons[x.Reason]++
		}
		if reasons["checks fail on the reference"] != budget || reasons["validation budget reached"] != len(candidates)-budget {
			t.Fatalf("files %d: reasons %v", s.Files, reasons)
		}
	}
}

func TestSmallRepositoryGetsFewerClasses(t *testing.T) {
	var candidates []Candidate
	for class, n := range map[string]int{"implement": 6, "tests": 5, "docs": 4, "mechanical": 3} {
		for i := range n {
			candidates = append(candidates, candidate(fmt.Sprintf("k-%s-%d", class, i), class, fmt.Sprintf("%s%d", class, i), 10))
		}
	}
	s := bigSurvey()
	s.Files = 150

	m := mineAll(t, s, candidates, 7)
	var kept []string
	for class, count := range m.Counts {
		if count.Skipped == "" {
			kept = append(kept, class)
		}
	}
	slices.Sort(kept)
	if !slices.Equal(kept, []string{"docs", "implement", "tests"}) {
		t.Fatalf("kept classes %v, want the 3 with most valid tasks", kept)
	}
	if len(m.Round2) != 0 {
		t.Fatalf("Round2 = %v, want none in a small project", m.Round2)
	}
	if len(m.Tasks) != 9 {
		t.Fatalf("%d tasks, want 3 per kept class", len(m.Tasks))
	}
}
