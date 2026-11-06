package budget

import (
	"math"
	"slices"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
)

// Mean is what the journal says one delegation of a class costs.
type Mean struct {
	Tokens, P90 int64
	Seconds     float64
	N           int
}

type ClassMeans map[string]Mean

// minMeanRecords is the fewest journal records whose mean replaces the
// default by scope; minP90Records the fewest whose 90th percentile is the
// high end of the range.
const (
	minMeanRecords = 3
	minP90Records  = 5
)

// scopeDefaults are the estimates by scope when the journal has too few
// records of a class.
var scopeDefaults = map[string]struct {
	low, high int64
	minutes   float64
}{
	"1-3":  {80000, 250000, 8},
	"4-20": {200000, 600000, 20},
	"20+":  {500000, 1500000, 40},
}

// JournalMeans averages tokens (input and output) and seconds per class
// over the delegations that say something about quality, leaving out the
// profile reruns of another delegation.
func JournalMeans(ds []record.Delegation) ClassMeans {
	tokens := map[string][]int64{}
	seconds := map[string]float64{}
	for _, d := range ds {
		if d.Class == "" || !d.QualityRun() || d.ProfileOf != "" {
			continue
		}
		tokens[d.Class] = append(tokens[d.Class], d.Input+d.Output)
		seconds[d.Class] += d.Seconds
	}
	means := ClassMeans{}
	for class, ts := range tokens {
		var sum int64
		for _, n := range ts {
			sum += n
		}
		slices.Sort(ts)
		// Nearest rank: the smallest value at or above 90% of the records.
		p90 := ts[int(math.Ceil(0.9*float64(len(ts))))-1]
		means[class] = Mean{Tokens: sum / int64(len(ts)), P90: p90, Seconds: seconds[class] / float64(len(ts)), N: len(ts)}
	}
	return means
}

// Estimate is the expected size and cost of one cell of a task.
type Estimate struct {
	Files        int     `json:"files"`
	Lines        int     `json:"lines"`
	Components   int     `json:"components"`
	TokensLow    int64   `json:"tokens_low"`
	TokensHigh   int64   `json:"tokens_high"`
	Minutes      float64 `json:"minutes"`
	CheckSeconds float64 `json:"check_seconds"`
	Large        bool    `json:"large"`
}

// EstimateTask sizes c from the journal means of its class, or from the
// default of its scope when the journal has fewer than 3 records.
func EstimateTask(c Candidate, m ClassMeans, checkSeconds float64, cfg config.Calibration) Estimate {
	e := Estimate{Files: len(c.Files), Lines: c.Lines, Components: len(c.Components), CheckSeconds: checkSeconds}
	if mean, ok := m[c.Class]; ok && mean.N >= minMeanRecords {
		e.TokensLow, e.TokensHigh, e.Minutes = mean.Tokens, 2*mean.Tokens, mean.Seconds/60
		if mean.N >= minP90Records {
			e.TokensHigh = mean.P90
		}
	} else {
		d := scopeDefaults[scopeOf(c)]
		e.TokensLow, e.TokensHigh, e.Minutes = d.low, d.high, d.minutes
	}
	e.Large = c.Large || e.TokensHigh > cfg.CellMaxTokens/2
	return e
}

// overCap reports whether one cell of the estimate would pass the cell cap.
func overCap(e Estimate, cfg config.Calibration) bool {
	return e.TokensHigh > cfg.CellMaxTokens || e.Minutes > float64(cfg.CellMaxMinutes)
}

// scopeOf is the scope feature of c, or the one its file count gives.
func scopeOf(c Candidate) string {
	if _, ok := scopeDefaults[c.Features["scope"]]; ok {
		return c.Features["scope"]
	}
	switch n := len(c.Files); {
	case n > 20:
		return "20+"
	case n > 3:
		return "4-20"
	}
	return "1-3"
}
