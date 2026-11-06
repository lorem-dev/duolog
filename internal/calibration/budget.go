package calibration

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/store"
)

const (
	ReserveWeekly = 15.0 // percentage points kept for the lead
	ReserveWindow = 10.0
	BoundWeekly   = 1.0 // per cell without a measured rate
	BoundWindow   = 5.0
	QuotaCacheTTL = 120 * time.Second
	KindWeekly    = "weekly"
	KindFiveHour  = "five_hour"
)

// RatesFile holds the measured quota rates under Dir; it is personal.
const RatesFile = "rates.json"

// resetSlack absorbs a reset time that moves by seconds between readings of
// one window; the next five-hour window resets hours later.
const resetSlack = time.Minute

// Window mirrors one window of internal/quota (feat/quota-command): the
// adapter in cmd/duolog/calibrate_quota.go fills it.
type Window struct {
	Kind string
	// Model is the family a model's own limit applies to; "" for the
	// account's windows.
	Model       string
	UsedPercent float64
	ResetsAt    time.Time
}

type QuotaReader func(ctx context.Context, agent string) ([]Window, error)

// Rate is the measured cost of one million tokens in percentage points of
// each window.
type Rate struct {
	WeeklyPerMTok float64 `json:"weekly_per_mtok"`
	WindowPerMTok float64 `json:"window_per_mtok"`
}

// CellCost is a cell's cost in percentage points of each window.
type CellCost struct {
	WeeklyPct float64
	WindowPct float64
}

// Decision is the admission of one cell: Admit, a pause until Wait, or a
// stop for the reason in Stop.
type Decision struct {
	Admit bool
	Wait  time.Time
	Stop  string
}

func ratesPath(stateDir string) string { return filepath.Join(Dir(stateDir), RatesFile) }

// ReadRates returns the measured rates by agent; no file means no rates.
func ReadRates(stateDir string) (map[string]Rate, error) {
	data, err := os.ReadFile(ratesPath(stateDir))
	if os.IsNotExist(err) {
		return map[string]Rate{}, nil
	}
	if err != nil {
		return nil, err
	}
	rates := map[string]Rate{}
	if err := json.Unmarshal(data, &rates); err != nil {
		return nil, fmt.Errorf("calibration %s: %w", RatesFile, err)
	}
	return rates, nil
}

// WriteRates replaces rates.json through a temporary file, like Run.Save.
func WriteRates(stateDir string, r map[string]Rate) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	dir := Dir(stateDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rates-*.json")
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
	return store.Rename(tmp.Name(), ratesPath(stateDir))
}

// CostOf estimates a cell from the rate, or from the conservative bound
// without one, and never below the 90th percentile of measured once it holds
// at least 3 cells. A zero rate field or a missing token estimate measures
// nothing, so that field keeps the bound.
func CostOf(rate *Rate, tokens int64, measured []CellCost) CellCost {
	c := CellCost{WeeklyPct: BoundWeekly, WindowPct: BoundWindow}
	if rate != nil && tokens > 0 {
		mtok := float64(tokens) / 1e6
		if rate.WeeklyPerMTok > 0 {
			c.WeeklyPct = mtok * rate.WeeklyPerMTok
		}
		if rate.WindowPerMTok > 0 {
			c.WindowPct = mtok * rate.WindowPerMTok
		}
	}
	if len(measured) >= 3 {
		weekly := make([]float64, len(measured))
		window := make([]float64, len(measured))
		for i, m := range measured {
			weekly[i], window[i] = m.WeeklyPct, m.WindowPct
		}
		c.WeeklyPct = max(c.WeeklyPct, p90(weekly))
		c.WindowPct = max(c.WindowPct, p90(window))
	}
	return c
}

// p90 is the nearest-rank 90th percentile, so it is always a measured value.
func p90(values []float64) float64 {
	slices.Sort(values)
	return values[int(math.Ceil(0.9*float64(len(values))))-1]
}

// judgeFloor is the least share of the conservative bound a judge call or
// rewrite counts without a rate, however large the class's cells are.
const judgeFloor = 0.05

