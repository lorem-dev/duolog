package learn

import (
	"bytes"
	"math"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
)

// Estimate combines the configured prior with weighted journal observations.
func Estimate(l config.Learning, prior float64, s config.EntryStat) (quality, margin float64) {
	n := l.PriorRuns + s.Weight
	return (l.PriorRuns*prior + s.Weight*s.Quality) / n, l.Uncertainty / math.Sqrt(n)
}

// Rank orders entries by their class's quality or cost priority.
func Rank(l config.Learning, c config.Class, entries []config.LadderEntry, stats map[string]config.EntryStat) []string {
	remaining := make([]int, len(entries))
	quality := make([]float64, len(entries))
	qualifies := make([]bool, len(entries))
	for i, e := range entries {
		remaining[i] = i
		q, m := Estimate(l, e.Prior, stats[e.Key()])
		quality[i] = q
		qualifies[i] = q-m >= l.Threshold
	}
	cheaper := func(a, b int) bool {
		sa, sb := stats[entries[a].Key()], stats[entries[b].Key()]
		if sa.Delegations > 0 && sb.Delegations > 0 {
			return sa.USD < sb.USD
		}
		return a < b
	}
	var out []string
	for len(remaining) > 0 {
		best := remaining[0]
		for _, i := range remaining {
			if quality[i] > quality[best] {
				best = i
			}
		}
		if c.Priority == "quality" {
			chosen := -1
			for _, i := range remaining {
				if quality[i] >= quality[best]-l.QualityMargin && (chosen < 0 || cheaper(i, chosen)) {
					chosen = i
				}
			}
			best = chosen
		} else {
			chosen := -1
			for _, i := range remaining {
				if qualifies[i] && (chosen < 0 || cheaper(i, chosen)) {
					chosen = i
				}
			}
			if chosen >= 0 {
				best = chosen
			}
		}
		out = append(out, entries[best].Key())
		remaining = slices.DeleteFunc(remaining, func(i int) bool { return i == best })
	}
	return out
}

func configured(l config.Learning, key string) (class, role string, c config.Class, entries []config.LadderEntry) {
	// Exact class names take precedence over step keys.
	if c, ok := l.Classes[key]; ok {
		return key, "", c, c.Ladder
	}
	class, role, _ = strings.Cut(key, ".")
	c = l.Classes[class]
	return class, role, c, c.Steps[role]
}

// Propose ranks ladders with evidence or an existing learned order. A decided
// winning profile may add an entry without changing the project's config.
func Propose(cfg *config.Config, st Stats, vs []record.Verdict) config.LearnedRouting {
	l := cfg.Learning
	keys := map[string]bool{}
	for k := range st {
		keys[k] = true
	}
	for k, v := range l.Learned {
		if len(v.Order) > 0 {
			keys[k] = true
		}
	}
	winners := map[string][]string{}
	for _, v := range decided(vs) {
		a, b, ok := scoreMeans(v.Scores)
		if ok && b > a {
			keys[v.Ladder] = true
			winners[v.Ladder] = append(winners[v.Ladder], v.Profile)
		}
	}
	p := config.LearnedRouting{Ladders: map[string]config.LearnedLadder{}}
	for key := range keys {
		_, _, c, es := configured(l, key)
		es = slices.Clone(es)
		// Only decided winning profiles can extend the configured candidates.
		for _, name := range winners[key] {
			if slices.ContainsFunc(es, func(e config.LadderEntry) bool { return e.Key() == name }) {
				continue
			}
			e, err := config.ParseEntryKey(name)
			if err != nil {
				continue
			}
			e.Prior = l.Threshold
			es = append(es, e)
		}
		p.Ladders[key] = config.LearnedLadder{Order: Rank(l, c, es, st[key]), Stats: st[key]}
	}
	return p
}

func currentOrder(cfg *config.Config, key string) []string {
	class, role, _, _ := configured(cfg.Learning, key)
	_, es := cfg.Learning.Ladder(class, role)
	var out []string
	for _, e := range es {
		out = append(out, e.Key())
	}
	return out
}

// Changed compares orders only, treating a missing learned order as configured.
func Changed(cfg *config.Config, p config.LearnedRouting) bool {
	for key, v := range p.Ladders {
		if !slices.Equal(v.Order, currentOrder(cfg, key)) {
			return true
		}
	}
	return false
}

// LineDiff walks a longest common subsequence, preferring deletions on ties.
func LineDiff(old, new []byte) string {
	if bytes.Equal(old, new) {
		return ""
	}
	lines := func(b []byte) []string {
		if len(b) == 0 {
			return nil
		}
		return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
	a, b := lines(old), lines(new)
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = 1 + dp[i+1][j+1]
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	var out strings.Builder
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out.WriteString(" " + a[i] + "\n")
			i++
			j++
		case i < len(a) && (j == len(b) || dp[i+1][j] >= dp[i][j+1]):
			out.WriteString("-" + a[i] + "\n")
			i++
		default:
			out.WriteString("+" + b[j] + "\n")
			j++
		}
	}
	return out.String()
}
