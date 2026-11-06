package calibration

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
)

// A small project has fewer tracked files or commits than these; it gets
// at most smallClasses classes and no round 2.
const (
	smallFiles   = 200
	smallCommits = 100
	smallClasses = 3
)

// largeShare is the share of candidates above MaxLines from which the
// project's work counts as large and the large stratum is on.
const largeShare = 0.25

// ManifestFile and TasksDir are kept in a run's folder.
const (
	ManifestFile = "manifest.json"
	TasksDir     = "tasks"
)

type MineInput struct {
	Root, StateDir string
	Cfg            *config.Config
	Survey         Survey
	Candidates     []Candidate
	Means          ClassMeans
	Seed           int64
	Now            time.Time
	Validate       func(ctx context.Context, c Candidate) (verification, reason string, err error)
}

// Mine estimates every candidate that passed the filters, draws a
// stratified sample per class with the seed, and gives every candidate it
// does not keep a reason. It makes no model call; Validate runs the checks.
func Mine(ctx context.Context, in MineInput) (Manifest, error) {
	if in.Cfg == nil || in.Validate == nil {
		return Manifest{}, errors.New("calibration mine: no configuration or validation")
	}
	cfg := in.Cfg.Calibration
	m := Manifest{
		Seed: in.Seed, Reserve: map[string][]string{}, Counts: map[string]ClassCount{},
		Estimates: map[string]Estimate{}, Verification: map[string]string{},
		RepoFiles: in.Survey.Files, HistoryDays: in.Survey.HistoryDays, CheckSeconds: in.Survey.CheckSeconds,
	}
	candidates := slices.Clone(in.Candidates)
	slices.SortStableFunc(candidates, func(a, b Candidate) int { return strings.Compare(a.ID, b.ID) })
	if largeStratum(candidates, cfg, in.Now) {
		for i := range candidates {
			if sizeReason(candidates[i].Excluded) {
				Filter(&candidates[i], cfg.Filter, true)
				Classify(&candidates[i], in.Cfg)
			}
		}
	}

	reasons := map[string]string{}
	estimates := map[string]Estimate{}
	byClass := map[string][]*Candidate{}
	for i := range candidates {
		c := &candidates[i]
		if c.Excluded != "" {
			reasons[c.ID] = c.Excluded
			continue
		}
		if c.Class == "" {
			reasons[c.ID] = "no class"
			continue
		}
		e := EstimateTask(*c, in.Means, in.Survey.CheckSeconds, cfg)
		if overCap(e, cfg) {
			reasons[c.ID] = "estimate above the cell cap"
			continue
		}
		estimates[c.ID] = e
		byClass[c.Class] = append(byClass[c.Class], c)
	}

	classes := slices.Sorted(maps.Keys(in.Cfg.Learning.Classes))
	for class := range byClass {
		if !slices.Contains(classes, class) {
			classes = append(classes, class)
		}
	}
	slices.Sort(classes)
	small, round2 := smallProject(in.Survey), round2Tasks(in.Survey)
	s := &sampler{ctx: ctx, seed: in.Seed, now: in.Now, validate: in.Validate, estimates: estimates, results: map[string]validation{},
		budget: ValidationBudget(in.Survey), spent: map[string]int{}, unchecked: map[string]bool{}}
	draws := map[string]draw{}
	for _, class := range classes {
		d, window, err := s.widen(class, byClass[class], cfg.HistoryDays, round2)
		if err != nil {
			return Manifest{}, err
		}
		count := ClassCount{Candidates: len(byClass[class]), Valid: d.valid, WindowDays: window}
		// Only round 1 can clear the three-task rule; the reserve replaces
		// a task, it does not add one.
		if len(d.round1) < minClassTasks {
			count.Skipped = fewerTasks
		}
		draws[class], m.Counts[class] = d, count
	}
	if small {
		var kept []string
		for _, class := range classes {
			if m.Counts[class].Skipped == "" {
				kept = append(kept, class)
			}
		}
		slices.SortStableFunc(kept, func(a, b string) int { return cmp.Compare(m.Counts[b].Valid, m.Counts[a].Valid) })
		for _, class := range kept[min(len(kept), smallClasses):] {
			count := m.Counts[class]
			count.Skipped = "small project, at most 3 classes"
			m.Counts[class] = count
		}
	}

	keep := func(c *Candidate) {
		m.Estimates[c.ID] = estimates[c.ID]
		m.Verification[c.ID] = s.results[c.ID].verification
	}
	var round2Tasks []ManifestTask
	for _, class := range classes {
		count, d := m.Counts[class], draws[class]
		if count.Skipped != "" {
			for _, c := range byClass[class] {
				reasons[c.ID] = "class skipped: " + count.Skipped
			}
		} else {
			for _, c := range d.round1 {
				m.Tasks = append(m.Tasks, manifestTask(*c, s.results[c.ID].verification))
				keep(c)
			}
			for _, c := range d.round2 {
				round2Tasks = append(round2Tasks, manifestTask(*c, s.results[c.ID].verification))
				m.Round2 = append(m.Round2, c.ID)
				keep(c)
			}
			for _, c := range d.reserve {
				m.Reserve[class] = append(m.Reserve[class], c.ID)
				keep(c)
			}
			count.Selected, count.Reserve = len(d.round1)+len(d.round2), len(d.reserve)
			m.Counts[class] = count
			for _, c := range byClass[class] {
				if _, kept := m.Verification[c.ID]; !kept {
					reasons[c.ID] = "not sampled"
				}
			}
		}
		// A validation failure, or the budget that stopped validation, is
		// the better reason, whatever the class.
		for _, c := range byClass[class] {
			if v, ok := s.results[c.ID]; ok && v.verification == "" {
				reasons[c.ID] = v.reason
			} else if !ok && s.unchecked[c.ID] {
				reasons[c.ID] = budgetReached
			}
		}
	}
	m.Tasks = append(m.Tasks, round2Tasks...)
	byID := map[string]*Candidate{}
	for i := range candidates {
		byID[candidates[i].ID] = &candidates[i]
	}
	for _, task := range m.Tasks {
		if byID[task.ID].NeedsRewrite {
			m.Rewrite = append(m.Rewrite, task.ID)
		}
	}
	for _, c := range in.Candidates {
		if reason, ok := reasons[c.ID]; ok {
			m.Excluded = append(m.Excluded, Exclusion{ID: c.ID, Reason: reason})
		}
	}
	return m, nil
}

