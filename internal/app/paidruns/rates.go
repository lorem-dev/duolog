package paidruns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lorem-dev/duolog/internal/core/account"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/route"
)

const (
	// resetTolerance is how far apart two reads may place one window's reset.
	resetTolerance = time.Minute
	// maxIntervals is the most qualified intervals kept per family and window;
	// older ones go first.
	maxIntervals = 12
	// fingerprintKind keeps the account fingerprint in State.AccountKeys, so
	// a new one drops the agent's rates and tier as a new identity does.
	fingerprintKind = "fingerprint"
	// intervalsFile holds the movement and tokens of intervals still too
	// small to count, beside the state and under its lock.
	intervalsFile    = "intervals.json"
	intervalsVersion = 1
)

// Observe learns window rates from one interval. For every window of the
// After read that limits the family, the window's movement since the Before
// read and the interval's weighted tokens add to that window's open interval.
// Once the open interval has moved balance.min_interval_share and has tokens,
// it becomes a qualified interval: a RateSample of the movement, as a
// fraction of the whole window, over its weighted tokens. The usable share
// plays no part, so rates stay in real units.
//
// Movement the agent's own runs did not cause, such as the person's work,
// stays in the movement while only tracked tokens count, so it raises the
// rate; movement without tokens waits for tokens and is never a rate.
//
// A reset between the reads, falling usage, a rejected or full window, and
// reads whose plan or windows differ measure nothing and drop the open
// interval. An interval that starts before the end of the last one counted,
// such as the same reads again, counts nothing.
//
// A new fingerprint drops the agent's rates and tier, as a new identity
// does; spend and reservations stay. When the last three intervals of a
// window all differ from the rate of the earlier ones by more than
// balance.margin, the window's rates and the agent's tier go, and three fresh
// intervals must qualify again. Observe never sets a tier.
func (s *Store) Observe(ctx context.Context, cfg *config.Config,
	in paidrules.Interval) error {
	if in.Agent == "" || in.Family == "" || in.Fingerprint == "" {
		return errors.New("observe rates: an interval needs an agent, a family and a fingerprint")
	}
	family := route.Normalize(in.Agent, in.Family)
	weighted, err := paidrules.WeightedTokens(cfg, in.Agent, family, in.Input, in.Cached, in.Output)
	if err != nil {
		return fmt.Errorf("observe rates: %w", err)
	}
	for _, r := range []account.Snapshot{in.Before, in.After} {
		if !validWindows(r) {
			return errors.New("observe rates: invalid window usage")
		}
	}
	known, err := config.DefaultModels()
	if err != nil {
		return err
	}
	minShare, margin := balanceTerms(cfg)
	return s.Update(ctx, func(st *State) error {
		open, old, err := s.loadIntervals()
		if err != nil {
			return err
		}
		l := rateLearner{st: st, open: open, known: known, minShare: minShare, margin: margin}
		l.observe(in, family, weighted)
		data, err := json.MarshalIndent(intervalsState{Version: intervalsVersion, Open: open}, "", "  ")
		if err != nil {
			return fmt.Errorf("paid runs: encode open intervals: %w", err)
		}
		data = append(data, '\n')
		if bytes.Equal(data, old) {
			return nil
		}
		return s.write(intervalsFile, data)
	})
}

// balanceTerms are balance.min_interval_share and balance.margin as tier
// selection reads them, so learning and qualification agree: a missing or
// invalid value is its default.
func balanceTerms(cfg *config.Config) (minShare, margin float64) {
	_, balance := paidrules.TierSettings(cfg)
	return balance.MinIntervalShare, balance.Margin
}

// openInterval is the movement and weighted tokens of one window since its
// last qualified interval, and the read that ended it.
type openInterval struct {
	Agent          string    `json:"agent"`
	Fingerprint    string    `json:"fingerprint"`
	Reset          time.Time `json:"reset"`
	End            time.Time `json:"end"`
	Movement       float64   `json:"movement"`
	WeightedTokens float64   `json:"weighted_tokens"`
}

