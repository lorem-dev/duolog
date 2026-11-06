package calibration

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// PlanFile is the costed plan of a run, kept in its folder until approved.
const PlanFile = "plan.json"

// inputShare is the share of a cell's tokens priced as input; the rest is
// output.
const inputShare = 0.85

// sampleRunes bounds a sample statement in the plan.
const sampleRunes = 160

const (
	judgesNote = "blind pairwise; cross-vendor pairs judged by both vendors; " +
		"same-vendor pairs by the other vendor; swapped order for close or " +
		"disputed pairs; each call admitted and charged as a cell is; " +
		"the judges and rubrics stay as approved; you confirm every verdict"
	contaminationNote = "older public history may be known to the models; " +
		"recent tasks first; statements never hold the sha or commit message; " +
		"%d of %d tasks are older than 90 days"
)

// roleOf maps a task's output feature to the role it replays, as Classify does.
var roleOf = map[string]string{"code": "implement", "tests": "tests", "docs": "docs"}

type PlanEntry struct {
	Key        string
	Challenger bool
}

// Share is the part of each window of one agent the cells take, in
// percentage points; Unknown without a measured rate. Cells counts the
// agent's steps left to run: cells, judge calls and rewrites. Bound is their
// weekly points at the conservative bound, which counts without a rate.
type Share struct {
	Unknown                                      bool
	WeeklyLow, WeeklyHigh, WindowLow, WindowHigh float64
	Cells                                        int
	Bound                                        float64
}

type ClassPlan struct {
	Class, Role           string
	Tasks, Round2         []string
	Entries               []PlanEntry
	Cells                 int
	TokensLow, TokensHigh int64
	USDLow, USDHigh       float64
	Share                 map[string]Share // agent
	Minutes               float64
	Samples               []string // up to 3 statements
	// JudgeCalls are the judge calls per agent the plan costs: every pair of
	// entries passes every task and every judge sees it once, and the order
	// is swapped for swappedShare of the tasks. JudgeCallsMax swaps it for
	// every task, the most the class can take. Rewrites are the class's
	// statements the first judge rewrites. Calls and rewrites are costed in
	// JudgeTokens, USD and Share, and their weekly points per agent are in
	// JudgeShare.
	JudgeCalls                      map[string]int
	JudgeCallsMax                   map[string]int
	Rewrites                        int
	JudgeTokensLow, JudgeTokensHigh int64
	JudgeShare                      map[string]float64
	// Done counts the cells of an earlier approval that ran; their actual
	// tokens and USD are in the totals, and their spend is in Plan.Spent.
	Done int
	// AgentUSD is the high USD of each agent's cells still to run, and
	// JudgeUSD that of its judge calls and rewrites: what a paid route of the
	// agent would spend. Empty in plans made before paid budgets.
	AgentUSD map[string]float64 `json:",omitempty"`
	JudgeUSD map[string]float64 `json:",omitempty"`
}

// judgeTokensLow and judgeTokensHigh estimate one judge call or statement
// rewrite: it reads the statement and up to three patches and writes a
// short answer. swappedShare is the share of a pair's tasks the plan expects
// to be close or disputed, and so judged again with the order swapped.
const (
	judgeTokensLow  = 20000
	judgeTokensHigh = 80000
	swappedShare    = 0.5
)

// Plan is what the person approves once for the whole run; its Hash covers
// everything else in it.
type Plan struct {
	Run                    string
	Classes                []ClassPlan
	CapWeekly              float64 // MaxWeeklyShare*100
	CapWindow              float64
	Pilot                  bool
	StopRules              []string
	Contamination          string
	Judges                 string
	CellMinutes, CellTurns int
	Checks                 map[string][]string // class checks at plan time; part of the hash
	RepoFiles, HistoryDays int
	CheckSeconds           float64
	Estimates              map[string]Estimate
	Cut                    []string // what was cut to fit the cap, in order
	// Observed is the done cells' tokens over their high estimates; above 1
	// it scales the estimates of the cells left. Spent is each agent's
	// weekly spend of the run so far, which counts toward the cap.
	Observed float64
	Spent    map[string]float64
	Rewriter string // the agent that rewrites statements
	// JudgeShare is each agent's weekly points the plan keeps for its judge
	// calls and rewrites; cells leave it free. JudgeTargets are the judges in
	// order as agent/model/effort, and RubricHash the hash of the planned
	// classes' rubrics: judging keeps to them until a new approval.
	JudgeShare   map[string]float64
	JudgeTargets []string
	RubricHash   string
	// Billing is how each agent the plan uses is billed, with the USD a paid
	// route is approved to spend; LimitPaidPlan records it. An old plan has
	// none, and keeps its hash.
	Billing map[string]BillingBudget `json:",omitempty"`
	Hash    string
}

