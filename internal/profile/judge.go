package profile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/usage"
)

// shuffle returns true when the original goes first. Ordering is kept out of
// the judge's files and is recorded only after its answer has been parsed.
var shuffle = func() bool {
	var bit [1]byte
	if _, err := rand.Read(bit[:]); err != nil {
		panic(err)
	}
	return bit[0]&1 == 1
}

type Scored struct {
	Criterion string `json:"criterion"`
	A         int    `json:"a"`
	B         int    `json:"b"`
	CountA    *int   `json:"count_a,omitempty"`
	CountB    *int   `json:"count_b,omitempty"`
	Reason    string `json:"reason"`
}

// JudgeTask builds neutral side files and work-root-relative Read references.
// Entry names are deliberately excluded from every file it produces.
func JudgeTask(dir string, c config.Class, original []byte, first, second Side) (task []byte, files map[string][]byte) {
	files = map[string][]byte{
		"original-task.md": original,
		"a.patch":          first.Patch,
		"a.answer.txt":     []byte(first.Summary),
		"b.patch":          second.Patch,
		"b.answer.txt":     []byte(second.Summary),
	}
	var text strings.Builder
	text.WriteString("# Task: compare two results\n\n## Goal\nCompare results A and B of the same task using only this rubric:\n")
	for _, criterion := range c.Rubric {
		fmt.Fprintf(&text, "- %s\n", criterion)
	}
	text.WriteString("\n## Read\n")
	for _, name := range []string{"original-task.md", "a.patch", "a.answer.txt", "b.patch", "b.answer.txt"} {
		fmt.Fprintf(&text, "- `%s/%s`\n", strings.TrimSuffix(filepath.ToSlash(dir), "/"), name)
	}
	text.WriteString("\n## Write\nNone.\n\n## Limits\nScore every criterion from 0 to 3 for A and B: 3 is best, 0 is worst. For (count) criteria, fewer is better; the score measures quality, not the count. Give nonnegative counts for criteria marked (count). Give one reason per criterion. Do not guess which model wrote either side. Read only the listed files and do not search for the identity or history of either side.\n\n## Check\nThe answer's summary must contain one JSON object with this shape:\n")
	text.WriteString("```json\n{\"scores\":[{\"criterion\":\"rubric criterion\",\"a\":0,\"b\":0,\"count_a\":0,\"count_b\":0,\"reason\":\"one reason\"}]}\n```\n")
	return []byte(text.String()), files
}