type intervalsState struct {
	Version int                     `json:"version"`
	Open    map[string]openInterval `json:"open"`
}

// loadIntervals reads the open intervals and their bytes. A file that is not
// valid refuses, so no interval counts twice after a damaged write.
func (s *Store) loadIntervals() (map[string]openInterval, []byte, error) {
	path := filepath.Join(s.Dir, intervalsFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]openInterval{}, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("paid runs: read open intervals: %w", err)
	}
	var st intervalsState
	if json.Unmarshal(data, &st) != nil || st.Version != intervalsVersion {
		return nil, nil, fmt.Errorf("paid runs: %s is not valid; rates are not learned until it is repaired", path)
	}
	if st.Open == nil {
		st.Open = map[string]openInterval{}
	}
	return st.Open, data, nil
}

type rateLearner struct {
	st               *State
	open             map[string]openInterval
	known            map[string]config.Model
	minShare, margin float64
}

// scopedWindow is a window and the model family it limits, "" for all.
type scopedWindow struct {
	w     account.Window
	scope string
}

func (l *rateLearner) observe(in paidrules.Interval, family string, weighted float64) {
	fp := in.Fingerprint
	l.st.recordIdentity(in.Agent, fingerprintKind, fp)
	l.st.Samples = slices.DeleteFunc(l.st.Samples, func(r paidrules.RateSample) bool {
		return r.Agent == in.Agent && r.Fingerprint != fp
	})
	maps.DeleteFunc(l.open, func(_ string, o openInterval) bool {
		return o.Agent == in.Agent && o.Fingerprint != fp
	})

	b, a := in.Before, in.After
	if b.ReadAt.IsZero() || !a.ReadAt.After(b.ReadAt) {
		return
	}
	shapeKept := b.Plan == a.Plan && slices.Equal(paidrules.WindowShape(b), paidrules.WindowShape(a))
	before := l.familyWindows(b, family)
	for _, x := range l.familyWindows(a, family) {
		key := in.Agent + "/" + family + "/" + x.scope + "/" + x.w.Kind
		o := l.open[key]
		if o.Fingerprint != fp {
			o = openInterval{Agent: in.Agent, Fingerprint: fp}
		}
		if b.ReadAt.Before(o.End) {
			continue
		}
		o.End = a.ReadAt
		delta, ok := 0.0, false
		if shapeKept {
			if i := slices.IndexFunc(before, func(y scopedWindow) bool { return sameWindow(x, y) }); i >= 0 {
				delta, ok = movement(before[i].w, x.w, b, a)
			}
		}
		if !ok || !o.Reset.IsZero() && !sameReset(o.Reset, x.w.ResetsAt) {
			o.Movement, o.WeightedTokens = 0, 0
		}
		o.Reset = x.w.ResetsAt
		if ok {
			o.Movement += delta
			o.WeightedTokens += weighted
			if o.WeightedTokens > 0 && o.Movement >= l.minShare-paidrules.FitSlack {
				l.add(paidrules.RateSample{Agent: in.Agent, Family: family, Fingerprint: fp,
					Window:   paidrules.WindowKey{Kind: x.w.Kind, Family: x.scope, Reset: x.w.ResetsAt},
					Movement: o.Movement, WeightedTokens: o.WeightedTokens, Intervals: 1})
				o.Movement, o.WeightedTokens = 0, 0
			}
		}
		l.open[key] = o
	}
}

// familyWindows are the windows of s that limit family, as WindowCosts
// places them: the account's windows, the family's own and those that name
// no known family.
func (l *rateLearner) familyWindows(s account.Snapshot, family string) []scopedWindow {
	var ws []scopedWindow
	for _, w := range s.Windows {
		ws = append(ws, scopedWindow{w, paidrules.WindowScope(w, l.known)})
	}
	for _, scope := range slices.Sorted(maps.Keys(s.ModelLimits)) {
		for _, w := range s.ModelLimits[scope] {
			ws = append(ws, scopedWindow{w, scope})
		}
	}
	return slices.DeleteFunc(ws, func(x scopedWindow) bool {
		return x.scope != "" && x.scope != family && l.known[x.scope].Agent != ""
	})
}

