package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/store"
)

const judgeCriterion = "structure matches what was asked"
const judgeCountCriterion = "unrequested files (count)"
const judgeSummary = `{"scores":[{"criterion":"structure matches what was asked","a":3,"b":1,"reason":"A follows the task"},{"criterion":"unrequested files (count)","a":2,"b":0,"count_a":1,"count_b":4,"reason":"B adds more files"}]}`

func stubShuffle(t *testing.T, originalFirst bool) {
	t.Helper()
	previous := shuffle
	shuffle = func() bool { return originalFirst }
	t.Cleanup(func() { shuffle = previous })
}

func judgeFixture(t *testing.T) (*fixture, Replay) {
	t.Helper()
	f := newFixture(t)
	t.Chdir(f.root)
	c := f.cfg.Learning.Classes[f.rec.Class]
	c.Rubric = []string{judgeCriterion, judgeCountCriterion}
	f.cfg.Learning.Classes[f.rec.Class] = c
	r := Replay{Task: f.rec.Task, Delegations: []string{f.rec.ID}, Class: f.rec.Class, Ladder: f.rec.Ladder, Role: f.rec.Role,
		Original: Side{Entry: "codex/terra/medium", Patch: []byte("patch one"), Summary: "solution one", Checks: []journal.Check{{Command: "git --version", Exit: 0}}},
		Profile:  Side{Entry: "codex/sol/high", Patch: []byte("patch two"), Summary: "solution two", USD: 0.4, Checks: []journal.Check{{Command: "missing", Exit: -1}}}}
	f.fake(t, func(context.Context, *config.Config, *store.Store, string, string, string) (delegate.Outcome, error) {
		return delegate.Outcome{Result: runner.Result{Done: true, Summary: judgeSummary, Usage: runner.Usage{Input: 1000, Cached: 200, Output: 300}}}, nil
	})
	return f, r
}

