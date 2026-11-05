package calibration

import (
	"cmp"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/learning"
)

// Summarize counts the calibration results that count, per ladder and entry:
// the tasks, those that passed the checks, those with a confirmed or
// corrected verdict, their judged quality at the weights learning gives
// them, and the tasks with evidence. A ladder is the class's, or its step
// ladder for the role the tasks ran as. The model of an entry is recorded
// as its family in cfg, so a project alias shares its family's evidence.
func Summarize(cfg *config.Config, cs []journal.CalibrationResult, vs []journal.Verdict, now time.Time, version string) map[string][]EvidenceRow {
	type key struct{ ladder, entry string }
	type tally struct {
		tasks, passed, judged, judgeOnly, evidence int
		quality, weight                            float64
	}
	tallies := map[key]tally{}
	for _, o := range learning.CalibrationObservations(vs, cs) {
		k := key{o.Key, o.Result.Entry}
		t := tallies[k]
		t.tasks++
		if o.Result.Passed {
			t.passed++
		}
		if o.Judged {
			w := o.Weight()
			t.judged++
			t.quality += w * o.Quality
			t.weight += w
			if w < 1 {
				t.judgeOnly++
			}
		}
		if o.Evidence {
			t.evidence++
		}
		tallies[k] = t
	}
	date := now.UTC().Format(time.DateOnly)
	out := map[string][]EvidenceRow{}
	for _, k := range slices.SortedFunc(maps.Keys(tallies), func(a, b key) int {
		return cmp.Or(cmp.Compare(a.ladder, b.ladder), cmp.Compare(a.entry, b.entry))
	}) {
		e, err := config.ParseEntryKey(k.entry)
		if err != nil {
			continue
		}
		t := tallies[k]
		row := EvidenceRow{Agent: e.Agent, Family: modelFamily(cfg, e.Model), Effort: e.Effort, Tasks: t.tasks, TasksPassed: t.passed,
			Judged: t.judged, JudgeOnly: t.judgeOnly, Evidence: t.evidence, Date: date, DuologVersion: version}
		if t.weight > 0 {
			q := t.quality / t.weight
			row.JudgedQuality = &q
		}
		out[k.ladder] = append(out[k.ladder], row)
	}
	return out
}

// RanTasks returns the tasks of m that have a check result in run and were
// not dropped from it: those the project part describes.
func RanTasks(m Manifest, run string, cs []journal.CalibrationResult) []ManifestTask {
	ran, dropped := map[string]bool{}, map[string]bool{}
	for _, c := range cs {
		if c.Run != run {
			continue
		}
		switch c.Kind {
		case "result":
			ran[c.Task] = true
		case "drop":
			dropped[c.Task] = true
		}
	}
	var out []ManifestTask
	for _, t := range m.Tasks {
		if ran[t.ID] && !dropped[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// BuildProjectPart is the shared part of a calibration: the survey facts,
// with only the test commands that passed on HEAD, the tasks of m, and the
// evidence. A row with a model version, or a family the default models do
// not know, stays out, as the project part records families only.
func BuildProjectPart(s Survey, m Manifest, evidence map[string][]EvidenceRow) ProjectPart {
	facts := SurveyFacts{
		Languages:  slices.Clone(s.Facts.Languages),
		Build:      slices.Clone(s.Facts.Build),
		Components: slices.Clone(s.Facts.Components),
	}
	for _, command := range s.Facts.Test {
		if !slices.Contains(s.Failed, command) && !slices.Contains(s.NotRun, command) {
			facts.Test = append(facts.Test, command)
		}
	}
	p := ProjectPart{Version: 1, Survey: facts, Tasks: slices.Clone(m.Tasks)}
	for class, rows := range evidence {
		var kept []EvidenceRow
		for _, row := range rows {
			if validateRow(row) == nil {
				kept = append(kept, row)
			}
		}
		if len(kept) > 0 {
			if p.Evidence == nil {
				p.Evidence = map[string][]EvidenceRow{}
			}
			p.Evidence[class] = kept
		}
	}
	return p
}

// SeedStats turns the project part's evidence into learning stats for the
// ladders cfg has, at the weights the person's own aggregation gave them:
// per row, the failed tasks at quality 0 and weight 1, the judged ones at
// their mean, at weight 1 or 0.5 for a judge-only task, and the tasks with
// evidence as calibration tasks. A row's entry is the ladder's entry of its
// agent, family and effort, so a project alias of a family takes its
// evidence.
func SeedStats(cfg *config.Config, p ProjectPart) learning.Stats {
	st := learning.Stats{}
	for key, rows := range p.Evidence {
		entries, ok := ladderEntries(cfg, key)
		if !ok {
			continue
		}
		for _, row := range rows {
			judged := float64(row.Judged) - 0.5*float64(row.JudgeOnly)
			weight := judged + float64(row.Tasks-row.TasksPassed)
			var q float64
			if weight > 0 && row.JudgedQuality != nil {
				q = judged * *row.JudgedQuality / weight
			}
			entry := row.Agent + "/" + row.Family + "/" + row.Effort
			for _, e := range entries {
				if e.Agent == row.Agent && e.Effort == row.Effort && modelFamily(cfg, e.Model) == row.Family {
					entry = e.Key()
					break
				}
			}
			if st[key] == nil {
				st[key] = map[string]config.EntryStat{}
			}
			s := st[key][entry]
			if weight > 0 {
				s.Quality = (s.Weight*s.Quality + weight*q) / (s.Weight + weight)
				s.Weight += weight
			}
			s.CalibrationTasks += row.Evidence
			st[key][entry] = s
		}
	}
	return st
}

// ladderEntries returns the configured entries of a ladder key: a class, or
// class.role for a step ladder. Exact class names come first, as in learning.
func ladderEntries(cfg *config.Config, key string) ([]config.LadderEntry, bool) {
	if c, ok := cfg.Learning.Classes[key]; ok {
		return c.Ladder, true
	}
	class, role, ok := strings.Cut(key, ".")
	if !ok {
		return nil, false
	}
	steps, ok := cfg.Learning.Classes[class].Steps[role]
	return steps, ok
}

// modelFamily is a model alias's family when it names one, else the alias.
func modelFamily(cfg *config.Config, model string) string {
	if m, ok := cfg.Models[model]; ok {
		return cmp.Or(m.Family, model)
	}
	return model
}
