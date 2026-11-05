package calibration

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/store"
	"go.yaml.in/yaml/v3"
)

const projectHeader = "# Shared calibration evidence for this project. Written by duolog calibrate propose --apply-project.\n"

// ProjectPart is the part of a calibration that goes to Git. It holds families
// and efforts, never model versions, and no person, cost, quota or time figure.
type ProjectPart struct {
	Version  int                      `yaml:"version"` // 1
	Survey   SurveyFacts              `yaml:"survey"`
	Tasks    []ManifestTask           `yaml:"tasks"`
	Evidence map[string][]EvidenceRow `yaml:"evidence"` // class -> rows
}

type SurveyFacts struct {
	Languages  []string `yaml:"languages,omitempty" json:"languages,omitempty"`
	Build      []string `yaml:"build,omitempty" json:"build,omitempty"`
	Test       []string `yaml:"test,omitempty" json:"test,omitempty"`
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

type ManifestTask struct {
	ID              string            `yaml:"id" json:"id"`
	Statement       string            `yaml:"statement" json:"statement"`
	StatementSource string            `yaml:"statement_source" json:"statement_source"`
	Class           string            `yaml:"class" json:"class"`
	Verification    string            `yaml:"verification" json:"verification"`
	Sources         []string          `yaml:"sources,omitempty" json:"sources,omitempty"` // "commit:<sha>", "plan:<path>#Task 3", "pr:12", "adr:<path>"
	Hidden          []string          `yaml:"hidden,omitempty" json:"hidden,omitempty"`   // hidden test files
	Features        map[string]string `yaml:"features,omitempty" json:"features,omitempty"`
}

// EvidenceRow is one entry's calibration evidence on a ladder. Judged tasks
// verified by the judge alone count at half weight, so JudgedQuality is the
// mean over the judged tasks at those weights, and JudgeOnly says how many
// of them had half weight; a teammate's seed then weighs them as the person
// did. Evidence counts the tasks with evidence, which the three-task rule
// counts: a failed check, a decided verdict or a pass no other entry matched
// on a task where another entry has a result.
type EvidenceRow struct {
	Agent         string   `yaml:"agent"`
	Family        string   `yaml:"family"`
	Effort        string   `yaml:"effort"`
	Tasks         int      `yaml:"tasks"`
	TasksPassed   int      `yaml:"tasks_passed"`
	Judged        int      `yaml:"judged"`
	JudgeOnly     int      `yaml:"judge_only,omitempty"`
	JudgedQuality *float64 `yaml:"judged_quality,omitempty"`
	Evidence      int      `yaml:"evidence"`
	Date          string   `yaml:"date"` // "2006-01-02" UTC
	DuologVersion string   `yaml:"duolog_version"`
}

func projectPath(stateDir string) string { return filepath.Join(Dir(stateDir), ProjectFile) }

// ReadProject reads and validates the shared part; a missing file is not an error.
func ReadProject(stateDir string) (ProjectPart, bool, error) {
	path := projectPath(stateDir)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ProjectPart{}, false, nil
	}
	if err != nil {
		return ProjectPart{}, false, err
	}
	var p ProjectPart
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil {
		return ProjectPart{}, false, fmt.Errorf("%s: %w", path, err)
	}
	if err := ValidateProject(p); err != nil {
		return ProjectPart{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return p, true, nil
}

// RenderProject sorts classes and their rows so the same evidence always gives
// the same bytes, and a diff shows only real changes.
func RenderProject(p ProjectPart) ([]byte, error) {
	out := p
	out.Evidence = make(map[string][]EvidenceRow, len(p.Evidence))
	for _, class := range slices.Sorted(maps.Keys(p.Evidence)) {
		rows := slices.Clone(p.Evidence[class])
		slices.SortStableFunc(rows, func(a, b EvidenceRow) int {
			return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Family, b.Family), cmp.Compare(a.Effort, b.Effort))
		})
		out.Evidence[class] = rows
	}
	var buf bytes.Buffer
	buf.WriteString(projectHeader)
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&out); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ValidateProject checks the shape of p and every free-text field against
// the privacy policy, with names, the history's people. A field the policy
// would change is refused, never cleaned.
func ValidateProject(p ProjectPart, names ...string) error {
	if p.Version != 1 {
		return fmt.Errorf("calibration project part: version %d, want 1", p.Version)
	}
	// The statements are checked for every name. The other fields are
	// checked for names of two or more words only: a one-word name such as
	// "ci" or "test" is also an ordinary word of a command, path or
	// component.
	people, facts := newPrivacy(names), newPrivacy(multiWord(names))
	survey := map[string][]string{
		"languages": p.Survey.Languages, "build": p.Survey.Build, "test": p.Survey.Test, "components": p.Survey.Components,
	}
	for _, field := range slices.Sorted(maps.Keys(survey)) {
		for _, value := range survey[field] {
			if err := facts.check(value); err != nil {
				return fmt.Errorf("calibration survey %s %q: %w", field, value, err)
			}
		}
	}
	ids := map[string]bool{}
	for i, task := range p.Tasks {
		if task.ID == "" {
			return fmt.Errorf("calibration task %d: no id", i+1)
		}
		if err := facts.check(task.ID); err != nil {
			return fmt.Errorf("calibration task %d: id %w", i+1, err)
		}
		if ids[task.ID] {
			return fmt.Errorf("calibration task %s: listed twice", task.ID)
		}
		ids[task.ID] = true
		if task.Class == "" {
			return fmt.Errorf("calibration task %s: no class", task.ID)
		}
		if err := people.check(task.Statement); err != nil {
			return fmt.Errorf("calibration task %s: the statement %w", task.ID, err)
		}
		fields := map[string][]string{
			"statement source": {task.StatementSource}, "class": {task.Class}, "verification": {task.Verification},
			"source": task.Sources, "hidden test": task.Hidden,
		}
		for _, field := range slices.Sorted(maps.Keys(fields)) {
			for _, value := range fields[field] {
				if err := facts.check(value); err != nil {
					return fmt.Errorf("calibration task %s: %s %w", task.ID, field, err)
				}
			}
		}
		// Only the facts classification reads may be shared: any other
		// name could carry a cost, a quota or a person.
		for _, name := range slices.Sorted(maps.Keys(task.Features)) {
			value := task.Features[name]
			if err := config.CheckFeature(name, value); err != nil {
				return fmt.Errorf("calibration task %s: feature %w", task.ID, err)
			}
			if err := facts.check(value); err != nil {
				return fmt.Errorf("calibration task %s: feature %s %w", task.ID, name, err)
			}
		}
	}
	for _, class := range slices.Sorted(maps.Keys(p.Evidence)) {
		if class == "" {
			return errors.New("calibration evidence: a class with no name")
		}
		if err := facts.check(class); err != nil {
			return fmt.Errorf("calibration evidence class %q: %w", class, err)
		}
		for _, row := range p.Evidence[class] {
			if err := validateRow(row); err != nil {
				return fmt.Errorf("calibration evidence for %s: %w", class, err)
			}
			if err := facts.check(row.DuologVersion); err != nil {
				return fmt.Errorf("calibration evidence for %s: duolog_version %w", class, err)
			}
		}
	}
	return nil
}