// largeStratum reports whether the project's work is large: at least a
// quarter of the candidates of the first window that pass every filter but
// the size bounds change more than MaxLines. Without candidates in that
// window, all count.
func largeStratum(candidates []Candidate, cfg config.Calibration, now time.Time) bool {
	var window []Candidate
	for _, c := range candidates {
		if int(now.Sub(c.Time).Hours()/24) <= cfg.HistoryDays {
			window = append(window, c)
		}
	}
	if len(window) == 0 {
		window = candidates
	}
	eligible, over := 0, 0
	for _, c := range window {
		if c.Excluded != "" && !sizeReason(c.Excluded) {
			continue
		}
		// Filter stops at the size, before the class check.
		if sizeReason(c.Excluded) && slices.Contains(specClasses, c.Class) && !specSource(c.StatementSource) {
			continue
		}
		eligible++
		if c.Lines > cfg.Filter.MaxLines {
			over++
		}
	}
	return eligible > 0 && float64(over) >= largeShare*float64(eligible)
}

func sizeReason(reason string) bool {
	return strings.HasPrefix(reason, "files outside ") || strings.HasPrefix(reason, "lines outside ")
}

func manifestTask(c Candidate, verification string) ManifestTask {
	return ManifestTask{
		ID: c.ID, Statement: c.Statement, StatementSource: c.StatementSource, Class: c.Class,
		Verification: verification, Sources: slices.Clone(c.Sources), Hidden: slices.Clone(c.Hidden),
		Features: maps.Clone(c.Features),
	}
}

