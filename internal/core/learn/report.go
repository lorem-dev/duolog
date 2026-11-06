package learn

import (
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/core/usage"
)

// Offer describes one alternative run and the estimated replay plus judge cost.
type Offer struct {
	Delegation, Ladder, Class, Role, From, To, Reason string
	USD                                               float64
}

// JudgeTarget prefers a judge whose agent ran neither comparison side.
func JudgeTarget(l config.Learning, a, b config.Target) config.Target {
	for _, e := range l.Judges {
		if e.Agent != a.Agent && e.Agent != b.Agent {
			return e.Target()
		}
	}
	if len(l.Judges) == 0 {
		return config.Target{}
	}
	return l.Judges[0].Target()
}

func taskSteps(task []record.Delegation) []record.Delegation {
	out := slices.Clone(task)
	slices.SortStableFunc(out, func(a, b record.Delegation) int {
		if a.Step < b.Step {
			return -1
		}
		if a.Step > b.Step {
			return 1
		}
		return 0
	})
	return out
}

// WriteTable lists live delegations in step order and their final status.
func WriteTable(w io.Writer, task []record.Delegation) error {
	rows := taskSteps(task)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "STEP\tCLASS\tROLE\tTARGET\tUSD\tOUTCOME"); err != nil {
		return err
	}
	for i, d := range rows {
		if d.ProfileOf != "" {
			continue
		}
		outcome := "failed"
		switch {
		case d.Outcome == record.OutcomeInfra:
			outcome = "infra"
		case d.Outcome == record.OutcomeLead:
			outcome = "needs"
		case d.RunHere:
			outcome = "run here"
		case len(d.Needs) > 0:
			outcome = "needs"
		case d.Done:
			outcome = "done"
		}
		if d.Outcome != record.OutcomeInfra && d.Outcome != record.OutcomeLead {
			for _, later := range rows[i+1:] {
				if later.ProfileOf == "" && later.Outcome != record.OutcomeInfra && later.Outcome != record.OutcomeLead && later.EscalatedFrom == d.ID {
					outcome = "escalated"
					break
				}
			}
		}
		target := d.Entry
		if target == "" {
			target = (config.LadderEntry{Agent: d.Agent, Model: d.Model, Effort: d.Effort}).Key()
		}
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%.2f\t%s\n", d.Step, d.Class, d.Role, target, d.USD, outcome); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// WriteReport prints the fixed end-of-task conversation, leaving approval and
// outcome collection to the lead and the person.
func WriteReport(w io.Writer, cfg *config.Config, t record.OpenTask, task []record.Delegation, offers []Offer, learnChanged, classProposal bool) error {
	count := 0
	total := 0.0
	for _, d := range task {
		if d.ProfileOf == "" {
			count++
			total += d.USD
		}
	}
	if _, err := fmt.Fprintf(w, "task %s: %d delegations, $%.2f (estimated)\n", t.ID, count, total); err != nil {
		return err
	}
	if err := WriteTable(w, task); err != nil {
		return err
	}
	if len(offers) == 0 {
		if _, err := fmt.Fprintln(w, "Profiling offers: none"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(w, "Profiling offers (the user picks one, both or none):"); err != nil {
			return err
		}
		for i, o := range offers {
			if _, err := fmt.Fprintf(w, "%d. %s %s on %s instead of %s: %s; about $%.2f. Run: duolog profile %s --entry %s\n", i+1, o.Class, o.Role, o.To, o.From, o.Reason, o.USD, o.Delegation, o.To); err != nil {
				return err
			}
		}
	}
	if learnChanged {
		if _, err := fmt.Fprintln(w, "duolog learn has a proposed change; show it to the user after the outcome."); err != nil {
			return err
		}
	}
	if classProposal {
		if _, err := fmt.Fprintln(w, "A new class proposal is ready: run duolog learn after the outcome."); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w, "Ask the user for the outcome: accepted, small_fixes, reworked or dropped."); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "Record it with: duolog task outcome %s <outcome>\n", t.ID)
	return err
}

// Offers chooses at most two distinct ladder/target alternatives, preferring
// cheaper entries near the ran entry's estimate over cells with few judgments.
func Offers(cfg *config.Config, task []record.Delegation, st Stats, vs []record.Verdict) []Offer {
	l := cfg.Learning
	var offers []Offer
	offered := map[[2]string]bool{}
	judgeUSD := 0.0
	judgeRuns := 0
	for _, v := range vs {
		if v.Kind == "verdict" {
			judgeUSD += v.JudgeUSD
			judgeRuns++
		}
	}
	if judgeRuns > 0 {
		judgeUSD /= float64(judgeRuns)
	}
	for _, d := range taskSteps(task) {
		if d.ProfileOf != "" || !d.QualityRun() || d.Why == "person" || d.Class == "" || d.Class == Unclassified {
			continue
		}
		d.Entry = effectiveEntry(d)
		key, entries := l.Ladder(d.Class, d.Role)
		if key == "" {
			continue
		}
		from, err := config.ParseEntryKey(d.Entry)
		if err != nil {
			continue
		}
		from.Prior = l.Threshold
		for _, e := range entries {
			if e.Key() == d.Entry {
				from = e
				break
			}
		}
		ran := st[key][d.Entry]
		ranQuality, _ := Estimate(l, from.Prior, ran)
		chosen := false
		for _, reason := range []string{"cheaper and close to the best", "few judged runs in that cell"} {
			for _, to := range entries {
				pair := [2]string{key, to.Key()}
				if to.Key() == d.Entry || offered[pair] {
					continue
				}
				candidate := st[key][to.Key()]
				q, _ := Estimate(l, to.Prior, candidate)
				if reason == "cheaper and close to the best" {
					if candidate.Delegations == 0 || ran.Delegations == 0 || candidate.USD >= ran.USD || q < ranQuality-l.QualityMargin {
						continue
					}
				} else if candidate.Weight >= float64(l.ProfileBelowRuns) {
					continue
				}
				judge := JudgeTarget(l, from.Target(), to.Target())
				if judge == (config.Target{}) {
					continue
				}
				usd, ok := offerEstimate(cfg, d, from.Target(), to.Target(), judge, candidate, judgeUSD, judgeRuns > 0)
				if !ok {
					continue
				}
				offers = append(offers, Offer{Delegation: d.ID, Ladder: key, Class: d.Class, Role: d.Role, From: d.Entry, To: to.Key(), Reason: reason, USD: usd})
				offered[pair] = true
				chosen = true
				break
			}
			if chosen {
				break
			}
		}
		if len(offers) == 2 {
			break
		}
	}
	return offers
}

func offerEstimate(cfg *config.Config, d record.Delegation, from, to, judge config.Target, candidate config.EntryStat, judgeUSD float64, judgeKnown bool) (float64, bool) {
	// Cost with one million input and output tokens equals Input+Output of the
	// normalized model alias, with the same agent/price checks as live costing.
	price := func(t config.Target) (float64, bool) {
		p, ok := usage.Cost(cfg, t.Agent, t.Model, usage.Tokens{Input: 1_000_000, Output: 1_000_000})
		return p, ok && p > 0
	}
	replayKnown := candidate.Delegations > 0
	fromPrice := 0.0
	if !replayKnown || !judgeKnown {
		p, ok := price(from)
		if !ok {
			return 0, false
		}
		fromPrice = p
	}
	replayUSD := candidate.USD
	if !replayKnown {
		p, ok := price(to)
		if !ok {
			return 0, false
		}
		replayUSD = d.USD * p / fromPrice
	}
	if !judgeKnown {
		p, ok := price(judge)
		if !ok {
			return 0, false
		}
		judgeUSD = d.USD * p / fromPrice
	}
	return replayUSD + judgeUSD, true
}
