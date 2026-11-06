package calibration

import (
	"cmp"
	"context"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"strings"
	"time"
)

// ClassCount says what mining found for one class.
type ClassCount struct {
	Candidates int    `json:"candidates"`
	Valid      int    `json:"valid"`
	Selected   int    `json:"selected"`
	Reserve    int    `json:"reserve"`
	WindowDays int    `json:"window_days"`
	Skipped    string `json:"skipped,omitempty"`
}

type Exclusion struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Manifest is the sample of one calibration run, kept in its manifest.json.
type Manifest struct {
	Seed         int64                 `json:"seed"`
	Tasks        []ManifestTask        `json:"tasks"`   // selected, in sample order; Round 1 first
	Round2       []string              `json:"round2"`  // task IDs held for round 2
	Reserve      map[string][]string   `json:"reserve"` // class -> task IDs
	Counts       map[string]ClassCount `json:"counts"`
	Excluded     []Exclusion           `json:"excluded"`
	Estimates    map[string]Estimate   `json:"estimates"`    // selected and reserve tasks
	Verification map[string]string     `json:"verification"` // task ID -> verification, selected and reserve
	Rewrite      []string              `json:"rewrite"`      // task IDs whose statement is rewritten in the run
	RepoFiles    int                   `json:"repo_files"`
	HistoryDays  int                   `json:"history_days"`
	CheckSeconds float64               `json:"check_seconds"`
}

// Sampling bounds per class.
const (
	roundTasks    = 3 // round 1 tasks, and the most round 2 and reserve tasks
	minClassTasks = 3 // fewer valid tasks skip the class
)

// Recency buckets, in days before now.
const (
	recentDays = 90
	yearDays   = 365
)

const fewerTasks = "fewer than 3 tasks"

// validationsPerSlot bounds mining: a class validates at most this many
// candidates per place in its rounds and reserve, since each validation
// exports two trees and runs the checks up to 6 times.
const validationsPerSlot = 10

const budgetReached = "validation budget reached"

// ValidationBudget is the most candidates mining validates per class in the
// project s describes.
func ValidationBudget(s Survey) int {
	return validationsPerSlot * (roundTasks + round2Tasks(s) + roundTasks)
}

// round2Tasks is the size of round 2: none in a small project.
func round2Tasks(s Survey) int {
	if smallProject(s) {
		return 0
	}
	return roundTasks
}

func smallProject(s Survey) bool { return s.Files < smallFiles || s.Commits < smallCommits }

// validation is the memoised result of validating one candidate.
type validation struct{ verification, reason string }

// sampler draws the tasks of each class; it validates each candidate at
// most once, however often the window widens, and at most budget
// candidates per class.
type sampler struct {
	ctx       context.Context
	seed      int64
	now       time.Time
	validate  func(ctx context.Context, c Candidate) (verification, reason string, err error)
	estimates map[string]Estimate
	results   map[string]validation
	budget    int
	spent     map[string]int  // class -> validations
	unchecked map[string]bool // candidates left unvalidated at the budget
}

// draw is the sample of one class from the candidates of one window.
type draw struct {
	round1, round2, reserve []*Candidate
	valid                   int
}

// widen draws class from the candidates of the first window and widens the
// window while round 1 has fewer than 3 tasks: valid tasks the directory cap
// sends to the reserve do not make the class ready. When even the widest
// window leaves round 1 short, the class's valid tasks span fewer than 3
// top-level directories (each directory gives round 1 at least one), so the
// cap gives way: round 1 keeps the tasks it took from the other directories
// and fills up from the capped one. It returns the last window, 0 for all
// days.
func (s *sampler) widen(class string, pool []*Candidate, first, round2 int) (draw, int, error) {
	windows := []int{0}
	if first > 0 {
		windows = []int{first}
		for _, w := range []int{180, yearDays} {
			if w > first {
				windows = append(windows, w)
			}
		}
		windows = append(windows, 0)
	}
	var d draw
	var window int
	var order []*Candidate
	for _, window = range windows {
		var in []*Candidate
		for _, c := range pool {
			if window == 0 || s.age(c) <= window {
				in = append(in, c)
			}
		}
		order = s.order(class, in)
		var err error
		if d, err = s.draw(class, order, round2, true); err != nil {
			return draw{}, 0, err
		}
		if len(d.round1) >= minClassTasks {
			return d, window, nil
		}
	}
	// Validations are memoised, so the second draw validates only the
	// candidates of the capped directory that now find a place.
	taken := d.round1
	order = append(slices.Clone(taken), slices.DeleteFunc(order, func(c *Candidate) bool { return slices.Contains(taken, c) })...)
	d, err := s.draw(class, order, round2, false)
	return d, window, err
}