type PlanInput struct {
	Cfg      *config.Config
	Manifest Manifest
	Rates    map[string]Rate
	Quota    map[string][]Window
	// Billing is each agent's budget from the account gate; an agent without
	// one plans as a personal subscription.
	Billing map[string]BillingBudget
	RunID   string
	// Ages are the days since each task's commit; a task without one counts
	// as recent.
	Ages map[string]int
	// Cells and Spent are the run's cells and weekly spend when it is planned
	// again: cells that ran keep their actual cost and count no more.
	Cells []Cell
	Spent map[string]float64
}

// BuildPlan costs the manifest's tasks on each class ladder plus at most one
// challenger, and cuts the plan until no agent's weekly high estimate passes
// the cap.
func BuildPlan(in PlanInput) (Plan, error) {
	if in.Cfg == nil {
		return Plan{}, errors.New("calibration plan: no configuration")
	}
	cfg := in.Cfg
	m := in.Manifest
	p := Plan{
		Run:          in.RunID,
		CapWeekly:    percent(cfg.Calibration.MaxWeeklyShare),
		CapWindow:    percent(cfg.Calibration.MaxWindowShare),
		Judges:       judgesNote,
		CellMinutes:  cfg.Calibration.CellMaxMinutes,
		CellTurns:    cfg.Calibration.CellMaxTurns,
		Checks:       map[string][]string{},
		RepoFiles:    m.RepoFiles,
		HistoryDays:  m.HistoryDays,
		CheckSeconds: m.CheckSeconds,
		Estimates:    map[string]Estimate{},
		Observed:     observed(in),
	}
	for agent, spent := range in.Spent {
		if spent > 0 {
			if p.Spent == nil {
				p.Spent = map[string]float64{}
			}
			p.Spent[agent] = spent
		}
	}
	p.StopRules = []string{
		fmt.Sprintf("lead reserve: %s%% weekly, %s%% five-hour", number(ReserveWeekly), number(ReserveWindow)),
		"provider limit",
		fmt.Sprintf("approved cap: %s%% of each weekly window", number(p.CapWeekly)),
		fmt.Sprintf("five-hour window: pause at %s%% of calibration's own share until reset", number(p.CapWindow)),
		"pilot estimate above 1.5 x plan: stop and approve again",
	}

	tasks := map[string]ManifestTask{}
	byClass := map[string]*ClassPlan{}
	for _, task := range m.Tasks {
		tasks[task.ID] = task
		c := byClass[task.Class]
		if c == nil {
			c = &ClassPlan{Class: task.Class}
			byClass[task.Class] = c
		}
		if slices.Contains(m.Round2, task.ID) {
			c.Round2 = append(c.Round2, task.ID)
		} else {
			c.Tasks = append(c.Tasks, task.ID)
		}
	}
	var classes []*ClassPlan
	for _, name := range slices.Sorted(maps.Keys(byClass)) {
		c := byClass[name]
		c.Role = classRole(cfg, name, tasks[slices.Concat(c.Tasks, c.Round2)[0]])
		_, ladder := cfg.Learning.Ladder(name, c.Role)
		if len(ladder) == 0 {
			return Plan{}, fmt.Errorf("calibration plan: class %s has no ladder for role %s", name, c.Role)
		}
		for _, e := range ladder {
			c.Entries = append(c.Entries, PlanEntry{Key: e.Key()})
		}
		if challenger, ok := challengerFor(ladder, cfg.Learning.Judges); ok {
			c.Entries = append(c.Entries, PlanEntry{Key: challenger.Key(), Challenger: true})
		}
		classes = append(classes, c)
	}

	ran := ranCells(in.Cells)
	measure := func() {
		for _, c := range classes {
			measureClass(c, in, ran, max(1, p.Observed))
		}
	}
	// Without a rate a cell, judge call or rewrite counts at the conservative
	// bound admission uses, so the run does not stop at the cap with steps
	// left.
	// An agent is over when its steps pass what its route allows: the weekly
	// cap of its account's usable share, and on paid overage also the room
	// below the lead reserve; on a paid route, which takes no window, the USD
	// its cap leaves, judge calls and rewrites included.
	over := func() (map[string]bool, bool) {
		weekly, usd := map[string]float64{}, map[string]float64{}
		for _, c := range classes {
			for agent, s := range c.Share {
				if s.Unknown {
					weekly[agent] += s.Bound
				} else {
					weekly[agent] += s.WeeklyHigh
				}
			}
			for agent, v := range c.AgentUSD {
				usd[agent] += v
			}
			for agent, v := range c.JudgeUSD {
				usd[agent] += v
			}
		}
		agents := map[string]bool{}
		for agent, high := range weekly {
			b := in.Billing[agent]
			if b.paid() {
				continue
			}
			u := b.usable()
			if high+p.Spent[agent] > p.CapWeekly*u {
				agents[agent] = true
			}
			if room, ok := overageRoom(in.Quota, agent, u); ok && b.Verdict == VerdictOverage && high > room+usdSlack {
				agents[agent] = true
			}
		}
		for agent, v := range usd {
			if b := in.Billing[agent]; b.paid() && b.opted() && v > b.remaining()+usdSlack {
				agents[agent] = true
			}
		}
		return agents, len(agents) > 0
	}
	cut := func(line string) {
		p.Cut = append(p.Cut, line)
		measure()
	}
	measure()

	type ref struct {
		class *ClassPlan
		id    string
	}
	largestFirst := func(refs []ref) {
		slices.SortStableFunc(refs, func(a, b ref) int {
			ea, eb := m.Estimates[a.id], m.Estimates[b.id]
			return cmp.Or(cmp.Compare(eb.TokensHigh, ea.TokensHigh), cmp.Compare(eb.Lines, ea.Lines), strings.Compare(a.id, b.id))
		})
	}
	var round2 []ref
	for _, c := range classes {
		for _, id := range c.Round2 {
			round2 = append(round2, ref{c, id})
		}
	}
	largestFirst(round2)
	for _, r := range round2 {
		if _, ok := over(); !ok {
			break
		}
		r.class.Round2 = slices.DeleteFunc(r.class.Round2, func(id string) bool { return id == r.id })
		cut(fmt.Sprintf("round-2 task %s (%s)", r.id, r.class.Class))
	}
	// A challenger is cut only when its own agent is over the cap.
	for _, c := range classes {
		agents, ok := over()
		if !ok {
			break
		}
		i := slices.IndexFunc(c.Entries, func(e PlanEntry) bool { return e.Challenger })
		if i < 0 || !agents[entryAgent(c.Entries[i].Key)] {
			continue
		}
		key := c.Entries[i].Key
		c.Entries = slices.Delete(c.Entries, i, i+1)
		cut(fmt.Sprintf("challenger %s (%s)", key, c.Class))
	}
	var large []ref
	for _, c := range classes {
		for _, id := range c.Tasks {
			if m.Estimates[id].Large {
				large = append(large, ref{c, id})
			}
		}
	}
	largestFirst(large)
	for _, r := range large {
		if _, ok := over(); !ok {
			break
		}
		r.class.Tasks = slices.DeleteFunc(r.class.Tasks, func(id string) bool { return id == r.id })
		cut(fmt.Sprintf("large task %s (%s)", r.id, r.class.Class))
	}
	byValid := slices.Clone(classes)
	slices.SortStableFunc(byValid, func(a, b *ClassPlan) int {
		return cmp.Or(cmp.Compare(m.Counts[a.Class].Valid, m.Counts[b.Class].Valid), strings.Compare(a.Class, b.Class))
	})
	for _, c := range byValid {
		if _, ok := over(); !ok {
			break
		}
		classes = slices.DeleteFunc(classes, func(x *ClassPlan) bool { return x == c })
		cut(fmt.Sprintf("class %s (%d valid tasks)", c.Class, m.Counts[c.Class].Valid))
	}

	planned, older := 0, 0
	var names []string
	for _, c := range classes {
		if len(c.Tasks) == 0 {
			continue
		}
		names = append(names, c.Class)
		for agent, pct := range c.JudgeShare {
			if p.JudgeShare == nil {
				p.JudgeShare = map[string]float64{}
			}
			p.JudgeShare[agent] += pct
		}
		ids := slices.Concat(c.Tasks, c.Round2)
		for _, id := range ids {
			p.Estimates[id] = m.Estimates[id]
			planned++
			if in.Ages[id] > recentDays {
				older++
			}
			if len(c.Samples) < 3 {
				c.Samples = append(c.Samples, sample(tasks[id].Statement))
			}
		}
		for _, s := range c.Share {
			p.Pilot = p.Pilot || s.Unknown
		}
		p.Checks[c.Class] = slices.Clone(cfg.Learning.Classes[c.Class].Checks)
		if c.Rewrites > 0 {
			p.Rewriter = cfg.Learning.Judges[0].Agent
		}
		p.Classes = append(p.Classes, *c)
	}
	p.Contamination = fmt.Sprintf(contaminationNote, older, planned)
	p.JudgeTargets, p.RubricHash = judgeSetup(cfg, names)
	p.Hash = p.ComputeHash()
	if len(in.Billing) == 0 {
		return p, nil
	}
	return LimitPaidPlan(p, in.Billing)
}