// ParseVerdict accepts surrounding prose, then validates and orders the scores
// by the supplied rubric. Missing scores must not silently become zeroes.
func ParseVerdict(summary string, rubric []string) ([]Scored, error) {
	start, end := strings.IndexByte(summary, '{'), strings.LastIndexByte(summary, '}')
	if start < 0 || end < start {
		return nil, fmt.Errorf("judge answer has no verdict JSON object")
	}
	var parsed struct {
		Scores []json.RawMessage `json:"scores"`
	}
	if err := json.Unmarshal([]byte(summary[start:end+1]), &parsed); err != nil {
		return nil, fmt.Errorf("invalid judge verdict: %w", err)
	}
	known := make(map[string]bool, len(rubric))
	for _, criterion := range rubric {
		known[criterion] = true
	}
	byCriterion := make(map[string]Scored, len(parsed.Scores))
	for _, raw := range parsed.Scores {
		var s struct {
			Criterion string `json:"criterion"`
			A         *int   `json:"a"`
			B         *int   `json:"b"`
			CountA    *int   `json:"count_a"`
			CountB    *int   `json:"count_b"`
			Reason    string `json:"reason"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("criterion %q: %w", s.Criterion, err)
		}
		if !known[s.Criterion] {
			return nil, fmt.Errorf("unknown criterion %q", s.Criterion)
		}
		if _, ok := byCriterion[s.Criterion]; ok {
			return nil, fmt.Errorf("criterion %q appears more than once", s.Criterion)
		}
		if s.A == nil || s.B == nil || *s.A < 0 || *s.A > 3 || *s.B < 0 || *s.B > 3 {
			return nil, fmt.Errorf("criterion %q needs scores 0..3 for A and B", s.Criterion)
		}
		if strings.Contains(s.Criterion, "(count)") && (s.CountA == nil || s.CountB == nil) {
			return nil, fmt.Errorf("criterion %q needs counts for A and B", s.Criterion)
		}
		if s.CountA != nil && *s.CountA < 0 || s.CountB != nil && *s.CountB < 0 {
			return nil, fmt.Errorf("criterion %q has a negative count", s.Criterion)
		}
		if strings.TrimSpace(s.Reason) == "" {
			return nil, fmt.Errorf("criterion %q needs a reason", s.Criterion)
		}
		byCriterion[s.Criterion] = Scored{Criterion: s.Criterion, A: *s.A, B: *s.B, CountA: s.CountA, CountB: s.CountB, Reason: s.Reason}
	}
	scores := make([]Scored, 0, len(rubric))
	for _, criterion := range rubric {
		s, ok := byCriterion[criterion]
		if !ok {
			return nil, fmt.Errorf("missing criterion %q", criterion)
		}
		scores = append(scores, s)
	}
	return scores, nil
}

// Judge compares a replay blind and records scores in original/profile terms.
// Its delegation is excluded from the learning journal by Options.Judge.
func Judge(ctx context.Context, cfg *config.Config, s *store.Store, r Replay) (journal.Verdict, error) {
	var v journal.Verdict
	if len(r.Delegations) == 0 {
		return v, fmt.Errorf("judge needs a recorded delegation")
	}
	c, ok := cfg.Learning.Classes[r.Class]
	if !ok || len(c.Rubric) == 0 {
		return v, fmt.Errorf("class %q has no judge rubric", r.Class)
	}
	from, err := config.ParseEntryKey(r.Original.Entry)
	if err != nil {
		return v, err
	}
	to, err := config.ParseEntryKey(r.Profile.Entry)
	if err != nil {
		return v, err
	}
	target := learning.JudgeTarget(cfg.Learning, from.Target(), to.Target())
	if target.Agent == "" {
		return v, fmt.Errorf("no judge target configured")
	}
	j := &journal.Journal{Dir: filepath.Join(s.Dir, "journal")}
	var originals [][]byte
	for _, id := range r.Delegations {
		data, err := os.ReadFile(filepath.Join(j.TaskDir(r.Task, id), "task.md"))
		if err != nil {
			return v, err
		}
		originals = append(originals, data)
	}
	opts := delegate.OptionsFrom(ctx)
	work := opts.WorkRoot
	if work == "" {
		work = project.CurrentWorkRoot(filepath.Dir(s.Dir))
	}
	work, err = filepath.Abs(work)
	if err != nil {
		return v, err
	}
	judgeRoot := filepath.Join(work, ".duolog", "judge")
	if err := os.MkdirAll(judgeRoot, 0700); err != nil {
		return v, err
	}
	var id, dir string
	for {
		id = journal.NewID("v", time.Now())
		dir = filepath.Join(judgeRoot, id)
		if err := os.Mkdir(dir, 0700); os.IsExist(err) {
			continue
		} else if err != nil {
			return v, err
		}
		break
	}
	originalFirst := shuffle()
	first, second := r.Original, r.Profile
	if !originalFirst {
		first, second = second, first
	}
	task, files := JudgeTask(filepath.ToSlash(filepath.Join(".duolog", "judge", id)), c, bytes.Join(originals, []byte("\n\n")), first, second)
	files["task.md"] = task
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return v, err
		}
	}
	ctxRun := delegate.WithOptions(ctx, delegate.Options{Judge: true, Entry: &target, WorkRoot: work, Lead: opts.Lead, AllowManualModel: opts.AllowManualModel})
	out, err := runDelegate(ctxRun, cfg, s, "review_final", filepath.Join(dir, "task.md"), "")
	if err != nil {
		return v, fmt.Errorf("judge: %w", err)
	}
	// Retain the worker answer shape beside the neutral inputs, including an
	// invalid summary, so a parsing error has a concrete answer path to inspect.
	answer := struct {
		Done      bool     `json:"done"`
		Summary   string   `json:"summary"`
		Files     []string `json:"files"`
		Needs     []string `json:"needs"`
		Decisions []string `json:"decisions,omitempty"`
	}{out.Result.Done, out.Result.Summary, append([]string{}, out.Result.Files...), append([]string{}, out.Result.Needs...), out.Result.Decisions}
	data, err := json.Marshal(answer)
	if err != nil {
		return v, err
	}
	answerPath := filepath.Join(dir, "answer.json")
	if err := os.WriteFile(answerPath, append(data, '\n'), 0600); err != nil {
		return v, err
	}
	if err := ctx.Err(); err != nil {
		return v, err
	}
	if !out.Result.Done || out.Result.Interrupted || out.RunHere != nil || len(out.Needs) > 0 {
		return v, fmt.Errorf("judge did not finish; answer: %s", answerPath)
	}
	scores, err := ParseVerdict(out.Result.Summary, c.Rubric)
	if err != nil {
		return v, fmt.Errorf("judge: %w; answer: %s", err, answerPath)
	}
	v = journal.Verdict{Kind: "verdict", Time: time.Now().UTC(), ID: id, Task: r.Task, Delegation: r.Delegations[0], Class: r.Class, Ladder: r.Ladder, Original: r.Original.Entry, Profile: r.Profile.Entry, Judge: entryKey(target), OriginalLabel: "A", OriginalChecks: r.Original.Checks, ProfileChecks: r.Profile.Checks, ReplayUSD: r.Profile.USD}
	if !originalFirst {
		v.OriginalLabel = "B"
	}
	for _, s := range scores {
		score := journal.Score{Criterion: s.Criterion, Original: s.A, Profile: s.B, OriginalCount: s.CountA, ProfileCount: s.CountB, Reason: s.Reason}
		if !originalFirst {
			score.Original, score.Profile = score.Profile, score.Original
			score.OriginalCount, score.ProfileCount = score.ProfileCount, score.OriginalCount
		}
		v.Scores = append(v.Scores, score)
	}
	u := out.Result.Usage
	v.JudgeUSD, _ = usage.Cost(cfg, target.Agent, target.Model, u.Input, u.Cached, u.Output)
	if err := j.Append("verdicts.ndjson", v); err != nil {
		return journal.Verdict{}, err
	}
	return v, nil
}