// JudgeCost estimates one judge call or statement rewrite from the rate, or
// without one from the conservative bound scaled by its high token estimate
// over cellTokens, a cell's high estimate in its class, and never below
// judgeFloor of the bound. Without a cell estimate it counts the bound.
func JudgeCost(rate *Rate, cellTokens int64) CellCost {
	scale := 1.0
	if cellTokens > 0 {
		scale = max(judgeFloor, float64(judgeTokensHigh)/float64(cellTokens))
	}
	c := CostOf(rate, judgeTokensHigh, nil)
	if rate == nil || rate.WeeklyPerMTok <= 0 {
		c.WeeklyPct = BoundWeekly * scale
	}
	if rate == nil || rate.WindowPerMTok <= 0 {
		c.WindowPct = BoundWindow * scale
	}
	return c
}

// Admit decides whether the agent may run a cell of the given cost. ws comes
// from a quota reading; without a weekly window (a failed read gives none)
// the run would be blind, so it stops. An agent without a five-hour window
// is admitted on the weekly rules alone. The lead reserve is kept against
// each reading plus the run's charges it may not show yet, so a provider
// that shows the spend late cannot let the run into the reserve. held is the
// part of the approved cap kept for judging: a step must leave it, and once
// the spend alone leaves too little of it the run stops for a new approval.
func Admit(agent string, ws []Window, cost CellCost, spend Spend, cfg config.Calibration, held float64, now time.Time) Decision {
	return admitShare(agent, ws, cost, spend, cfg, held, now, 1)
}

// admitShare is Admit on an account of which duolog may use the share u of
// each window. Readings, costs and spend stay in real percentage points of
// the whole window, as the agents report them; only the limits scale: the
// calibration caps are u of the configured ones, and the lead reserves are
// kept of the usable part, so a window's ceiling is (100 - reserve) * u.
func admitShare(agent string, ws []Window, cost CellCost, spend Spend, cfg config.Calibration, held float64, now time.Time, u float64) Decision {
	five := accountWindow(ws, KindFiveHour)
	if accountWindow(ws, KindWeekly) == nil {
		return Decision{Stop: "quota unavailable"}
	}
	for _, w := range ws {
		if w.UsedPercent >= 100 {
			return Decision{Stop: "provider limit"}
		}
	}
	for _, w := range ws {
		used := w.UsedPercent + spend.outstanding(agent, w)
		if w.Kind == KindWeekly && used+cost.WeeklyPct > (100-ReserveWeekly)*u ||
			w.Kind == KindFiveHour && used+cost.WindowPct > (100-ReserveWindow)*u {
			return Decision{Stop: "lead reserve"}
		}
	}
	capWeekly := cfg.MaxWeeklyShare * 100 * u
	if held > 0 && spend.WeeklyPct[agent]+held > capWeekly {
		return Decision{Stop: "re-approve"}
	}
	if spend.WeeklyPct[agent]+cost.WeeklyPct+held > capWeekly {
		return Decision{Stop: "approved cap"}
	}
	if five != nil {
		if spend.Own(agent, now)+cost.WindowPct > cfg.MaxWindowShare*100*u {
			wait := five.ResetsAt
			if wait.IsZero() {
				wait = now.Add(fiveHours)
			}
			return Decision{Wait: wait}
		}
	}
	return Decision{Admit: true}
}

// Window lengths for a window that reports no reset time.
const (
	fiveHours = 5 * time.Hour
	week      = 7 * 24 * time.Hour
)

// accountWindow is the first of the account's windows of kind, nil without one.
func accountWindow(ws []Window, kind string) *Window {
	i := slices.IndexFunc(ws, func(w Window) bool { return w.Kind == kind && w.Model == "" })
	if i < 0 {
		return nil
	}
	return &ws[i]
}

// forModel keeps the account's windows and the model windows that may limit
// family: its own, and internal/quota's groups that name no one family.
func forModel(ws []Window, family string) []Window {
	return slices.DeleteFunc(slices.Clone(ws), func(w Window) bool {
		return w.Model != "" && w.Model != family && w.Model != "codex" && w.Model != "other"
	})
}

