package learning

import (
	"slices"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

// calibrationSource tags the verdicts and results of duolog calibrate.
const calibrationSource = "calibration"

// NoDifference is why ProposeGated keeps a ladder's order.
const NoDifference = "no difference found"

// minCalibrationTasks is how many calibration tasks an entry needs before
// calibration evidence may move it in a ladder.
const minCalibrationTasks = 3

type runTask struct{ run, task string }

// countedResults returns the results that count: per class only those of the
// run with the latest result, none of a task dropped from its run, and the
// last one per run, task and entry.
func countedResults(cs []journal.CalibrationResult) []journal.CalibrationResult {
	dropped := map[runTask]bool{}
	latest := map[string]journal.CalibrationResult{}
	for _, c := range cs {
		switch c.Kind {
		case "drop":
			dropped[runTask{c.Run, c.Task}] = true
		case "result":
			l, ok := latest[c.Class]
			if !ok || c.Time.After(l.Time) || c.Time.Equal(l.Time) && c.Run > l.Run {
				latest[c.Class] = c
			}
		}
	}
	type key struct{ run, task, entry string }
	index := map[key]int{}
	var out []journal.CalibrationResult
	for _, c := range cs {
		if c.Kind != "result" || c.Run != latest[c.Class].Run || dropped[runTask{c.Run, c.Task}] {
			continue
		}
		k := key{c.Run, c.Task, c.Entry}
		if i, ok := index[k]; ok {
			out[i] = c
			continue
		}
		index[k] = len(out)
		out = append(out, c)
	}
	return out
}

// calibrationVerdicts returns the confirmed or corrected calibration verdicts
// by run and task.
func calibrationVerdicts(vs []journal.Verdict) map[runTask][]journal.Verdict {
	out := map[runTask][]journal.Verdict{}
	for _, v := range decided(vs) {
		if v.Source == calibrationSource {
			k := runTask{v.Run, v.Task}
			out[k] = append(out[k], v)
		}
	}
	return out
}

// CalibrationObservation is one counted calibration result: the ladder it
// counts under and, for a passed result with a decided verdict on its task,
// the entry's judged quality there. Evidence says whether the task counts
// toward the three an entry needs: a failed check, a decided verdict, or a
// pass no other entry matched while another entry has a result on the task,
// which needs no judge. A pass that waits for a verdict, or that no other
// result on the task compares, is not evidence.
type CalibrationObservation struct {
	Key      string
	Result   journal.CalibrationResult
	Judged   bool
	Quality  float64
	Evidence bool
}

// CalibrationObservations returns one observation per counted task and
// entry. A passed result's quality is the mean of the entry's score means
// over the decided verdicts on the task.
func CalibrationObservations(vs []journal.Verdict, cs []journal.CalibrationResult) []CalibrationObservation {
	results := countedResults(cs)
	if len(results) == 0 {
		return nil
	}
	verdicts := calibrationVerdicts(vs)
	// Results recorded without a ladder take the one their verdicts name,
	// else the class's.
	ladder := func(c journal.CalibrationResult) string {
		if c.Ladder != "" {
			return c.Ladder
		}
		if on := verdicts[runTask{c.Run, c.Task}]; len(on) > 0 && on[0].Ladder != "" {
			return on[0].Ladder
		}
		return c.Class
	}
	type ladderTask struct{ run, task, key string }
	passes, entries := map[ladderTask]int{}, map[ladderTask]int{}
	for _, c := range results {
		k := ladderTask{c.Run, c.Task, ladder(c)}
		entries[k]++
		if c.Passed {
			passes[k]++
		}
	}
	// A lone pass compares only when another entry has a result on the
	// task; with the others cut or skipped it says nothing.
	lonePass := func(k ladderTask) bool { return passes[k] == 1 && entries[k] > 1 }
	var out []CalibrationObservation
	for _, c := range results {
		on := verdicts[runTask{c.Run, c.Task}]
		key := ladder(c)
		if key == "" || c.Entry == "" {
			continue
		}
		o := CalibrationObservation{Key: key, Result: c, Evidence: !c.Passed || lonePass(ladderTask{c.Run, c.Task, key})}
		if c.Passed {
			var sum float64
			n := 0
			for _, v := range on {
				a, b, ok := scoreMeans(v.Scores)
				switch {
				case !ok:
				case v.Original == c.Entry:
					sum, n = sum+a, n+1
				case v.Profile == c.Entry:
					sum, n = sum+b, n+1
				}
			}
			if n > 0 {
				o.Judged, o.Quality, o.Evidence = true, sum/float64(n), true
			}
		}
		out = append(out, o)
	}
	return out
}

// Weight is how much the observation's quality counts: 1 for a failed
// result, which needs no confirmation; for a judged one 1, or 0.5 for a
// judge_only task; 0 for a pass without a decided verdict.
func (o CalibrationObservation) Weight() float64 {
	switch {
	case !o.Result.Passed:
		return 1
	case !o.Judged:
		return 0
	case o.Result.Verification == "judge_only":
		return 0.5
	}
	return 1
}

// addCalibration adds one observation per counted task and entry: a failed
// result at quality 0, a judged one at its quality, each at its Weight. A
// pass without a decided verdict adds no quality. Only an observation with
// evidence adds a calibration task.
func addCalibration(update func(key, entry string, f func(*config.EntryStat)), vs []journal.Verdict, cs []journal.CalibrationResult) {
	for _, o := range CalibrationObservations(vs, cs) {
		update(o.Key, o.Result.Entry, func(s *config.EntryStat) {
			if o.Evidence {
				s.CalibrationTasks++
			}
			if w := o.Weight(); w > 0 {
				observe(s, o.Quality, w)
			}
		})
	}
}

// ProposeGated proposes with calibration evidence where it separates the
// entries, and without it elsewhere. base ranks live evidence only; with adds
// the calibration results in cs, or, when cs has no result, the shared seed,
// never both. A ladder whose two orders differ takes with only when every
// entry with calibration tasks, and every entry that moved, has at least 3;
// otherwise it keeps base and kept names it with NoDifference.
func ProposeGated(cfg *config.Config, ds []journal.Delegation, ts []journal.Task, vs []journal.Verdict, cs []journal.CalibrationResult, seed Stats) (p config.LearnedRouting, st Stats, kept map[string]string) {
	live := slices.DeleteFunc(slices.Clone(vs), func(v journal.Verdict) bool { return v.Source == calibrationSource })
	base := Propose(cfg, Aggregate(cfg, ds, ts, vs), live)
	verdicts := live
	if slices.ContainsFunc(cs, func(c journal.CalibrationResult) bool { return c.Kind == "result" }) {
		st = AggregateAll(cfg, ds, ts, vs, cs)
		verdicts = append(slices.Clone(live), calibrationWinners(vs, cs)...)
	} else {
		st = Aggregate(cfg, ds, ts, vs)
		addSeed(st, seed)
	}
	with := Propose(cfg, st, verdicts)
	p = config.LearnedRouting{Ladders: map[string]config.LearnedLadder{}}
	kept = map[string]string{}
	for key, w := range with.Ladders {
		b, ok := base.Ladders[key]
		before := b.Order
		if !ok {
			before = currentOrder(cfg, key)
		}
		if slices.Equal(w.Order, before) || separated(st[key], before, w.Order) {
			p.Ladders[key] = w
			continue
		}
		kept[key] = NoDifference
		if ok {
			p.Ladders[key] = b
		}
	}
	return p, st, kept
}

// separated reports whether every entry of after with calibration tasks, and
// every entry whose position differs from before, has enough of them.
func separated(stats map[string]config.EntryStat, before, after []string) bool {
	for i, e := range after {
		n := stats[e].CalibrationTasks
		if n > 0 && n < minCalibrationTasks || slices.Index(before, e) != i && n < minCalibrationTasks {
			return false
		}
	}
	return true
}

// calibrationWinners turns each decided calibration verdict on a counted task
// into a confirmed verdict whose profile is the higher side, so Propose may
// add that entry to its ladder as it does a winning profile. A pair's sides
// are in entry-key order, so the winner may be either.
func calibrationWinners(vs []journal.Verdict, cs []journal.CalibrationResult) []journal.Verdict {
	counted := map[runTask]bool{}
	for _, c := range countedResults(cs) {
		counted[runTask{c.Run, c.Task}] = true
	}
	var out []journal.Verdict
	for _, v := range decided(vs) {
		if v.Source != calibrationSource || !counted[runTask{v.Run, v.Task}] {
			continue
		}
		a, b, ok := scoreMeans(v.Scores)
		if !ok || a == b {
			continue
		}
		if a > b {
			v.Original, v.Profile = v.Profile, v.Original
			for i, s := range v.Scores {
				s.Original, s.Profile = s.Profile, s.Original
				s.OriginalCount, s.ProfileCount = s.ProfileCount, s.OriginalCount
				v.Scores[i] = s
			}
		}
		// The scores are already corrected; a later confirm keeps them.
		out = append(out, v, journal.Verdict{Kind: "decision", ID: v.ID, Decision: "confirm"})
	}
	return out
}

// addSeed adds the shared project part's evidence to st.
func addSeed(st, seed Stats) {
	for key, entries := range seed {
		if st[key] == nil {
			st[key] = map[string]config.EntryStat{}
		}
		for entry, s := range entries {
			cur := st[key][entry]
			if s.Weight > 0 {
				observe(&cur, s.Quality, s.Weight)
			}
			cur.CalibrationTasks += s.CalibrationTasks
			st[key][entry] = cur
		}
	}
}