// families are the model families of the default config's models for agent,
// sorted: a model's family when it names one, else its alias.
func families(agent string) ([]string, error) {
	models, err := config.DefaultModels()
	if err != nil {
		return nil, err
	}
	var out []string
	for name, m := range models {
		if m.Agent != agent {
			continue
		}
		out = append(out, cmp.Or(m.Family, name))
	}
	slices.Sort(out)
	return out, nil
}

// multiWord returns the names of two or more words.
func multiWord(names []string) []string {
	var out []string
	for _, name := range names {
		if len(strings.Fields(name)) >= 2 {
			out = append(out, name)
		}
	}
	return out
}

// duologVersion is a release version, with an optional pre-release or
// build part, such as 1.3.0 or 1.4.0-rc.1.
var duologVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// validateRow allows a family alias only: a digit means a model version, which
// would pin every teammate to a model that ages out. The duolog version must
// be a version string.
func validateRow(row EvidenceRow) error {
	if row.Family == "" || strings.ContainsAny(row.Family, "0123456789") {
		return fmt.Errorf("family %q: record the model family, such as opus or sol, not a version", row.Family)
	}
	if _, err := config.ParseEntryKey(row.Agent + "/" + row.Family + "/" + row.Effort); err != nil {
		return err
	}
	known, err := families(row.Agent)
	if err != nil {
		return err
	}
	if !slices.Contains(known, row.Family) {
		return fmt.Errorf("family %q: not a %s model family (known: %s)", row.Family, row.Agent, strings.Join(known, ", "))
	}
	if row.Tasks < 0 || row.TasksPassed < 0 || row.Judged < 0 || row.TasksPassed > row.Tasks || row.Judged > row.TasksPassed ||
		row.JudgeOnly < 0 || row.JudgeOnly > row.Judged || row.Evidence < 0 || row.Evidence > row.Tasks {
		return fmt.Errorf("%s/%s/%s: task counts out of range", row.Agent, row.Family, row.Effort)
	}
	if _, err := time.Parse(time.DateOnly, row.Date); err != nil {
		return fmt.Errorf("%s/%s/%s: date %q, want YYYY-MM-DD", row.Agent, row.Family, row.Effort, row.Date)
	}
	if !duologVersion.MatchString(row.DuologVersion) {
		return fmt.Errorf("%s/%s/%s: duolog_version %q, want a version such as 1.3.0", row.Agent, row.Family, row.Effort, row.DuologVersion)
	}
	return nil
}

// WriteProject validates p, with names, the history's people, and replaces
// project.yaml with it. The file is shared in Git, so it is readable by all
// (0644), unlike the personal runs.
func WriteProject(stateDir string, p ProjectPart, names ...string) (old, updated []byte, err error) {
	if err := ValidateProject(p, names...); err != nil {
		return nil, nil, err
	}
	updated, err = RenderProject(p)
	if err != nil {
		return nil, nil, err
	}
	path := projectPath(stateDir)
	old, err = os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return old, nil, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+ProjectFile+".*.tmp")
	if err != nil {
		return old, nil, err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(updated)
	closeErr := tmp.Close()
	if writeErr != nil {
		return old, nil, writeErr
	}
	if closeErr != nil {
		return old, nil, closeErr
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		return old, nil, err
	}
	if err := store.Rename(tmp.Name(), path); err != nil {
		return old, nil, err
	}
	return old, updated, nil
}