// levelKey names one window of an agent in Spend.Levels.
func levelKey(agent string, w Window) string {
	key := agent + "/" + w.Kind
	if w.Model != "" {
		key += "/" + w.Model
	}
	return key
}

// Charge is one step's cost on one window that the readings may not show
// yet. Ended says the step is over, and Base is the reading taken then.
type Charge struct {
	Pct   float64 `json:"pct"`
	Ended bool    `json:"ended,omitempty"`
	Base  float64 `json:"base,omitempty"`
}

// Outstanding are a window's charges in the order the steps started.
// Cleared is the level the last charge taken off needed the reading to reach.
type Outstanding struct {
	Cleared float64  `json:"cleared,omitempty"`
	Charges []Charge `json:"charges,omitempty"`
}

// outstanding is the sum of the run's charges on w's window that the
// readings do not show yet, 0 when the window has reset since.
func (s Spend) outstanding(agent string, w Window) float64 {
	key := levelKey(agent, w)
	l, ok := s.Levels[key]
	if !ok || w.ResetsAt.After(l.ResetsAt.Add(resetSlack)) {
		return 0
	}
	var sum float64
	for _, c := range s.Outstanding[key].Charges {
		sum += c.Pct
	}
	return sum
}

// Own is the run's own share of the agent's current five-hour window: how
// far the readings rose since its first one, whoever spent it, plus the
// charges they do not show yet. A window that has reset holds none of it.
func (s Spend) Own(agent string, now time.Time) float64 {
	w, ok := s.Windows[agent]
	if !ok || !now.Before(w.ResetsAt) {
		return 0
	}
	var sum float64
	for _, c := range s.Outstanding[agent+"/"+KindFiveHour].Charges {
		sum += c.Pct
	}
	return w.LastUsed - w.StartUsed + sum
}

// WithResets gives each window that reports no reset time one, so its spend
// is counted and a pause has an end: the reset given to that window before
// while it lies ahead, else now plus the window's length.
func (s Spend) WithResets(agent string, ws []Window, now time.Time) []Window {
	out := slices.Clone(ws)
	for i, w := range out {
		if !w.ResetsAt.IsZero() {
			continue
		}
		length := fiveHours
		if w.Kind == KindWeekly {
			length = week
		}
		if l, ok := s.Levels[levelKey(agent, w)]; ok && now.Before(l.ResetsAt) {
			out[i].ResetsAt = l.ResetsAt
		} else {
			out[i].ResetsAt = now.Add(length)
		}
	}
	return out
}

// Track records the agent's readings: the account's five-hour window in
// Windows, for the run's own share, and every window in Levels, for the
// reserve. The first reading of a window, including the first after
// ResetsAt, starts it at the reading and drops its outstanding charges. A
// later reading takes charges off in order: a charge only once its step has
// ended and the reading has risen by the charge above the reading at the end
// of the step and above what the charges before it took. A rise that cannot
// be told apart from the lead's own work therefore keeps the charge, so
// unknown spend counts against calibration.
func (s *Spend) Track(agent string, ws []Window) {
	track := func(m map[string]WindowSpend, key string, w Window) bool {
		cur, ok := m[key]
		if !ok || w.ResetsAt.After(cur.ResetsAt.Add(resetSlack)) {
			m[key] = WindowSpend{ResetsAt: w.ResetsAt, StartUsed: w.UsedPercent, LastUsed: w.UsedPercent}
			return true
		}
		cur.LastUsed = w.UsedPercent
		m[key] = cur
		return false
	}
	for _, w := range ws {
		if s.Levels == nil {
			s.Levels = map[string]WindowSpend{}
		}
		key := levelKey(agent, w)
		if track(s.Levels, key, w) {
			delete(s.Outstanding, key)
			continue
		}
		o, ok := s.Outstanding[key]
		if !ok {
			continue
		}
		for len(o.Charges) > 0 && o.Charges[0].Ended {
			c := o.Charges[0]
			level := max(c.Base, o.Cleared) + c.Pct
			if w.UsedPercent < level {
				break
			}
			o.Cleared, o.Charges = level, o.Charges[1:]
		}
		s.Outstanding[key] = o
	}
	if five := accountWindow(ws, KindFiveHour); five != nil {
		if s.Windows == nil {
			s.Windows = map[string]WindowSpend{}
		}
		track(s.Windows, agent, *five)
	}
}

