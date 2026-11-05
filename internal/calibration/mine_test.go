package calibration

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMineWidensWindow(t *testing.T) {
	candidates := []Candidate{candidate("k-new", "implement", "n", 30)}
	for i := range 4 {
		candidates = append(candidates, candidate(fmt.Sprintf("k-old%d", i), "implement", fmt.Sprintf("o%d", i), 120))
	}
	// A small project has no round 2, so the 3 round-1 tasks are all selected.
	s := bigSurvey()
	s.Files = 150

	m := mineAll(t, s, candidates, 3)
	if len(m.Tasks) != 3 {
		t.Fatalf("%d tasks selected, want 3: %v", len(m.Tasks), taskIDs(m))
	}
	if got := m.Counts["implement"].WindowDays; got != 180 {
		t.Fatalf("WindowDays = %d, want 180", got)
	}
}

func TestMineShallowHistoryReportsSkip(t *testing.T) {
	root := project(t)
	write(t, root, "main.go", "package main\n\nfunc main() {}\n")
	commit(t, root, "start")
	write(t, root, "calc.go", "package main\n\n"+lines("// line", 12))
	commit(t, root, "add calc")
	candidates, _ := collect(t, root, nil)

	m, err := Mine(context.Background(), MineInput{Root: root, Cfg: testConfig(t),
		Survey: Survey{Files: 2, Commits: 2, HistoryDays: 1}, Candidates: candidates, Now: testNow, Validate: validAll})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Counts) == 0 {
		t.Fatal("no class counts")
	}
	for class, count := range m.Counts {
		if count.Skipped != "fewer than 3 tasks" {
			t.Errorf("class %s: Skipped %q, want fewer than 3 tasks", class, count.Skipped)
		}
	}
	if len(m.Tasks) != 0 {
		t.Fatalf("tasks %v, want none", taskIDs(m))
	}
}

func TestManifestHasReasonForEveryExclusion(t *testing.T) {
	var candidates []Candidate
	for i := range 14 {
		candidates = append(candidates, candidate(fmt.Sprintf("k-i%02d", i), "implement", fmt.Sprintf("d%d", i), i*30))
	}
	reverted := candidate("k-revert", "implement", "r", 5)
	reverted.Excluded = "revert"
	candidates = append(candidates, reverted, candidate("k-doc", "docs", "docs", 5))
	validate := func(ctx context.Context, c Candidate) (string, string, error) {
		if c.ID == "k-i01" || c.ID == "k-i02" {
			return "", "no verification", nil
		}
		return "hidden", "", nil
	}
	m, err := Mine(context.Background(), MineInput{Cfg: testConfig(t), Survey: bigSurvey(), Candidates: candidates,
		Seed: 5, Now: testNow, Validate: validate})
	if err != nil {
		t.Fatal(err)
	}

	kept := map[string]bool{}
	for _, id := range taskIDs(m) {
		kept[id] = true
	}
	for _, ids := range m.Reserve {
		for _, id := range ids {
			kept[id] = true
		}
	}
	excluded := map[string]string{}
	for _, x := range m.Excluded {
		if x.Reason == "" {
			t.Errorf("%s excluded without a reason", x.ID)
		}
		if _, twice := excluded[x.ID]; twice || kept[x.ID] {
			t.Errorf("%s listed twice", x.ID)
		}
		excluded[x.ID] = x.Reason
	}
	for _, c := range candidates {
		if _, ok := excluded[c.ID]; !ok && !kept[c.ID] {
			t.Errorf("%s is neither selected, reserve nor excluded", c.ID)
		}
	}
	if excluded["k-revert"] != "revert" || excluded["k-i01"] != "no verification" {
		t.Fatalf("reasons %v", excluded)
	}
	if !strings.Contains(excluded["k-doc"], "fewer than 3 tasks") {
		t.Fatalf("k-doc reason %q, want the skipped class", excluded["k-doc"])
	}
	if !strings.Contains(strings.Join(mapValues(excluded), "\n"), "not sampled") {
		t.Fatalf("no candidate is left unsampled: %v", excluded)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestTaskFilesAreNotMarkdown(t *testing.T) {
	root := project(t)
	write(t, root, "calc.go", "package calc\n\nfunc One() int { return 1 }\n")
	first := commit(t, root, "start")
	write(t, root, "calc.go", "package calc\n\nfunc One() int { return 1 }\n\n// Sum adds two numbers.\nfunc Sum(a, b int) int {\n\treturn a + b\n}\n")
	write(t, root, "calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSum(t *testing.T) {\n\tif Sum(1, 2) != 3 {\n\t\tt.Fatal(\"sum\")\n\t}\n}\n")
	ref := commit(t, root, "add sum to calc\n\nAdd a Sum function after "+first[:9]+".\nThis is the add sum to calc change.")
	candidates, _ := collect(t, root, nil)
	c := only(t, candidates, ref)

	state := stateDir(t)
	run, err := NewRun(state, testNow)
	if err != nil {
		t.Fatal(err)
	}
	runDir := run.Dir(state)
	m := Manifest{Tasks: []ManifestTask{{ID: c.ID, Class: c.Class, Verification: "hidden"}}}
	if err := SaveManifest(runDir, m); err != nil {
		t.Fatal(err)
	}
	if err := WriteTaskFiles(context.Background(), runDir, root, c); err != nil {
		t.Fatal(err)
	}

	tasks := filepath.Join(runDir, "tasks")
	err = filepath.WalkDir(tasks, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(name), ".md") {
			t.Errorf("%s is Markdown", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tasks, c.ID)
	for _, name := range []string{"statement.txt", "reference.patch", "hidden.patch", "task.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	statement, err := os.ReadFile(filepath.Join(dir, "statement.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(statement)
	if !strings.Contains(text, "Sum function") {
		t.Fatalf("statement.txt = %q, want the statement", text)
	}
	if strings.Contains(text, first[:7]) || strings.Contains(text, ref[:7]) || strings.Contains(text, "add sum to calc") {
		t.Fatalf("statement.txt names a sha or the subject: %q", text)
	}
	hidden, err := os.ReadFile(filepath.Join(dir, "hidden.patch"))
	if err != nil {
		t.Fatal(err)
	}
	reference, err := os.ReadFile(filepath.Join(dir, "reference.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hidden), "calc_test.go") || strings.Contains(string(reference), "calc_test.go") ||
		!strings.Contains(string(reference), "func Sum") {
		t.Fatalf("hidden.patch %q\nreference.patch %q", hidden, reference)
	}

	record, err := ReadTask(runDir, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.ID != c.ID || record.Verification != "hidden" || record.Parent != first || record.Ref != ref ||
		!record.NeedsRewrite || len(record.Hidden) != 1 || len(record.HiddenNames) != 1 {
		t.Fatalf("ReadTask = %+v", record)
	}
}
