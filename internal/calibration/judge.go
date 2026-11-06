package calibration

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/delegate"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
	"github.com/lorem-dev/duolog/internal/profile"
	"github.com/lorem-dev/duolog/internal/usage"
)

// JudgeDir is kept in a run's folder; each verdict writes JudgeDir/<id>/.
const JudgeDir = "judge"

// closeMeans is the largest gap between the score means of A and B at which
// a pair is close and is judged again with the order swapped; disputeGap is
// the gap between two judges on one criterion and side that disputes it.
const (
	closeMeans = 0.5
	disputeGap = 2
)

// judgeOrder returns true when the pair's A is shown first; tests replace it.
var judgeOrder = func() bool { return rand.IntN(2) == 0 }

// judgeInputs are the files a judge reads, in the order its task lists them.
var judgeInputs = []struct{ name, what string }{
	{"statement.txt", "the task both results were given"},
	{"a.patch", "result A"},
	{"b.patch", "result B"},
	{"reference.patch", "how the project solved the task"},
	{"checks.txt", "the checks each result ran and their exit codes"},
}

// Pair is two entries whose cells both passed one task; A sorts before B by
// entry key.
type Pair struct {
	Task  string `json:"task"`
	Class string `json:"class"`
	A     string `json:"a"`
	B     string `json:"b"`
}

// Pairs lists every pair of entries whose done cells passed the same task,
// by class, task and entries.
func Pairs(run *Run) []Pair {
	type key struct{ task, class string }
	passed := map[key][]string{}
	for _, c := range run.Cells {
		if c.State != CellDone || !c.Passed {
			continue
		}
		k := key{c.Task, c.Class}
		if !slices.Contains(passed[k], c.Entry) {
			passed[k] = append(passed[k], c.Entry)
		}
	}
	var pairs []Pair
	for k, entries := range passed {
		slices.Sort(entries)
		for i, a := range entries {
			for _, b := range entries[i+1:] {
				pairs = append(pairs, Pair{Task: k.task, Class: k.class, A: a, B: b})
			}
		}
	}
	slices.SortFunc(pairs, func(x, y Pair) int {
		return cmp.Or(cmp.Compare(x.Class, y.Class), cmp.Compare(x.Task, y.Task), cmp.Compare(x.A, y.A), cmp.Compare(x.B, y.B))
	})
	return pairs
}

// JudgeTargets gives a pair of different agents the first judge of each, and
// a pair of one agent the first judge of another agent. Without such a judge
// it falls back to learning.JudgeTarget.
func JudgeTargets(l config.Learning, a, b config.Target) []config.Target {
	var out []config.Target
	seen := map[string]bool{}
	for _, e := range l.Judges {
		switch {
		case a.Agent == b.Agent && e.Agent != a.Agent:
			return []config.Target{e.Target()}
		case a.Agent != b.Agent && (e.Agent == a.Agent || e.Agent == b.Agent) && !seen[e.Agent]:
			seen[e.Agent] = true
			out = append(out, e.Target())
		}
	}
	if len(out) == 0 {
		if t := learning.JudgeTarget(l, a, b); t.Agent != "" {
			out = append(out, t)
		}
	}
	return out
}

// JudgeTask builds the judge's task and its five neutral files: the
// statement, the two patches, the reference patch and the check results.
// No file holds an entry name or a candidate's summary.
func JudgeTask(c config.Class, statement, reference []byte, checks string, first, second []byte) (task []byte, files map[string][]byte) {
	files = map[string][]byte{
		"statement.txt":   statement,
		"a.patch":         first,
		"b.patch":         second,
		"reference.patch": reference,
		"checks.txt":      []byte(checks),
	}
	var text strings.Builder
	text.WriteString("# Task: compare two results\n\n## Goal\nCompare results A and B of the same task using only this rubric:\n")
	for _, criterion := range c.Rubric {
		fmt.Fprintf(&text, "- %s\n", criterion)
	}
	text.WriteString("\nThe reference patch is how the project solved it. It is evidence, not a side. Score A and B only.\n")
	text.WriteString("\n## Read\nThese files are in the work root:\n")
	for _, input := range judgeInputs {
		fmt.Fprintf(&text, "- `%s`: %s\n", input.name, input.what)
	}
	text.WriteString("\n## Write\nNone.\n\n## Limits\nScore every criterion from 0 to 3 for A and B: 3 is best, 0 is worst. For (count) criteria, fewer is better; the score measures quality, not the count. Give nonnegative counts for criteria marked (count). Give one reason per criterion. Do not guess which model wrote either side. Read only the listed files and do not search for the identity or history of either side.\n\n## Check\nYour summary must contain one JSON object with this shape:\n")
	text.WriteString("```json\n{\"scores\":[{\"criterion\":\"rubric criterion\",\"a\":0,\"b\":0,\"count_a\":0,\"count_b\":0,\"reason\":\"one reason\"}]}\n```\n")
	return []byte(text.String()), files
}

