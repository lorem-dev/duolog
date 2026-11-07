package telemetry

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
)

const (
	other = "other"

	maxSafe        = 1<<53 - 1
	maxInt31       = 1<<31 - 1
	maxAttempt     = 1_000_000
	maxBasisPoints = 10_000
	maxScore       = 1e9
	maxLine        = 1024
	// maxItems refuses an array early; far fewer items already pass maxLine.
	maxItems = 64
)

// Closed sets. Callers map their own values (model names to families, quota
// window names to kinds) before Encode; anything not listed is other. The set
// of agents and the set of families come from the registered agents' descriptors
// through Names.
var (
	efforts     = []string{"low", "medium", "high", "xhigh", "max", "ultra", other}
	placements  = []string{"worker", "peer", "self", "inlead", other}
	outcomes    = []string{"done", "failed", "infra", "lead", "interrupted", "needs", other}
	errs        = []string{"none", "quota", "auth", "timeout", "exit", "parse", "gate", other}
	sources     = []string{"usage_read", "app_server", "rate_limit_event", other}
	plans       = []string{"free", "pro", "max", "plus", "team", "business", "enterprise", "edu", other}
	windowKinds = []string{"five_hour", "weekly", other}
	statuses    = []string{"ok", "blocked", "unknown", other}
	rules       = []string{"explicit", "availability", "cross_review", "pinned", "admission", "hysteresis", "balance", "outside_ladder", other}
	runReasons  = append(slices.Clone(rules), "none", "quota", "auth", "timeout", "gate")
	gateKinds   = []string{"read", "write", "exec", "net", "mcp", "unknown", other}
	ruleClasses = []string{"outside", "on_unmatched", "mode", "mcp", "unverified", other}
	answers     = []string{"allow", "deny", "timeout", other}
	deciders    = []string{"person", "lead", "auto", other}
	peerOps     = []string{"open", "send", "close", other}
	resets      = []string{"account", "plan", "drift", "manual", other}

	namePattern = regexp.MustCompile(`^[a-z0-9_.-]{1,32}$`)
)

func closed(value string, set []string) string {
	if slices.Contains(set, value) {
		return value
	}
	return other
}

// configured keeps a role or class name that is well formed and configured.
func configured(value string, names []string) string {
	if namePattern.MatchString(value) && slices.Contains(names, value) {
		return value
	}
	return other
}

// bounded names the field but never the value.
func bounded(field string, n, lo, hi int64) error {
	if n < lo || n > hi {
		return fmt.Errorf("telemetry: %s out of range", field)
	}
	return nil
}

func tokens(field string, n int64) error { return bounded(field, n, 0, maxSafe) }

func seconds(field string, n int64) error { return bounded(field, n, 0, maxInt31) }

// common keeps the caller's ids and replaces the rest of the metadata.
func common(stamp Common, typ string, from Common) (Common, error) {
	stamp.Type = typ
	stamp.Run, stamp.Task = from.Run, from.Task
	return stamp, errors.Join(from.Run.check(), from.Task.check())
}

func (r Run) clean(stamp Common, names Names) (any, error) {
	var err error
	r.Common, err = common(stamp, "run", r.Common)
	r.Role = configured(r.Role, names.Roles)
	r.Class = configured(r.Class, names.Classes)
	r.Agent = closed(r.Agent, names.Agents)
	r.Family = closed(r.Family, names.Families)
	r.Effort = closed(r.Effort, efforts)
	r.Placement = closed(r.Placement, placements)
	r.Outcome = closed(r.Outcome, outcomes)
	r.Err = closed(r.Err, errs)
	r.Reason = closed(r.Reason, runReasons)
	err = errors.Join(err,
		bounded("attempt", int64(r.Attempt), 1, maxAttempt),
		tokens("in", r.In), tokens("cached", r.Cached), tokens("out", r.Out),
		tokens("usd_micro", r.USDmicro), tokens("asks", r.Asks), tokens("denials", r.Denials),
		seconds("secs", r.Secs))
	if err == nil && r.Cached > r.In {
		err = errors.New("telemetry: cached over in")
	}
	return r, err
}

func (w Window) clean(names Names) (Window, error) {
	w.Kind = closed(w.Kind, windowKinds)
	w.Family = closed(w.Family, names.Families)
	w.Status = closed(w.Status, statuses)
	return w, errors.Join(
		bounded("len_min", w.LenMin, 1, maxInt31),
		bounded("used_bp", w.UsedBP, 0, maxBasisPoints),
		seconds("reset_in_min", w.ResetInMin))
}

func (q Quota) clean(stamp Common, names Names) (any, error) {
	if len(q.Windows) > maxItems {
		return nil, errLineTooLong
	}
	var err error
	q.Common, err = common(stamp, "quota", q.Common)
	q.Agent = closed(q.Agent, names.Agents)
	q.Src = closed(q.Src, sources)
	q.Plan = closed(q.Plan, plans)
	windows := make([]Window, len(q.Windows))
	for i, w := range q.Windows {
		var werr error
		windows[i], werr = w.clean(names)
		err = errors.Join(err, werr)
	}
	q.Windows = windows
	return q, errors.Join(err, tokens("epoch", q.Epoch))
}

func (t Target) clean(names Names) Target {
	return Target{Agent: closed(t.Agent, names.Agents), Family: closed(t.Family, names.Families), Effort: closed(t.Effort, efforts)}
}

func (r Route) clean(stamp Common, names Names) (any, error) {
	if len(r.Candidates) > maxItems {
		return nil, errLineTooLong
	}
	var err error
	r.Common, err = common(stamp, "route", r.Common)
	r.Role = configured(r.Role, names.Roles)
	r.Class = configured(r.Class, names.Classes)
	candidates := make([]Target, len(r.Candidates))
	for i, t := range r.Candidates {
		candidates[i] = t.clean(names)
	}
	r.Candidates = candidates
	r.Chosen = r.Chosen.clean(names)
	r.Incumbent = r.Incumbent.clean(names)
	r.Rule = closed(r.Rule, rules)
	// The comparison also refuses NaN.
	if !(r.ScoreMin >= -maxScore && r.ScoreMin <= maxScore) {
		err = errors.Join(err, errors.New("telemetry: score_min out of range"))
	}
	return r, err
}

func (g Gate) clean(stamp Common, _ Names) (any, error) {
	var err error
	g.Common, err = common(stamp, "gate", g.Common)
	g.Kind = closed(g.Kind, gateKinds)
	g.RuleClass = closed(g.RuleClass, ruleClasses)
	g.Answer = closed(g.Answer, answers)
	g.By = closed(g.By, deciders)
	return g, errors.Join(err, seconds("wait_secs", g.WaitSecs))
}

func (p Peer) clean(stamp Common, names Names) (any, error) {
	var err error
	p.Common, err = common(stamp, "peer", p.Common)
	p.Op = closed(p.Op, peerOps)
	p.Agent = closed(p.Agent, names.Agents)
	p.Outcome = closed(p.Outcome, outcomes)
	return p, errors.Join(err, seconds("secs", p.Secs))
}

func (r Reset) clean(stamp Common, names Names) (any, error) {
	var err error
	r.Common, err = common(stamp, "reset", r.Common)
	r.Agent = closed(r.Agent, names.Agents)
	r.Reason = closed(r.Reason, resets)
	return r, err
}