// Raise counts a step of the given cost that is about to start: its weekly
// estimate toward the approved cap, and its cost as a charge on every window
// of ws that Track recorded, which the reserve and the run's own five-hour
// share count until a reading shows it. Settling the step later replaces the
// weekly estimate with a larger measured rise.
func (s *Spend) Raise(agent string, ws []Window, cost CellCost) {
	if s.WeeklyPct == nil {
		s.WeeklyPct = map[string]float64{}
	}
	s.WeeklyPct[agent] += cost.WeeklyPct
	for _, w := range ws {
		key := levelKey(agent, w)
		if _, ok := s.Levels[key]; !ok {
			continue
		}
		pct := cost.WindowPct
		if w.Kind == KindWeekly {
			pct = cost.WeeklyPct
		}
		if s.Outstanding == nil {
			s.Outstanding = map[string]Outstanding{}
		}
		o := s.Outstanding[key]
		o.Charges = append(o.Charges, Charge{Pct: pct})
		s.Outstanding[key] = o
	}
}

// Release takes back the charge Raise made with ws and cost for a step whose
// worker never started: its weekly estimate and its open charge on each
// window. Steps run one at a time, so the step's charge is the last one
// still open.
func (s *Spend) Release(agent string, ws []Window, cost CellCost) {
	if _, ok := s.WeeklyPct[agent]; ok {
		s.WeeklyPct[agent] = max(0, s.WeeklyPct[agent]-cost.WeeklyPct)
	}
	for _, w := range ws {
		key := levelKey(agent, w)
		o, ok := s.Outstanding[key]
		if n := len(o.Charges); !ok || n == 0 || o.Charges[n-1].Ended {
			continue
		}
		o.Charges = slices.Clone(o.Charges[:len(o.Charges)-1])
		s.Outstanding[key] = o
	}
}

// End marks the agent's steps that are still open as over, with the reading
// in ws as the base their charges are taken off from. Steps run one at a
// time, so a step still open when the next is admitted is over too.
func (s *Spend) End(agent string, ws []Window) {
	for _, w := range ws {
		key := levelKey(agent, w)
		o, ok := s.Outstanding[key]
		if !ok {
			continue
		}
		o.Charges = slices.Clone(o.Charges)
		for i, c := range o.Charges {
			if !c.Ended {
				o.Charges[i] = Charge{Pct: c.Pct, Ended: true, Base: w.UsedPercent}
			}
		}
		s.Outstanding[key] = o
	}
}

// QuotaCache keeps each agent's last reading for QuotaCacheTTL. A failed
// read is not cached, and it drops the agent's earlier reading: that one no
// longer says how much is left.
//
// With Shared set it keeps nothing itself: the account gate's route-aware
// cache is the one authority on how old a reading may be, and Get passes
// fresh on to it. Read is then unused.
type QuotaCache struct {
	Read   QuotaReader
	Shared func(ctx context.Context, agent string, fresh bool) ([]Window, error)
	Now    func() time.Time

	mu      sync.Mutex
	entries map[string]quotaEntry
}

type quotaEntry struct {
	at time.Time
	ws []Window
}

// Get returns the agent's windows, reading them again when fresh is set or
// the cached reading is older than QuotaCacheTTL.
func (q *QuotaCache) Get(ctx context.Context, agent string, fresh bool) ([]Window, error) {
	if q.Shared != nil {
		return q.Shared(ctx, agent, fresh)
	}
	now := time.Now
	if q.Now != nil {
		now = q.Now
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if e, ok := q.entries[agent]; ok && !fresh && now().Sub(e.at) <= QuotaCacheTTL {
		return slices.Clone(e.ws), nil
	}
	ws, err := q.Read(ctx, agent)
	if err != nil {
		delete(q.entries, agent)
		return nil, err
	}
	if q.entries == nil {
		q.entries = map[string]quotaEntry{}
	}
	q.entries[agent] = quotaEntry{at: now(), ws: slices.Clone(ws)}
	return ws, nil
}