func TestJudgeTaskIsBlind(t *testing.T) {
	dir := ".duolog/judge/v-test"
	c := config.Class{Rubric: []string{judgeCriterion, judgeCountCriterion}}
	a, b := Side{Entry: "codex/sol/high", Patch: []byte("patch one"), Summary: "solution one"}, Side{Entry: "claude/opus/high", Patch: []byte("patch two"), Summary: "solution two"}
	data, files := JudgeTask(dir, c, []byte("Find a solution."), a, b)
	forbidden := regexp.MustCompile(`(?i)\b(codex|claude|opus|sol|gpt|high)\b`)
	all := [][]byte{data}
	for _, content := range files {
		all = append(all, content)
	}
	for _, content := range all {
		if forbidden.Match(content) {
			t.Fatalf("identity leaked: %s", content)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(dir), "task.md")
	writeTest(t, path, data)
	for name, content := range files {
		writeTest(t, filepath.Join(root, filepath.FromSlash(dir), name), content)
	}
	work := delegate.WorkTree(root)
	name, _, err := work.Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := taskspec.Validate(work.FS, name); err != nil {
		t.Fatal(err)
	}
	refs, err := taskspec.References(work.FS, name, work.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 5 {
		t.Fatal(refs)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.Path, dir+"/") {
			t.Fatal(ref.Path)
		}
	}
	inline, err := taskspec.Inline(work, work, path, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range files {
		if !bytes.Contains(inline, content) {
			t.Fatalf("file not inlined: %s", content)
		}
	}
}

func TestJudgeLabelsFollowShuffle(t *testing.T) {
	for _, first := range []bool{true, false} {
		t.Run(map[bool]string{true: "original-A", false: "original-B"}[first], func(t *testing.T) {
			f, r := judgeFixture(t)
			stubShuffle(t, first)
			f.fake(t, func(ctx context.Context, _ *config.Config, _ *store.Store, role, path, _ string) (delegate.Outcome, error) {
				o := delegate.OptionsFrom(ctx)
				want := config.Target{Agent: "claude", Model: "opus", Effort: "high"}
				if role != "review_final" || !o.Judge || o.Entry == nil || *o.Entry != want || o.WorkRoot != f.root || o.Lead != "judge-lead" || !o.AllowManualModel {
					t.Fatalf("role %q options %+v", role, o)
				}
				a, b := r.Original.Patch, r.Profile.Patch
				if !first {
					a, b = b, a
				}
				if !bytes.Equal(readTest(t, filepath.Join(filepath.Dir(path), "a.patch")), a) || !bytes.Equal(readTest(t, filepath.Join(filepath.Dir(path), "b.patch")), b) {
					t.Fatal("labels did not follow shuffle")
				}
				return delegate.Outcome{Result: runner.Result{Done: true, Summary: judgeSummary}}, nil
			})
			ctx := delegate.WithOptions(context.Background(), delegate.Options{Lead: "judge-lead", AllowManualModel: true})
			v, err := Judge(ctx, f.cfg, f.s, r)
			if err != nil {
				t.Fatal(err)
			}
			want := "B"
			if first {
				want = "A"
			}
			if v.OriginalLabel != want {
				t.Fatal(v)
			}
		})
	}
}

func TestParseVerdictRequiresEveryCriterion(t *testing.T) {
	_, err := ParseVerdict(`{"scores":[{"criterion":"first","a":3,"b":1,"reason":"ok"}]}`, []string{"first", "second"})
	if err == nil || !strings.Contains(err.Error(), "second") {
		t.Fatal(err)
	}
}

func TestParseVerdictRejectsOutOfRange(t *testing.T) {
	for _, score := range []string{`"a":4,"b":1`, `"a":1,"b":-1`, `"a":null,"b":1`, `"b":1`, `"a":1.5,"b":1`, `"a":"3","b":1`} {
		_, err := ParseVerdict(`{"scores":[{"criterion":"first",`+score+`,"reason":"ok"}]}`, []string{"first"})
		if err == nil || !strings.Contains(err.Error(), "first") {
			t.Fatalf("%s: %v", score, err)
		}
	}
}

func TestParseVerdictToleratesProse(t *testing.T) {
	scores, err := ParseVerdict("Here: "+judgeSummary+" done", []string{judgeCriterion, judgeCountCriterion})
	if err != nil || len(scores) != 2 || scores[0].A != 3 || scores[1].CountA == nil || *scores[1].CountA != 1 {
		t.Fatal(scores, err)
	}
}

func TestParseVerdictValidatesCountsAndCriteria(t *testing.T) {
	for _, summary := range []string{
		`{"scores":[{"criterion":"files (count)","a":1,"b":2,"reason":"ok"}]}`,
		`{"scores":[{"criterion":"files (count)","a":1,"b":2,"count_a":-1,"count_b":0,"reason":"ok"}]}`,
		`{"scores":[{"criterion":"unknown","a":1,"b":2,"reason":"ok"}]}`,
		`{"scores":[{"criterion":"files (count)","a":1,"b":2,"count_a":0,"count_b":0,"reason":"ok"},{"criterion":"files (count)","a":1,"b":2,"count_a":0,"count_b":0,"reason":"ok"}]}`,
	} {
		if _, err := ParseVerdict(summary, []string{"files (count)"}); err == nil {
			t.Fatal(summary)
		}
	}
}

func TestVerdictRecordedInOriginalTerms(t *testing.T) {
	f, r := judgeFixture(t)
	stubShuffle(t, false)
	v, err := Judge(context.Background(), f.cfg, f.s, r)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind != "verdict" || v.Task != r.Task || v.Delegation != f.rec.ID || v.Class != r.Class || v.Ladder != r.Ladder || v.Original != r.Original.Entry || v.Profile != r.Profile.Entry || v.Judge != "claude/opus/high" || v.ReplayUSD != 0.4 || v.Scores[0].Original != 1 || v.Scores[0].Profile != 3 || *v.Scores[1].OriginalCount != 4 || *v.Scores[1].ProfileCount != 1 {
		t.Fatalf("%+v", v)
	}
	wantCost, _ := usage.Cost(f.cfg, "claude", "opus", usage.Tokens{Input: 1000, Cached: 200, Output: 300})
	if v.JudgeUSD != wantCost || !reflect.DeepEqual(v.OriginalChecks, r.Original.Checks) || !reflect.DeepEqual(v.ProfileChecks, r.Profile.Checks) {
		t.Fatal(v)
	}
	vs, err := journal.ReadAll[journal.Verdict](f.j, "verdicts.ndjson")
	if err != nil || len(vs) != 1 || !reflect.DeepEqual(vs[0], v) {
		t.Fatal(vs, err)
	}
}

func TestBadJudgeAnswerRecordsNothing(t *testing.T) {
	f, r := judgeFixture(t)
	var dir string
	f.fake(t, func(_ context.Context, _ *config.Config, _ *store.Store, _, path, _ string) (delegate.Outcome, error) {
		dir = filepath.Dir(path)
		return delegate.Outcome{Result: runner.Result{Done: true, Summary: "no scores"}}, nil
	})
	_, err := Judge(context.Background(), f.cfg, f.s, r)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, "answer.json")) {
		t.Fatal(err)
	}
	data := readTest(t, filepath.Join(dir, "answer.json"))
	var answer struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || answer.Summary != "no scores" {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(f.j.Dir, "verdicts.ndjson")); !os.IsNotExist(err) {
		t.Fatal("bad verdict recorded", err)
	}
}