// Call is one judge's scores in the labels it saw; AFirst says the pair's A
// was shown as A.
type Call struct {
	Judge  string           `json:"judge"`
	AFirst bool             `json:"a_first"`
	Scores []profile.Scored `json:"scores"`
}

// pairSide is one criterion of a call in the pair's terms.
type pairSide struct {
	a, b           int
	countA, countB *int
	reason         string
}

func (c Call) side(criterion string) (pairSide, bool) {
	i := slices.IndexFunc(c.Scores, func(s profile.Scored) bool { return s.Criterion == criterion })
	if i < 0 {
		return pairSide{}, false
	}
	s := c.Scores[i]
	if c.AFirst {
		return pairSide{s.A, s.B, s.CountA, s.CountB, s.Reason}, true
	}
	return pairSide{s.B, s.A, s.CountB, s.CountA, s.Reason}, true
}

// means are the call's scores for A and B, each averaged over the rubric.
func (c Call) means(rubric []string) (a, b float64, ok bool) {
	n := 0
	for _, criterion := range rubric {
		if s, found := c.side(criterion); found {
			a, b, n = a+float64(s.a), b+float64(s.b), n+1
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	return a / float64(n), b / float64(n), true
}

// Combine averages each criterion over the calls in the pair's terms and
// rounds it. The pair is disputed when two judges differ by disputeGap or
// more on a criterion for one side, or when one judge's winner reverses
// with the order.
func Combine(calls []Call, rubric []string) (scores []journal.Score, disputed bool) {
	for _, criterion := range rubric {
		var a, b, countA, countB float64
		var n, counts int
		var reasons []string
		for _, call := range calls {
			s, ok := call.side(criterion)
			if !ok {
				continue
			}
			a, b, n = a+float64(s.a), b+float64(s.b), n+1
			if s.countA != nil && s.countB != nil {
				countA, countB, counts = countA+float64(*s.countA), countB+float64(*s.countB), counts+1
			}
			if reason := strings.TrimSpace(s.reason); reason != "" {
				label := call.Judge
				if !call.AFirst {
					label += ", B shown as A"
				}
				reasons = append(reasons, label+": "+reason)
			}
		}
		if n == 0 {
			continue
		}
		score := journal.Score{Criterion: criterion, Original: rounded(a, n), Profile: rounded(b, n), Reason: strings.Join(reasons, "; ")}
		if counts == n {
			ca, cb := rounded(countA, n), rounded(countB, n)
			score.OriginalCount, score.ProfileCount = &ca, &cb
		}
		scores = append(scores, score)
	}
	for i, x := range calls {
		for _, y := range calls[i+1:] {
			if x.Judge != y.Judge && gap(x, y, rubric) || x.Judge == y.Judge && x.AFirst != y.AFirst && reversed(x, y, rubric) {
				disputed = true
			}
		}
	}
	return scores, disputed
}

func rounded(sum float64, n int) int { return int(math.Round(sum / float64(n))) }

// gap reports a criterion on which x and y differ by disputeGap or more for
// one side.
func gap(x, y Call, rubric []string) bool {
	for _, criterion := range rubric {
		sx, okx := x.side(criterion)
		sy, oky := y.side(criterion)
		if okx && oky && (abs(sx.a-sy.a) >= disputeGap || abs(sx.b-sy.b) >= disputeGap) {
			return true
		}
	}
	return false
}

// reversed reports that x and y name different winners; a tie names none.
func reversed(x, y Call, rubric []string) bool {
	xa, xb, okx := x.means(rubric)
	ya, yb, oky := y.means(rubric)
	return okx && oky && (xa > xb && ya < yb || xa < xb && ya > yb)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// isClose reports that the means of A and B over all calls differ by at most
// closeMeans.
func isClose(calls []Call, rubric []string) bool {
	var a, b float64
	n := 0
	for _, call := range calls {
		if ca, cb, ok := call.means(rubric); ok {
			a, b, n = a+ca, b+cb, n+1
		}
	}
	return n > 0 && math.Abs(a-b)/float64(n) <= closeMeans
}

// JudgeEnv is what judging a run needs: the cells' environment, the journal
// the verdicts go to and Paid, which runs each paid call of a judge target
// under admission and reports false when it refused the call; call returns
// the tokens it used. Runner.Judge sets it, and nil runs every call.
type JudgeEnv struct {
	CellEnv
	Journal *journal.Journal
	Paid    func(ctx context.Context, target config.Target, call func() (int64, error)) (bool, error)
}

func (env JudgeEnv) paid(ctx context.Context, target config.Target, call func() (int64, error)) (bool, error) {
	if env.Paid == nil {
		_, err := call()
		return true, err
	}
	return env.Paid(ctx, target, call)
}

// pairFile keeps a pair's calls in its verdict folder until the verdict is
// journalled, so a pair stopped between calls goes on from them.
const pairFile = "pair.json"

// pairState is a pair's schedule and the calls made so far: the judges in
// order and the rubric's hash, frozen when the pair starts, and the order of
// its first calls. A pair whose schedule no longer matches is Dropped and
// judged again from the start; Note says so on the new one.
type pairState struct {
	Pair    Pair     `json:"pair"`
	Judges  []string `json:"judges"`
	Rubric  string   `json:"rubric"`
	AFirst  bool     `json:"a_first"`
	Calls   []Call   `json:"calls,omitempty"`
	USD     float64  `json:"usd,omitempty"`
	Done    bool     `json:"done,omitempty"`
	Dropped bool     `json:"dropped,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// restartNote is the note of a pair judged again from the start.
const restartNote = "judged again from the start: the judges or the rubric changed after its first calls"

// openPair returns the verdict id and folder under root of p's unfinished
// calls on the schedule of judges and rubric, or of a new verdict with the
// order of its first calls drawn. Unfinished calls on another schedule are
// dropped.
func openPair(root string, p Pair, judges []string, rubric string) (id, dir string, st pairState, err error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", "", st, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", "", st, err
	}
	note := ""
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(root, e.Name(), pairFile))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", "", st, err
		}
		var saved pairState
		if err := json.Unmarshal(data, &saved); err != nil {
			return "", "", st, fmt.Errorf("calibration judge %s: %s: %w", e.Name(), pairFile, err)
		}
		if saved.Pair != p || saved.Done || saved.Dropped {
			continue
		}
		if slices.Equal(saved.Judges, judges) && saved.Rubric == rubric {
			return e.Name(), filepath.Join(root, e.Name()), saved, nil
		}
		saved.Dropped = true
		if err := writeJSON(filepath.Join(root, e.Name(), pairFile), saved); err != nil {
			return "", "", st, err
		}
		if len(saved.Calls) > 0 {
			note = restartNote
		}
	}
	for {
		id = journal.NewID("v", time.Now())
		dir = filepath.Join(root, id)
		if err := os.Mkdir(dir, 0700); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", "", st, err
		}
		break
	}
	st = pairState{Pair: p, Judges: judges, Rubric: rubric, AFirst: judgeOrder(), Note: note}
	return id, dir, st, writeJSON(filepath.Join(dir, pairFile), st)
}

// judgedSide is one entry's result on the pair's task.
type judgedSide struct {
	patch  []byte
	checks []journal.Check
}

// JudgePair judges p on t blind: each judge of JudgeTargets scores the two
// patches in a random order with the reference as evidence, and a close or
// disputed pair is judged again by the same judges with the order swapped.
// All calls make one verdict, appended to the journal for the person to
// confirm. Each call runs in the judge slot, which then holds only its
// inputs; RunDir/judge/<verdict id>/ keeps the task, pair.json with the
// schedule and the calls made so far and, per call, copies of the inputs and
// answer.json. A call env.Paid refuses returns ErrNotAdmitted, and judging p
// again goes on from the calls saved, or from the start with a note when the
// judges or the rubric changed since.
func JudgePair(ctx context.Context, env JudgeEnv, run *Run, p Pair, t TaskRecord) (journal.Verdict, error) {
	var v journal.Verdict
	if env.Cfg == nil || env.Delegate == nil || env.Journal == nil {
		return v, errors.New("calibration judge: no configuration, delegate or journal")
	}
	if t.ID != p.Task {
		return v, fmt.Errorf("calibration judge: task %s is not the pair's task %s", t.ID, p.Task)
	}
	c, ok := env.Cfg.Learning.Classes[p.Class]
	if !ok || len(c.Rubric) == 0 {
		return v, fmt.Errorf("class %q has no judge rubric", p.Class)
	}
	from, err := config.ParseEntryKey(p.A)
	if err != nil {
		return v, err
	}
	to, err := config.ParseEntryKey(p.B)
	if err != nil {
		return v, err
	}
	targets := JudgeTargets(env.Cfg.Learning, from.Target(), to.Target())
	if len(targets) == 0 {
		return v, errors.New("no judge target configured")
	}
	a, err := readSide(env.RunDir, run, p.Task, p.A)
	if err != nil {
		return v, err
	}
	b, err := readSide(env.RunDir, run, p.Task, p.B)
	if err != nil {
		return v, err
	}
	reference, err := os.ReadFile(filepath.Join(env.RunDir, TasksDir, t.ID, "reference.patch"))
	if err != nil {
		return v, err
	}
	statement := []byte(strings.TrimSpace(t.Statement) + "\n")

	var judges []string
	for _, target := range targets {
		judges = append(judges, targetKey(target))
	}
	id, dir, st, err := openPair(filepath.Join(env.RunDir, JudgeDir), p, judges, hashOf(c.Rubric))
	if err != nil {
		return v, err
	}
	// The calls come in a fixed sequence, so the ones saved are its start.
	n := 0
	judgeAll := func(aFirst bool) error {
		for _, target := range targets {
			if n++; n <= len(st.Calls) {
				continue
			}
			call, cost, err := judgeOnce(ctx, env, c, dir, n, target, aFirst, statement, reference, a, b)
			if err != nil {
				return err
			}
			st.Calls, st.USD = append(st.Calls, call), st.USD+cost
			if err := writeJSON(filepath.Join(dir, pairFile), st); err != nil {
				return err
			}
		}
		return nil
	}
	if err := judgeAll(st.AFirst); err != nil {
		return v, err
	}
	if _, disputed := Combine(st.Calls, c.Rubric); disputed || isClose(st.Calls, c.Rubric) {
		if err := judgeAll(!st.AFirst); err != nil {
			return v, err
		}
	}
	calls, usd := st.Calls, st.USD
	scores, disputed := Combine(calls, c.Rubric)
	ladder, _ := env.Cfg.Learning.Ladder(p.Class, cellRole(env.Cfg, t))
	v = journal.Verdict{Kind: "verdict", Time: time.Now().UTC(), ID: id, Source: "calibration", Run: run.ID, Task: p.Task,
		Class: p.Class, Ladder: ladder, Verification: t.Verification, Original: p.A, Profile: p.B, Judge: strings.Join(judges, ","),
		Scores: scores, OriginalChecks: a.checks, ProfileChecks: b.checks, JudgeUSD: usd, Disputed: disputed, Note: st.Note}
	if err := env.Journal.Append("verdicts.ndjson", v); err != nil {
		return journal.Verdict{}, err
	}
	st.Done = true
	return v, writeJSON(filepath.Join(dir, pairFile), st)
}

// readSide reads the patch and checks of entry's done cell on task.
func readSide(runDir string, run *Run, task, entry string) (judgedSide, error) {
	i := slices.IndexFunc(run.Cells, func(c Cell) bool { return c.Task == task && c.Entry == entry && c.State == CellDone })
	if i < 0 {
		return judgedSide{}, fmt.Errorf("calibration judge: no done cell of %s on task %s", entry, task)
	}
	dir := filepath.Join(runDir, CellsDir, run.Cells[i].ID)
	patch, err := os.ReadFile(filepath.Join(dir, "result.patch"))
	if err != nil {
		return judgedSide{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "checks.json"))
	if err != nil {
		return judgedSide{}, err
	}
	var checks cellChecks
	if err := json.Unmarshal(data, &checks); err != nil {
		return judgedSide{}, fmt.Errorf("calibration cell %s: checks.json: %w", run.Cells[i].ID, err)
	}
	return judgedSide{patch: patch, checks: slices.Concat(checks.Hidden, checks.Class)}, nil
}

// checksText lists each side's checks with their exit codes.
func checksText(first, second []journal.Check) string {
	var b strings.Builder
	for _, side := range []struct {
		label  string
		checks []journal.Check
	}{{"A", first}, {"B", second}} {
		fmt.Fprintf(&b, "%s:\n", side.label)
		if len(side.checks) == 0 {
			b.WriteString("  no checks\n")
		}
		for _, check := range side.checks {
			fmt.Fprintf(&b, "  %s: exit %d\n", check.Command, check.Exit)
		}
	}
	return b.String()
}

// judgeOnce runs one judge call n in the judge slot holding its inputs and
// keeps copies of them and its answer in dir/<n>/. A slot Codex does not
// trust refuses the call before admission, so it costs nothing.
func judgeOnce(ctx context.Context, env JudgeEnv, c config.Class, dir string, n int, target config.Target, aFirst bool,
	statement, reference []byte, a, b judgedSide) (_ Call, _ float64, err error) {
	first, second := a, b
	if !aFirst {
		first, second = b, a
	}
	task, files := JudgeTask(c, statement, reference, checksText(first.checks, second.checks), first.patch, second.patch)
	taskPath := filepath.Join(dir, "task.md")
	if err := os.WriteFile(taskPath, task, 0600); err != nil {
		return Call{}, 0, err
	}
	callDir := filepath.Join(dir, strconv.Itoa(n))
	if err := os.MkdirAll(callDir, 0700); err != nil {
		return Call{}, 0, err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(callDir, name), data, 0600); err != nil {
			return Call{}, 0, err
		}
	}
	slot, err := JudgeSlot(env.Root)
	if err != nil {
		return Call{}, 0, err
	}
	claudeEnv, err := WorkerEnviron(env.Cfg, "claude")
	if err != nil {
		return Call{}, 0, err
	}
	workerEnv, err := WorkerEnviron(env.Cfg, target.Agent)
	if err != nil {
		return Call{}, 0, err
	}
	cleanup, err := NewJudgeSlot(ctx, env.Root, slot, files, claudeEnv)
	if err != nil {
		return Call{}, 0, err
	}
	defer func() { err = errors.Join(err, cleanup()) }()
	if err := CheckTreeTrust(target.Agent, slot, workerEnv); err != nil {
		return Call{}, 0, fmt.Errorf("judge: %w", err)
	}
	// Calibration keeps the task copy in the slot's own state, the only
	// place task.Inline reads it from outside the work root.
	var out delegate.Outcome
	admitted, err := env.paid(ctx, target, func() (int64, error) {
		// Built once the call is admitted, with what the plan still allows it.
		budget, waiting := StepOptions(ctx)
		ctxRun := delegate.WithOptions(ctx, delegate.Options{Judge: true, Calibration: true, Entry: &target, WorkRoot: slot, Lead: env.Lead,
			ProtectedRoots: env.ProtectedRoots, Budget: budget, Waiting: waiting})
		var runErr error
		out, runErr = env.Delegate(ctxRun, env.Cfg, env.Store, "review_final", taskPath, "")
		return out.Result.Usage.Input + out.Result.Usage.Output, runErr
	})
	if !admitted && err == nil {
		return Call{}, 0, ErrNotAdmitted
	}
	if err != nil {
		return Call{}, 0, fmt.Errorf("judge: %w", err)
	}
	answer := struct {
		Done      bool     `json:"done"`
		Summary   string   `json:"summary"`
		Files     []string `json:"files"`
		Needs     []string `json:"needs"`
		Decisions []string `json:"decisions,omitempty"`
	}{out.Result.Done, out.Result.Summary, append([]string{}, out.Result.Files...), append([]string{}, out.Result.Needs...), out.Result.Decisions}
	answerPath := filepath.Join(callDir, "answer.json")
	if err := writeJSON(answerPath, answer); err != nil {
		return Call{}, 0, err
	}
	if err := ctx.Err(); err != nil {
		return Call{}, 0, err
	}
	if !out.Result.Done || out.Result.Interrupted || out.RunHere != nil || len(out.Needs) > 0 {
		return Call{}, 0, fmt.Errorf("judge did not finish; answer: %s", answerPath)
	}
	scores, err := profile.ParseVerdict(out.Result.Summary, c.Rubric)
	if err != nil {
		return Call{}, 0, fmt.Errorf("judge: %w; answer: %s", err, answerPath)
	}
	usd, _ := usage.Cost(env.Cfg, target.Agent, target.Model, out.Result.Usage)
	return Call{Judge: targetKey(target), AFirst: aFirst, Scores: scores}, usd, nil
}

func targetKey(t config.Target) string { return t.Agent + "/" + t.Model + "/" + t.Effort }

// JudgeSlot is the folder judge calls and statement rewrites work in, beside
// the tree slots: <user cache dir>/duolog/calibration/<project id>/judge. It
// stays the same from run to run, so the person trusts it in Codex once.
func JudgeSlot(root string) (string, error) {
	tree, err := TreeSlot(root, 0)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(tree), "judge"), nil
}

// NewJudgeSlot readies dir for one judge call or rewrite as PrepareTree
// readies a tree slot: it empties dir but for the project's
// .codex/hooks.json, written afresh, and starts a fresh repository there,
// so the task copy stays in it and nothing a worker wrote into .git reaches
// the next call. It then gives Claude the duolog gate as NewTreeAt does,
// judged from environ, the environment a Claude worker gets, and writes
// files. dir must neither be inside repo nor hold it. The cleanup empties dir
// again but for the hooks.
func NewJudgeSlot(ctx context.Context, repo, dir string, files map[string][]byte, environ []string) (cleanup func() error, err error) {
	if inside(repo, dir) || inside(dir, repo) {
		return nil, fmt.Errorf("judge slot %s must be outside the project %s", dir, repo)
	}
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return nil, fmt.Errorf("judge slot %s is not a folder", dir)
	}
	hooks, err := projectHooks(ctx, repo)
	if err != nil {
		return nil, err
	}
	cleanup = func() error { return clearSlot(dir) }
	if err := cleanup(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if _, err := run(ctx, dir, "init", "-q"); err != nil {
		return nil, err
	}
	target := filepath.Join(dir, filepath.FromSlash(codexHooks))
	// Written afresh, so a worker's edit or a link in its place is gone.
	if err := os.RemoveAll(target); err != nil {
		return nil, err
	}
	if hooks == nil {
		// Only an empty folder goes.
		_ = os.Remove(filepath.Dir(target))
	} else if err = os.MkdirAll(filepath.Dir(target), 0o755); err == nil {
		err = os.WriteFile(target, hooks, 0o644)
	}
	if err != nil {
		return nil, err
	}
	if err := addClaudeGate(dir, environ); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return nil, errors.Join(err, cleanup())
		}
	}
	return cleanup, nil
}

// clearSlot empties a judge slot as clearTree empties a tree slot, its .git
// folder included. A link is never kept or followed.
func clearSlot(dir string) error {
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return os.Remove(dir)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := filepath.Join(dir, e.Name())
		switch {
		case e.Name() == ".codex" && e.IsDir():
			errs = append(errs, emptyDirKeeping(name, []string{"hooks.json"}))
		default:
			errs = append(errs, os.RemoveAll(name))
		}
	}
	return errors.Join(errs...)
}

// Judge judges each pair that judged does not hold, in order, under the
// run's admission: each call is admitted as a cell is, against the judge's
// quota, the lead reserve and the approved cap, and charged to the run's
// spend before it starts, so a failed or interrupted call counts once.
// Without a rate a call counts as JudgeCost of its class, and the first call
// that moves a reading gives the judge's agent a rate. A refusal saves its
// stop or pause and ends judging; the pair keeps the calls it made, so
// judging again makes only the rest. A quota refresh that fails after a call
// stops judging once the pair saved that call. It returns the verdicts of
// every pair judged, judged before or now.
func (r *Runner) Judge(ctx context.Context, env JudgeEnv, pairs []Pair, judged map[Pair]journal.Verdict) (Outcome, []journal.Verdict, error) {
	if r.Run == nil || r.Cfg == nil || r.Quota == nil {
		return Outcome{}, nil, errors.New("calibration judge: no run, configuration or quota")
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	if r.Out == nil {
		r.Out = io.Discard
	}
	rates, err := ReadRates(r.StateDir)
	if err != nil {
		return Outcome{}, nil, err
	}
	r.rates, r.judging = rates, true
	if pause := r.Run.Pause; !pause.IsZero() && r.Now().Before(pause) {
		_, err := fmt.Fprintf(r.Out, "paused until %s; judge again with duolog calibrate judge\n", pause.UTC().Format(time.RFC3339))
		return Outcome{Pause: pause}, nil, err
	}
	r.Run.Stopped, r.Run.Pause = "", time.Time{}
	var held Outcome
	var quotaAgent string
	var quotaErr error
	var cellTokens int64 // of the pair's class
	// Each call's budget and wait notice reach its delegation through ctx.
	ctx, step := withStep(ctx)
	env.Paid = func(ctx context.Context, target config.Target, call func() (int64, error)) (bool, error) {
		if quotaErr != nil {
			o, err := r.quotaStop(quotaAgent, quotaErr)
			held = o
			return false, err
		}
		agent, family := target.Agent, modelFamily(r.Cfg, target.Model)
		cost := JudgeCost(r.rate(agent), cellTokens)
		before, o, stop, err := r.admit(ctx, agent, family, cost, 0)
		if err != nil || stop {
			held = o
			return false, err
		}
		step.budget = r.stepBudget(agent, r.newReceipt("judge"), true)
		step.waiting = r.waiting(agent)
		r.Run.Spend.Raise(agent, before, cost)
		r.Run.Spend.addJudge(agent, cost.WeeklyPct)
		// The high USD is held in the same save, so an interrupted call
		// keeps it counted against the approved USD.
		heldUSD := r.holdUSD(agent, cellUSD(r.Cfg, agent, target.Model, judgeTokensHigh), true)
		if err := r.Run.Save(r.StateDir); err != nil {
			return true, err
		}
		tokens, callErr := call()
		if notStarted(callErr) {
			// Refused before its worker started: nothing ran to measure.
			r.chargeUSD(agent, -heldUSD, true)
			return true, errors.Join(callErr, r.release(agent, before, cost, true))
		}
		weekly, window, err := r.settle(ctx, agent, family, before, cost, true)
		r.settleUSD(agent, heldUSD, cellUSD(r.Cfg, agent, target.Model, tokens), true, callErr == nil)
		if err != nil {
			quotaAgent, quotaErr = agent, err
		} else if err := r.learnRate(agent, tokens, weekly, window); err != nil {
			callErr = errors.Join(callErr, err)
		} else if err := r.observe(ctx, agent, family, tokens, 0, 0); err != nil {
			callErr = errors.Join(callErr, err)
		}
		return true, errors.Join(callErr, r.Run.Save(r.StateDir))
	}
	var vs []journal.Verdict
	for _, p := range pairs {
		if v, ok := judged[p]; ok {
			vs = append(vs, v)
			continue
		}
		t, err := ReadTask(r.Run.Dir(r.StateDir), p.Task)
		if err != nil {
			return Outcome{}, vs, err
		}
		cellTokens = classTokens(r.Manifest, p.Class)
		v, err := JudgePair(ctx, env, r.Run, p, t)
		if errors.Is(err, ErrNotAdmitted) {
			return held, vs, nil
		}
		if err != nil {
			return Outcome{}, vs, err
		}
		vs = append(vs, v)
	}
	if quotaErr != nil {
		o, err := r.quotaStop(quotaAgent, quotaErr)
		return o, vs, err
	}
	return Outcome{Done: true}, vs, r.Run.Save(r.StateDir)
}