// judgeSetup is what judging the classes depends on: each judge in order as
// agent/model/effort, and the hash of the classes' rubrics.
func judgeSetup(cfg *config.Config, classes []string) (judges []string, rubric string) {
	for _, j := range cfg.Learning.Judges {
		judges = append(judges, targetKey(j.Target()))
	}
	rubrics := map[string][]string{}
	for _, class := range classes {
		rubrics[class] = cfg.Learning.Classes[class].Rubric
	}
	return judges, hashOf(rubrics)
}

// CheckJudges refuses a config whose judges or rubrics differ from the ones
// p was approved with; a plan that recorded none passes.
func (p Plan) CheckJudges(cfg *config.Config) error {
	if p.RubricHash == "" {
		return nil
	}
	var classes []string
	for _, c := range p.Classes {
		classes = append(classes, c.Class)
	}
	judges, rubric := judgeSetup(cfg, classes)
	if !slices.Equal(judges, p.JudgeTargets) {
		return errors.New("judges changed since approval; plan again")
	}
	if rubric != p.RubricHash {
		return errors.New("judge rubrics changed since approval; plan again")
	}
	return nil
}

// hashOf is the sha256 of v's JSON; JSON sorts map keys.
func hashOf(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// classTokens is the mean high token estimate of a cell of class among the
// manifest's tasks, 0 without one.
func classTokens(m Manifest, class string) int64 {
	var sum, n int64
	for _, t := range m.Tasks {
		if e := m.Estimates[t.ID]; t.Class == class && e.TokensHigh > 0 {
			sum, n = sum+e.TokensHigh, n+1
		}
	}
	if n == 0 {
		return 0
	}
	return sum / n
}

// classRole is the role the class's tasks replay: the one their output
// gives, or the class's first role when the class does not route that one.
func classRole(cfg *config.Config, class string, task ManifestTask) string {
	roles := cfg.Learning.Classes[class].Roles
	if role := roleOf[task.Features["output"]]; role != "" && (len(roles) == 0 || slices.Contains(roles, role)) {
		return role
	}
	if len(roles) > 0 {
		return roles[0]
	}
	return "implement"
}

// challengerFor is the first judge of the other agent when every ladder
// entry has one agent.
func challengerFor(ladder []config.LadderEntry, judges []config.LadderEntry) (config.LadderEntry, bool) {
	agent := ladder[0].Agent
	for _, e := range ladder {
		if e.Agent != agent {
			return config.LadderEntry{}, false
		}
	}
	for _, j := range judges {
		if j.Agent != agent {
			return j, true
		}
	}
	return config.LadderEntry{}, false
}

// HasAgent reports whether any class of p runs an entry of agent.
func (p Plan) HasAgent(agent string) bool {
	for _, c := range p.Classes {
		if slices.ContainsFunc(c.Entries, func(e PlanEntry) bool { return entryAgent(e.Key) == agent }) {
			return true
		}
	}
	return false
}

// HasJudge reports whether p may give agent a judge call or a rewrite.
func (p Plan) HasJudge(agent string) bool {
	return p.Rewriter == agent || slices.ContainsFunc(p.Classes, func(c ClassPlan) bool { return c.JudgeCalls[agent] > 0 })
}

func entryAgent(key string) string {
	agent, _, _ := strings.Cut(key, "/")
	return agent
}

// measureClass sums the cells of c: tokens, USD, minutes and each agent's
// share of its windows, then the judge calls and rewrites the class is
// expected to take. A cell in ran counts its actual tokens and USD and no
// share, as the run's spend holds it; the estimates of the other cells are
// scaled by factor. Without a rate a judge call or rewrite counts as
// JudgeCost of the class's cells.
func measureClass(c *ClassPlan, in PlanInput, ran map[string]Cell, factor float64) {
	ids := slices.Concat(c.Tasks, c.Round2)
	c.Cells = len(ids) * len(c.Entries)
	c.TokensLow, c.TokensHigh, c.USDLow, c.USDHigh, c.Minutes = 0, 0, 0, 0, 0
	c.Done, c.Rewrites, c.JudgeTokensLow, c.JudgeTokensHigh = 0, 0, 0, 0
	c.Share = map[string]Share{}
	c.JudgeCalls, c.JudgeCallsMax, c.JudgeShare = map[string]int{}, map[string]int{}, map[string]float64{}
	c.AgentUSD, c.JudgeUSD = nil, nil
	// addUSD adds high USD to an agent's cells or judge calls; a plan without
	// billing keeps neither, so its JSON and hash stay as they were.
	addUSD := func(m *map[string]float64, agent string, usd float64) {
		if len(in.Billing) == 0 || usd <= 0 {
			return
		}
		if *m == nil {
			*m = map[string]float64{}
		}
		(*m)[agent] += usd
	}
	cellTokens := classTokens(in.Manifest, c.Class)
	// share adds n steps of tokens low to high, each bound weekly points at
	// the conservative bound, to the agent's share.
	share := func(agent string, n int, low, high int64, bound float64) {
		s := c.Share[agent]
		rate, known := in.Rates[agent]
		s.Unknown = s.Unknown || !known
		s.Cells += n
		s.Bound += float64(n) * bound
		if known {
			l, h := CostOf(&rate, low, nil), CostOf(&rate, high, nil)
			s.WeeklyLow += float64(n) * l.WeeklyPct
			s.WeeklyHigh += float64(n) * h.WeeklyPct
			if hasFiveHour(in.Quota, agent) {
				s.WindowLow += float64(n) * l.WindowPct
				s.WindowHigh += float64(n) * h.WindowPct
			}
		}
		c.Share[agent] = s
	}
	for _, e := range c.Entries {
		parts := strings.SplitN(e.Key, "/", 3)
		agent, model := parts[0], parts[1]
		for _, id := range ids {
			if done, ok := ran[cellKey(c.Class, id, e.Key)]; ok {
				c.Done++
				c.TokensLow, c.TokensHigh = c.TokensLow+done.Tokens, c.TokensHigh+done.Tokens
				c.USDLow, c.USDHigh = c.USDLow+done.USD, c.USDHigh+done.USD
				continue
			}
			est := in.Manifest.Estimates[id]
			low, high := scale(est.TokensLow, factor), scale(est.TokensHigh, factor)
			c.TokensLow += low
			c.TokensHigh += high
			c.USDLow += cellUSD(in.Cfg, agent, model, low)
			c.USDHigh += cellUSD(in.Cfg, agent, model, high)
			addUSD(&c.AgentUSD, agent, cellUSD(in.Cfg, agent, model, high))
			c.Minutes += est.Minutes + est.CheckSeconds/60
			share(agent, 1, low, high, BoundWeekly)
		}
	}
	judge := func(target config.Target, n int) {
		c.JudgeTokensLow += int64(n) * judgeTokensLow
		c.JudgeTokensHigh += int64(n) * judgeTokensHigh
		c.USDLow += float64(n) * cellUSD(in.Cfg, target.Agent, target.Model, judgeTokensLow)
		c.USDHigh += float64(n) * cellUSD(in.Cfg, target.Agent, target.Model, judgeTokensHigh)
		addUSD(&c.JudgeUSD, target.Agent, float64(n)*cellUSD(in.Cfg, target.Agent, target.Model, judgeTokensHigh))
		var rate *Rate
		if r, ok := in.Rates[target.Agent]; ok {
			rate = &r
		}
		cost := JudgeCost(rate, cellTokens)
		share(target.Agent, n, judgeTokensLow, judgeTokensHigh, cost.WeeklyPct)
		c.JudgeShare[target.Agent] += float64(n) * cost.WeeklyPct
	}
	swapped := int(math.Ceil(swappedShare * float64(len(ids))))
	for i, a := range c.Entries {
		ea, err := config.ParseEntryKey(a.Key)
		if err != nil {
			continue
		}
		for _, b := range c.Entries[i+1:] {
			eb, err := config.ParseEntryKey(b.Key)
			if err != nil {
				continue
			}
			for _, target := range JudgeTargets(in.Cfg.Learning, ea.Target(), eb.Target()) {
				c.JudgeCalls[target.Agent] += len(ids) + swapped
				c.JudgeCallsMax[target.Agent] += 2 * len(ids)
				judge(target, len(ids)+swapped)
			}
		}
	}
	if judges := in.Cfg.Learning.Judges; len(judges) > 0 {
		for _, id := range ids {
			if slices.Contains(in.Manifest.Rewrite, id) {
				c.Rewrites++
			}
		}
		if c.Rewrites > 0 {
			judge(judges[0].Target(), c.Rewrites)
		}
	}
	for agent, s := range c.Share {
		if s.Unknown {
			c.Share[agent] = Share{Unknown: true, Cells: s.Cells, Bound: s.Bound}
		}
	}
}

func scale(tokens int64, factor float64) int64 { return int64(math.Round(float64(tokens) * factor)) }

func cellKey(class, task, entry string) string { return class + "\x00" + task + "\x00" + entry }

// Ran reports whether the cell ran to an end: its spend is in the run's,
// and a new approval keeps it as it is.
func (c Cell) Ran() bool {
	return c.State == CellDone || c.State == CellFailedLimit || c.State == CellLeakage || c.State == CellSetupChanged
}

// ranCells indexes the cells that ran by class, task and entry.
func ranCells(cells []Cell) map[string]Cell {
	out := map[string]Cell{}
	for _, c := range cells {
		if c.Ran() {
			out[cellKey(c.Class, c.Task, c.Entry)] = c
		}
	}
	return out
}

// observed is the done cells' tokens over their tasks' high estimates; 0
// without a done cell that has both.
func observed(in PlanInput) float64 {
	var tokens, high int64
	for _, c := range in.Cells {
		if e := in.Manifest.Estimates[c.Task]; c.State == CellDone && c.Tokens > 0 && e.TokensHigh > 0 {
			tokens, high = tokens+c.Tokens, high+e.TokensHigh
		}
	}
	if high == 0 {
		return 0
	}
	return float64(tokens) / float64(high)
}

// Approve records the approval of p. A run without cells gets the plan's
// cells. A run planned again keeps each cell that ran, with its id, files
// and figures, gives the plan's other cells pending ones, reusing the id of
// a cell that did not run, and skips the pending cells the plan left out.
// The plan approved already is refused, so approving twice cannot reset a
// cell.
func Approve(run *Run, p Plan, now time.Time) error {
	if run.Approved != nil && run.Approved.Hash == p.Hash {
		return fmt.Errorf("plan %s is already approved; see duolog calibrate status", p.Hash)
	}
	planned := Cells(p, run.Seed)
	if len(run.Cells) > 0 {
		prior := map[string]Cell{}
		for _, c := range run.Cells {
			k := cellKey(c.Class, c.Task, c.Entry)
			if old, ok := prior[k]; !ok || c.Ran() && !old.Ran() {
				prior[k] = c
			}
		}
		next := nextCellNumber(run.Cells)
		used := map[string]bool{}
		var cells []Cell
		for _, c := range planned {
			if old, ok := prior[cellKey(c.Class, c.Task, c.Entry)]; ok && !used[old.ID] {
				used[old.ID] = true
				if old.Ran() {
					cells = append(cells, old)
					continue
				}
				c.ID = old.ID
			} else {
				c.ID = fmt.Sprintf("c%03d", next)
				next++
			}
			cells = append(cells, c)
		}
		for _, c := range run.Cells {
			if used[c.ID] {
				continue
			}
			if c.State == CellPending || c.State == CellRunning {
				c.State, c.Reason = CellSkipped, "not in the approved plan"
			}
			cells = append(cells, c)
		}
		planned = cells
	}
	run.Cells = planned
	run.Approved = &Approval{Hash: p.Hash, Time: now.UTC()}
	if run.Stopped == stopReapprove {
		run.Stopped = ""
	}
	return nil
}

// nextCellNumber is the number after the highest cNNN cell id.
func nextCellNumber(cells []Cell) int {
	next := len(cells) + 1
	for _, c := range cells {
		if n, err := strconv.Atoi(strings.TrimPrefix(c.ID, "c")); err == nil && n >= next {
			next = n + 1
		}
	}
	return next
}

// hasFiveHour reports whether the agent's quota reading has a five-hour
// window; without a reading it assumes one.
func hasFiveHour(quota map[string][]Window, agent string) bool {
	ws, ok := quota[agent]
	return !ok || accountWindow(ws, KindFiveHour) != nil
}

// cellUSD prices tokens as 85% input and 15% output; an unpriced model
// costs 0.
func cellUSD(cfg *config.Config, agent, model string, tokens int64) float64 {
	input := int64(inputShare * float64(tokens))
	usd, _ := usage.Cost(cfg, agent, model, usage.Tokens{Input: input, Output: tokens - input})
	return usd
}

// percent turns a share into percentage points without float noise.
func percent(share float64) float64 { return math.Round(share*100*1e6) / 1e6 }

func number(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// sample is the first line of a statement, cut to sampleRunes.
func sample(statement string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(statement), "\n")
	if r := []rune(line); len(r) > sampleRunes {
		line = string(r[:sampleRunes-3]) + "..."
	}
	return line
}

// ComputeHash is the sha256 of the plan's JSON with Hash empty; JSON sorts
// map keys, so equal plans give equal hashes.
func (p Plan) ComputeHash() string {
	p.Hash = ""
	return hashOf(p)
}

// Cells orders the plan's cells: each class's round 1 cells shuffled with
// the seed, taken in turn from the classes sorted by name, then round 2 the
// same way.
func Cells(p Plan, seed int64) []Cell {
	rng := rand.New(rand.NewSource(seed))
	classes := slices.Clone(p.Classes)
	slices.SortStableFunc(classes, func(a, b ClassPlan) int { return strings.Compare(a.Class, b.Class) })
	var cells []Cell
	for round := 1; round <= 2; round++ {
		queues := make([][]Cell, len(classes))
		for i, c := range classes {
			tasks := c.Tasks
			if round == 2 {
				tasks = c.Round2
			}
			for _, task := range tasks {
				for _, e := range c.Entries {
					queues[i] = append(queues[i], Cell{Task: task, Class: c.Class, Entry: e.Key, Round: round, State: CellPending})
				}
			}
			rng.Shuffle(len(queues[i]), func(a, b int) { queues[i][a], queues[i][b] = queues[i][b], queues[i][a] })
		}
		for i := 0; ; i++ {
			added := false
			for _, q := range queues {
				if i < len(q) {
					cells = append(cells, q[i])
					added = true
				}
			}
			if !added {
				break
			}
		}
	}
	for i := range cells {
		cells[i].ID = fmt.Sprintf("c%03d", i+1)
	}
	return cells
}

// RenderPlan writes one table per class, then the rules, notes and figures
// the person needs to approve or cut the plan.
func RenderPlan(w io.Writer, p Plan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "calibration run %s: plan\n", p.Run)
	var cells int
	var tokensLow, tokensHigh int64
	var usdLow, usdHigh, minutes float64
	weekly := map[string][2]float64{}
	unknown := map[string]bool{}
	for _, c := range p.Classes {
		fmt.Fprintf(&b, "class %s (role %s)\n", c.Class, c.Role)
		fmt.Fprintf(&b, "  tasks:     %d round 1, %d round 2\n", len(c.Tasks), len(c.Round2))
		entries := make([]string, len(c.Entries))
		for i, e := range c.Entries {
			entries[i] = e.Key
			if e.Challenger {
				entries[i] += " (challenger)"
			}
		}
		fmt.Fprintf(&b, "  entries:   %s\n", strings.Join(entries, ", "))
		if c.Done > 0 {
			fmt.Fprintf(&b, "  cells:     %d (%d done)\n", c.Cells, c.Done)
		} else {
			fmt.Fprintf(&b, "  cells:     %d\n", c.Cells)
		}
		fmt.Fprintf(&b, "  tokens:    %d-%d\n", c.TokensLow, c.TokensHigh)
		calls, most := 0, 0
		for agent, n := range c.JudgeCalls {
			calls, most = calls+n, most+c.JudgeCallsMax[agent]
		}
		if calls > 0 {
			fmt.Fprintf(&b, "  judge calls: %d costed, up to %d if every pair is judged again in swapped order; %d-%d tokens with the rewrites\n",
				calls, most, c.JudgeTokensLow, c.JudgeTokensHigh)
		}
		if c.Rewrites > 0 {
			fmt.Fprintf(&b, "  rewrites:  %d\n", c.Rewrites)
		}
		fmt.Fprintf(&b, "  USD:       %.2f-%.2f equivalent\n", c.USDLow, c.USDHigh)
		for _, agent := range slices.Sorted(maps.Keys(c.Share)) {
			s := c.Share[agent]
			fmt.Fprintf(&b, "  share %s: %s\n", agent, shareText(s, p))
			if s.Unknown {
				unknown[agent] = true
			} else {
				t := weekly[agent]
				weekly[agent] = [2]float64{t[0] + s.WeeklyLow, t[1] + s.WeeklyHigh}
			}
		}
		fmt.Fprintf(&b, "  wall time: %.0f min\n", c.Minutes)
		checks := "none"
		if len(p.Checks[c.Class]) > 0 {
			checks = strings.Join(p.Checks[c.Class], "; ")
		}
		fmt.Fprintf(&b, "  checks:    %s\n", checks)
		cells += c.Cells
		tokensLow, tokensHigh = tokensLow+c.TokensLow, tokensHigh+c.TokensHigh
		usdLow, usdHigh, minutes = usdLow+c.USDLow, usdHigh+c.USDHigh, minutes+c.Minutes
	}
	fmt.Fprintf(&b, "total: %d cells, %d-%d tokens, %.2f-%.2f USD equivalent, wall time %.0f min\n", cells, tokensLow, tokensHigh, usdLow, usdHigh, minutes)
	for _, agent := range slices.Sorted(maps.Keys(weekly)) {
		if !unknown[agent] {
			fmt.Fprintf(&b, "total share %s: %.1f-%.1f%% weekly (cap %s%% weekly)\n", agent, weekly[agent][0], weekly[agent][1], number(p.CapWeekly))
		}
	}
	// Without a rate each cell counts at the bound, after what the run spent
	// so far and the share kept for judging.
	for _, agent := range slices.Sorted(maps.Keys(unknown)) {
		steps := max(0, int(math.Floor((p.CapWeekly-p.Spent[agent]-p.JudgeShare[agent])/BoundWeekly+1e-9)))
		fmt.Fprintf(&b, "total share %s: unknown, at most %d cells before the cap of %s%% weekly, at %s%% weekly each until a rate is measured; a judge call or rewrite counts less, by its tokens\n",
			agent, steps, number(p.CapWeekly), number(BoundWeekly))
	}
	for _, agent := range slices.Sorted(maps.Keys(p.JudgeShare)) {
		fmt.Fprintf(&b, "judge share %s: %s%% weekly, kept for judge calls and rewrites\n", agent, number(math.Round(p.JudgeShare[agent]*10)/10))
	}
	for _, line := range billingLines(p) {
		fmt.Fprintln(&b, line)
	}
	for _, agent := range slices.Sorted(maps.Keys(p.Spent)) {
		fmt.Fprintf(&b, "spent so far: %s %s%% weekly, counted toward the cap\n", agent, number(math.Round(p.Spent[agent]*10)/10))
	}
	if p.Observed > 0 {
		fmt.Fprintf(&b, "observed: the done cells used %.1f x their high estimate", p.Observed)
		if p.Observed > 1 {
			b.WriteString("; the cells left are estimated at that rate")
		}
		b.WriteString("\n")
	}
	if p.Pilot {
		b.WriteString("pilot: without a measured rate the run starts with 2 cells, then estimates again\n")
	}
	fmt.Fprintf(&b, "cell cap: %d min, %d turns\n", p.CellMinutes, p.CellTurns)
	b.WriteString("stop rules:\n")
	for _, rule := range p.StopRules {
		fmt.Fprintf(&b, "  %s\n", rule)
	}
	fmt.Fprintf(&b, "contamination: %s\n", p.Contamination)
	if len(p.JudgeTargets) > 0 {
		fmt.Fprintf(&b, "judges: %s; %s\n", strings.Join(p.JudgeTargets, ", "), p.Judges)
	} else {
		fmt.Fprintf(&b, "judges: %s\n", p.Judges)
	}
	b.WriteString("sample statements:\n")
	for _, c := range p.Classes {
		for _, s := range c.Samples {
			fmt.Fprintf(&b, "  %s: %s\n", c.Class, s)
		}
	}
	fmt.Fprintf(&b, "repository: %d tracked files\n", p.RepoFiles)
	fmt.Fprintf(&b, "history: %d days\n", p.HistoryDays)
	fmt.Fprintf(&b, "check time: %.1f s per run\n", p.CheckSeconds)
	b.WriteString("estimate per task and cell:\n")
	for _, c := range p.Classes {
		for round, ids := range [][]string{c.Tasks, c.Round2} {
			for _, id := range ids {
				e := p.Estimates[id]
				large := ""
				if e.Large {
					large = ", large"
				}
				fmt.Fprintf(&b, "  %s %s round %d: %d files, %d lines, %d-%d tokens, %.0f min%s\n",
					id, c.Class, round+1, e.Files, e.Lines, e.TokensLow, e.TokensHigh, e.Minutes, large)
			}
		}
	}
	if len(p.Cut) == 0 {
		b.WriteString("cut to fit the cap: nothing\n")
	} else {
		b.WriteString("cut to fit the cap:\n")
		for _, line := range p.Cut {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	fmt.Fprintf(&b, "approve with: duolog calibrate plan --approve %s\n", p.Hash)
	_, err := io.WriteString(w, b.String())
	return err
}

func shareText(s Share, p Plan) string {
	capText := fmt.Sprintf("(cap %s%% weekly)", number(p.CapWeekly))
	if s.Unknown {
		return "unknown, pilot first " + capText
	}
	text := fmt.Sprintf("%.1f-%.1f%% weekly %s", s.WeeklyLow, s.WeeklyHigh, capText)
	if s.WindowHigh == 0 {
		return text + ", no five-hour window"
	}
	return text + fmt.Sprintf(", %.1f-%.1f%% of five-hour windows (pause at %s%% of one)", s.WindowLow, s.WindowHigh, number(p.CapWindow))
}

// NextPlanFile holds a plan shown after an approval: plan.json keeps the
// approved one, which the run follows until the person approves this one.
const NextPlanFile = "plan-next.json"

// SavePlan replaces plan.json in runDir through a temporary file.
func SavePlan(runDir string, p Plan) error {
	return writeJSON(filepath.Join(runDir, PlanFile), p)
}

// SaveNextPlan replaces plan-next.json in runDir.
func SaveNextPlan(runDir string, p Plan) error {
	return writeJSON(filepath.Join(runDir, NextPlanFile), p)
}

func LoadPlan(runDir string) (Plan, error) { return loadPlan(runDir, PlanFile) }

// LoadPlanToApprove reads the plan shown last: plan-next.json, or plan.json
// without one.
func LoadPlanToApprove(runDir string) (Plan, error) {
	p, err := loadPlan(runDir, NextPlanFile)
	if os.IsNotExist(err) {
		return LoadPlan(runDir)
	}
	return p, err
}

func loadPlan(runDir, name string) (Plan, error) {
	var p Plan
	data, err := os.ReadFile(filepath.Join(runDir, name))
	if err != nil {
		return Plan{}, err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Plan{}, fmt.Errorf("calibration %s: %w", name, err)
	}
	return p, nil
}

// TaskAges reads the days since each task's commit from its task.json and
// Git; a task without either has no age.
func TaskAges(ctx context.Context, root, runDir string, m Manifest, now time.Time) map[string]int {
	ages := map[string]int{}
	for _, task := range m.Tasks {
		r, err := ReadTask(runDir, task.ID)
		if err != nil || r.Ref == "" {
			continue
		}
		out, err := run(ctx, root, "show", "-s", "--format=%ct", r.Ref, "--")
		if err != nil {
			continue
		}
		unix, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		if err != nil {
			continue
		}
		ages[task.ID] = int(now.Sub(time.Unix(unix, 0)).Hours() / 24)
	}
	return ages
}

// Limit is cfg with the caps the person approved in p wherever cfg's are
// looser: a changed config may make the run stricter without a new
// approval, never looser.
func (p Plan) Limit(cfg *config.Config) *config.Config {
	c := *cfg
	limit := &c.Calibration
	if p.CapWeekly > 0 {
		limit.MaxWeeklyShare = min(limit.MaxWeeklyShare, p.CapWeekly/100)
	}
	if p.CapWindow > 0 {
		limit.MaxWindowShare = min(limit.MaxWindowShare, p.CapWindow/100)
	}
	if p.CellMinutes > 0 {
		limit.CellMaxMinutes = min(limit.CellMaxMinutes, p.CellMinutes)
	}
	if p.CellTurns > 0 {
		limit.CellMaxTurns = min(limit.CellMaxTurns, p.CellTurns)
	}
	return &c
}