// TaskRecord is task.json: what cells, rewrite and judge need beyond the
// shared ManifestTask. It never goes into the project part.
type TaskRecord struct {
	ManifestTask
	Parent       string   `json:"parent"`
	Ref          string   `json:"ref"`
	Subject      string   `json:"subject"`
	Commits      []string `json:"commits"`
	HiddenNames  []string `json:"hidden_names"`
	NeedsRewrite bool     `json:"needs_rewrite"`
}

// SaveManifest replaces manifest.json in runDir through a temporary file.
func SaveManifest(runDir string, m Manifest) error {
	return writeJSON(filepath.Join(runDir, ManifestFile), m)
}

func LoadManifest(runDir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(runDir, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("calibration %s: %w", ManifestFile, err)
	}
	return m, nil
}

// WriteTaskFiles writes runDir/tasks/<id>/ for c: the statement, the
// reference change without its hidden tests, the hidden tests, and
// task.json. None is Markdown, which a candidate's gate would let it read.
// The verification comes from the manifest, so save that first.
func WriteTaskFiles(ctx context.Context, runDir, root string, c Candidate) error {
	if !validID(c.ID) {
		return fmt.Errorf("calibration task %q: not a task id", c.ID)
	}
	m, err := LoadManifest(runDir)
	if err != nil {
		return err
	}
	verification, ok := m.Verification[c.ID]
	if !ok {
		for _, task := range m.Tasks {
			if task.ID == c.ID {
				verification, ok = task.Verification, true
			}
		}
	}
	if !ok {
		return fmt.Errorf("calibration task %s: not in the manifest", c.ID)
	}
	diff := []string{"-c", "core.quotePath=false", "diff", "--binary", "--no-renames", "--no-ext-diff", "--no-textconv",
		"--no-color", "--src-prefix=a/", "--dst-prefix=b/", c.Parent, c.Ref, "--"}
	reference := slices.Clone(diff)
	for _, name := range c.Hidden {
		reference = append(reference, ":(exclude,literal)"+name)
	}
	referencePatch, err := run(ctx, root, reference...)
	if err != nil {
		return err
	}
	var hiddenPatch []byte
	if len(c.Hidden) > 0 {
		hidden := slices.Clone(diff)
		for _, name := range c.Hidden {
			hidden = append(hidden, ":(literal)"+name)
		}
		if hiddenPatch, err = run(ctx, root, hidden...); err != nil {
			return err
		}
	}
	statement := strings.TrimSpace(c.Statement)
	if statement != "" {
		statement += "\n"
	}
	dir := filepath.Join(runDir, TasksDir, c.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for name, data := range map[string][]byte{"statement.txt": []byte(statement), "reference.patch": referencePatch, "hidden.patch": hiddenPatch} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	record := TaskRecord{ManifestTask: manifestTask(c, verification), Parent: c.Parent, Ref: c.Ref, Subject: c.Subject,
		Commits: slices.Clone(c.Commits), HiddenNames: slices.Clone(c.HiddenNames), NeedsRewrite: c.NeedsRewrite}
	return writeJSON(filepath.Join(dir, "task.json"), record)
}

func ReadTask(runDir, id string) (TaskRecord, error) {
	if !validID(id) {
		return TaskRecord{}, fmt.Errorf("calibration task %q: not a task id", id)
	}
	data, err := os.ReadFile(filepath.Join(runDir, TasksDir, id, "task.json"))
	if err != nil {
		return TaskRecord{}, err
	}
	var r TaskRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return TaskRecord{}, fmt.Errorf("calibration task %s: %w", id, err)
	}
	return r, nil
}

func validID(id string) bool { return id != "" && id == filepath.Base(id) && id != "." && id != ".." }

// writeJSON replaces target through a temporary file, readable by the
// person only.
func writeJSON(target string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmp.Name(), 0600); err != nil {
		return err
	}
	return store.Rename(tmp.Name(), target)
}
