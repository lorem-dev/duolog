package learn

import (
	"fmt"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/journal"
)

// Stats holds the observations for each ladder and entry.
type Stats map[string]map[string]config.EntryStat

// Aggregate combines live outcomes, replay costs and decided comparisons.
func Aggregate(cfg *config.Config, ds []record.Delegation, ts []record.Task, vs []record.Verdict) Stats {
	return AggregateAll(cfg, ds, ts, vs, nil)
}

// AggregateAll is Aggregate plus the calibration results in cs and the
// calibration verdicts on them; see addCalibration.
func AggregateAll(cfg *config.Config, ds []record.Delegation, ts []record.Task, vs []record.Verdict, cs []record.CalibrationResult) Stats {
	st := Stats{}
	update := func(key, entry string, f func(*config.EntryStat)) {
		if st[key] == nil {
			st[key] = map[string]config.EntryStat{}
		}
		s := st[key][entry]
		f(&s)
		st[key][entry] = s
	}
	outcomes := map[string]string{}
	for _, t := range ts {
		if t.Outcome != "" {
			outcomes[t.ID] = t.Outcome
		}
	}
	type count struct{ usd, seconds int }
	counts := map[string]map[string]count{}
	escalated := map[string]int{}
	for i, d := range ds {
		if d.QualityRun() && d.ProfileOf == "" && d.EscalatedFrom != "" {
			escalated[d.EscalatedFrom] = i
		}
	}
	for i, d := range ds {
		if !d.QualityRun() {
			continue
		}
		d.Entry = effectiveEntry(d)
		if d.Entry == "" || d.Class == "" || d.Class == Unclassified || (d.ProfileOf == "" && d.Task == "") {
			continue
		}
		key := d.Ladder
		if key == "" && cfg != nil {
			key, _ = cfg.Learning.Ladder(d.Class, d.Role)
		}
		if key == "" {
			continue
		}
		if counts[key] == nil {
			counts[key] = map[string]count{}
		}
		n := counts[key][d.Entry]
		n.seconds++
		if d.Priced {
			n.usd++
		}
		counts[key][d.Entry] = n
		update(key, d.Entry, func(s *config.EntryStat) {
			s.Seconds += (d.Seconds - s.Seconds) / float64(n.seconds)
			if d.Priced {
				s.USD += (d.USD - s.USD) / float64(n.usd)
			}
			if d.ProfileOf != "" {
				return
			}
			s.Delegations++
			if !d.Done || escalated[d.ID] > i {
				s.Failures++
				observe(s, 0, 0.5)
			} else if q, ok := journal.Outcomes[outcomes[d.Task]]; ok {
				observe(s, q, 0.5)
			}
		})
	}
	for _, v := range decided(vs) {
		// Calibration verdicts count once per task and entry, with the results.
		if v.Source == calibrationSource {
			continue
		}
		a, b, ok := scoreMeans(v.Scores)
		if !ok {
			continue
		}
		update(v.Ladder, v.Original, func(s *config.EntryStat) { observe(s, a, 1) })
		update(v.Ladder, v.Profile, func(s *config.EntryStat) { observe(s, b, 1) })
	}
	addCalibration(update, vs, cs)
	return st
}

func effectiveEntry(d record.Delegation) string {
	if d.EffortCapped {
		if entry, err := config.ParseEntryKey(d.Entry); err == nil {
			entry.Effort = d.Effort
			return entry.Key()
		}
	}
	return d.Entry
}

func observe(s *config.EntryStat, q, w float64) {
	s.Quality = (s.Weight*s.Quality + w*q) / (s.Weight + w)
	s.Weight += w
}

// decided joins each judge verdict to its latest decision, preserving the
// judge's criteria when a correction only replaces some of them.
func decided(vs []record.Verdict) []record.Verdict {
	decisions := map[string]record.Verdict{}
	for _, v := range vs {
		if v.Kind == "decision" {
			decisions[v.ID] = v
		}
	}
	var out []record.Verdict
	for _, v := range vs {
		if v.Kind != "verdict" {
			continue
		}
		d, ok := decisions[v.ID]
		if !ok || (d.Decision != "confirm" && d.Decision != "correct") {
			continue
		}
		v.Scores = slices.Clone(v.Scores)
		if d.Decision == "correct" {
			for i, s := range v.Scores {
				for _, c := range d.Scores {
					if c.Criterion == s.Criterion {
						v.Scores[i] = c
						break
					}
				}
			}
		}
		out = append(out, v)
	}
	return out
}
func scoreMeans(scores []record.Score) (a, b float64, ok bool) {
	if len(scores) == 0 {
		return 0, 0, false
	}
	for _, s := range scores {
		a += float64(s.Original)
		b += float64(s.Profile)
	}
	return a / float64(len(scores)), b / float64(len(scores)), true
}

// Evidence describes the observations behind one ladder in entry-key order.
func Evidence(st Stats, key string) []string {
	var keys []string
	for k := range st[key] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out []string
	for _, k := range keys {
		s := st[key][k]
		line := fmt.Sprintf("%s: %s %d delegations, weight %g, quality %.2f, failures %d, $%.2f, %.0fs", key, k, s.Delegations, s.Weight, s.Quality, s.Failures, s.USD, s.Seconds)
		if s.CalibrationTasks > 0 {
			line += fmt.Sprintf(", calibration tasks %d", s.CalibrationTasks)
		}
		out = append(out, line)
	}
	return out
}