// draw validates the candidates of order in that order until round 1,
// round 2 and the reserve are full. Round 2, and round 1 when capRound1 is
// set, take at most a third of their tasks (rounded up) from one top-level
// directory, and the class takes at most one large task. A candidate that
// fits no open place, or comes after the class has spent its budget, is not
// validated.
func (s *sampler) draw(class string, order []*Candidate, round2 int, capRound1 bool) (draw, error) {
	perDir := (roundTasks + round2 + 2) / 3
	dirs := map[string]int{}
	large := false
	var d draw
	for _, c := range order {
		if len(d.round1) == roundTasks && len(d.round2) == round2 && len(d.reserve) == roundTasks {
			break
		}
		isLarge := s.estimates[c.ID].Large
		if isLarge && large {
			continue
		}
		dirOK := !slices.ContainsFunc(c.Components, func(dir string) bool { return dirs[dir] >= perDir })
		var slot *[]*Candidate
		switch {
		case (dirOK || !capRound1) && len(d.round1) < roundTasks:
			slot = &d.round1
		case dirOK && len(d.round2) < round2:
			slot = &d.round2
		case len(d.reserve) < roundTasks:
			slot = &d.reserve
		default:
			continue
		}
		if _, done := s.results[c.ID]; !done && s.spent[class] >= s.budget {
			s.unchecked[c.ID] = true
			continue
		}
		v, err := s.check(class, c)
		if err != nil {
			return draw{}, err
		}
		if v.verification == "" {
			continue
		}
		d.valid++
		*slot = append(*slot, c)
		if slot != &d.reserve {
			for _, dir := range c.Components {
				dirs[dir]++
			}
		}
		large = large || isLarge
	}
	return d, nil
}

func (s *sampler) check(class string, c *Candidate) (validation, error) {
	if v, ok := s.results[c.ID]; ok {
		return v, nil
	}
	s.spent[class]++
	verification, reason, err := s.validate(s.ctx, *c)
	if err != nil {
		return validation{}, err
	}
	if verification == "" && reason == "" {
		reason = "no verification"
	}
	v := validation{verification, reason}
	s.results[c.ID] = v
	return v, nil
}

// age is the number of whole days from c to now.
func (s *sampler) age(c *Candidate) int { return int(s.now.Sub(c.Time).Hours() / 24) }

type stratum struct{ recency, size int }

// order groups pool into strata by recency and size, shuffles each stratum
// with the run's seed, and takes one candidate from each stratum in turn,
// small and medium strata before large ones in every turn.
func (s *sampler) order(class string, pool []*Candidate) []*Candidate {
	strata := map[stratum][]*Candidate{}
	for _, c := range pool {
		k := stratum{recency: 2, size: size(c)}
		switch age := s.age(c); {
		case age <= recentDays:
			k.recency = 0
		case age <= yearDays:
			k.recency = 1
		}
		strata[k] = append(strata[k], c)
	}
	var keys []stratum
	for k := range strata {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b stratum) int {
		return cmp.Or(cmp.Compare(a.size/2, b.size/2), cmp.Compare(a.recency, b.recency), cmp.Compare(a.size, b.size))
	})
	h := fnv.New64a()
	h.Write([]byte(class))
	rng := rand.New(rand.NewPCG(uint64(s.seed), h.Sum64())) //nolint:gosec // G115: a negative seed wraps to another fixed seed, which is all sampling needs
	for _, k := range keys {
		list := strata[k]
		slices.SortFunc(list, func(a, b *Candidate) int { return strings.Compare(a.ID, b.ID) })
		rng.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
	}
	var out []*Candidate
	for i := 0; len(out) < len(pool); i++ {
		for _, k := range keys {
			if i < len(strata[k]) {
				out = append(out, strata[k][i])
			}
		}
	}
	return out
}

// size is 0 for a small scope, 1 for a medium one and 2 for a large one or
// a candidate of the large stratum.
func size(c *Candidate) int {
	switch {
	case c.Large || scopeOf(*c) == "20+":
		return 2
	case scopeOf(*c) == "4-20":
		return 1
	}
	return 0
}