func sameWindow(a, b scopedWindow) bool {
	return a.scope == b.scope && a.w.Kind == b.w.Kind && a.w.Group == b.w.Group &&
		a.w.Model == b.w.Model && a.w.DurationMinutes == b.w.DurationMinutes
}

func sameReset(a, b time.Time) bool {
	d := a.Sub(b)
	return d <= resetTolerance && d >= -resetTolerance
}

// movement is how far window b of read before moved to window a of read
// after, as a fraction of the window. It is false when the reads cannot show
// it: an account rejection or spend control, a rejected or full window whose
// usage hides how far it went, an unknown or changed reset, a reset passed by
// the second read, or falling usage.
func movement(b, a account.Window, before, after account.Snapshot) (float64, bool) {
	switch {
	case before.Rejected || after.Rejected || before.SpendControlReached || after.SpendControlReached:
		return 0, false
	case b.Rejected() || a.Rejected() || b.UsedPercent >= 100 || a.UsedPercent >= 100:
		return 0, false
	case b.ResetsAt.IsZero() || !sameReset(b.ResetsAt, a.ResetsAt):
		return 0, false
	case !b.ResetsAt.After(after.ReadAt) || !a.ResetsAt.After(after.ReadAt):
		return 0, false
	}
	d := (a.UsedPercent - b.UsedPercent) / 100
	return d, d >= 0
}

// add keeps a qualified interval. When the last three of its window all
// differ from the earlier rate by more than the margin, the window's samples
// and the agent's tier go; otherwise the oldest past maxIntervals go.
func (l *rateLearner) add(r paidrules.RateSample) {
	l.st.Samples = append(l.st.Samples, r)
	same := func(x paidrules.RateSample) bool {
		return x.Agent == r.Agent && x.Family == r.Family && x.Fingerprint == r.Fingerprint &&
			x.Window.Kind == r.Window.Kind && x.Window.Family == r.Window.Family
	}
	var mine []paidrules.RateSample
	for _, x := range l.st.Samples {
		if same(x) {
			mine = append(mine, x)
		}
	}
	if drifted(mine, l.margin) {
		l.st.Samples = slices.DeleteFunc(l.st.Samples, same)
		delete(l.st.Tiers, r.Agent)
		return
	}
	drop := len(mine) - maxIntervals
	l.st.Samples = slices.DeleteFunc(l.st.Samples, func(x paidrules.RateSample) bool {
		if drop > 0 && same(x) {
			drop--
			return true
		}
		return false
	})
}

// drifted reports whether the last three samples each differ from the
// pooled rate of the earlier ones, which rest on at least three intervals,
// by more than margin.
func drifted(rs []paidrules.RateSample, margin float64) bool {
	if len(rs) < 4 {
		return false
	}
	older, recent := rs[:len(rs)-3], rs[len(rs)-3:]
	var movement, tokens float64
	intervals := 0
	for _, x := range older {
		movement += x.Movement
		tokens += x.WeightedTokens
		intervals += x.Intervals
	}
	if intervals < paidrules.MinMeasurements || tokens <= 0 {
		return false
	}
	stored := movement / tokens
	for _, x := range recent {
		rate := x.Movement / x.WeightedTokens
		if rate <= stored*(1+margin) && rate >= stored*(1-margin) {
			return false
		}
	}
	return true
}

func validWindows(s account.Snapshot) bool {
	all := slices.Clone(s.Windows)
	for _, ws := range s.ModelLimits {
		all = append(all, ws...)
	}
	return !slices.ContainsFunc(all, func(w account.Window) bool {
		return !paidrules.Finite(w.UsedPercent) || w.UsedPercent < 0 || w.UsedPercent > 100
	})
}
