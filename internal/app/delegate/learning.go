package delegate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

const delegationsFile = "delegations.ndjson"

// learnRun journals one delegation in learning mode. A nil *learnRun is
// learning off: every method does nothing.
type learnRun struct {
	j        *journal.Journal
	stateDir string
	leadDir  string
	workRoot string
	cfg      *config.Config
	opts     Options
	id, role string
	task     journal.OpenTask
	implicit bool
	// after is the entry an escalation leaves; route starts after it.
	after      string
	leftTarget config.Target
	// rec holds the classification; finish adds the route and the run.
	rec  journal.Delegation
	next learnRoute // chosen by route, before Resolve
	cur  learnRoute // the final attempt's route: the one Resolve gave last
	// The final attempt's worker: ran is set by start once the before tree is
	// taken, usage is summed per runWorker call; resolved clears them.
	ran       bool
	started   time.Time
	before    string
	decisions int64
	usage     run.Usage
	quota     bool
}

type learnRoute struct {
	entry, requested, ladder, why string
	route                         coreroute.Route
}

// workRootOf is the directory the worker runs in: opts.WorkRoot, or the
// current work root of the project that holds s.
func workRootOf(s *store.Store, opts Options) (string, error) {
	if opts.WorkRoot != "" {
		return filepath.Abs(opts.WorkRoot)
	}
	root, err := filepath.Abs(filepath.Dir(s.Dir))
	if err != nil {
		return "", err
	}
	return project.CurrentWorkRoot(root), nil
}

// sameModel reports whether a target runs the lead's own model, which the
// router would otherwise run in the lead itself.
func sameModel(t config.Target, lead *config.Target) bool {
	return lead != nil && t.Agent == lead.Agent && coreroute.Normalize(t.Agent, t.Model) == coreroute.Normalize(lead.Agent, lead.Model)
}

// beginLearning classifies the delegation and finds its task. It refuses only
// an unknown stated class; a missing task opens an implicit one. Judge and
// calibration runs are not learnt from here.
func beginLearning(cfg *config.Config, s *store.Store, leadID, role, id string, opts Options) (*learnRun, error) {
	if !cfg.Learning.Enabled || opts.Judge || opts.Calibration {
		return nil, nil
	}
	l := cfg.Learning
	if opts.Class != "" {
		if _, ok := l.Classes[opts.Class]; !ok {
			return nil, fmt.Errorf("unknown class %q", opts.Class)
		}
	}
	j, err := journal.Open(s.Dir)
	if err != nil {
		return nil, err
	}
	var left journal.Delegation
	if opts.EscalateFrom != "" && opts.ReplayOf == "" {
		if left, err = escalated(j, leadID, opts); err != nil {
			return nil, err
		}
		if len(opts.Features) == 0 && opts.Class == "" {
			opts.Features, opts.Class = left.Features, left.StatedClass
		}
	}
	lr := &learnRun{j: j, stateDir: s.Dir, cfg: cfg, opts: opts, id: id, role: role, started: time.Now(), after: left.Entry}
	if left.RequestedEntry != "" {
		lr.after = left.RequestedEntry
	}
	if entry, err := config.ParseEntryKey(left.Entry); err == nil {
		lr.leftTarget = entry.Target()
		if left.EffortCapped {
			lr.leftTarget.Effort = left.Effort
		}
	}
	rec := journal.Delegation{ID: id, Lead: leadID, Role: role, Features: opts.Features, StatedClass: opts.Class, ProfileOf: opts.ReplayOf, EscalatedFrom: left.ID}
	if left.ID != "" {
		rec.EscalationReason = opts.EscalateReason
	}
	rec.RuleClass = learn.Classify(l.Classify, role, opts.Features)
	rec.Class = opts.Class
	if rec.Class == "" {
		rec.Class = rec.RuleClass
	}
	if rec.RuleClass == "" {
		rec.RuleClass = learn.Unclassified
	}
	if rec.Class == "" {
		rec.Class = learn.Unclassified
	}
	if opts.ReplayOf != "" {
		recs, err := journal.ReadAll[journal.Delegation](j, delegationsFile)
		if err != nil {
			return nil, err
		}
		i := slices.IndexFunc(recs, func(r journal.Delegation) bool { return r.ID == opts.ReplayOf && r.ProfileOf == "" })
		if i < 0 {
			return nil, fmt.Errorf("replay: delegation %s is not in the journal", opts.ReplayOf)
		}
		orig := recs[i]
		rec.Task, rec.Features, rec.StatedClass, rec.RuleClass, rec.Class, rec.Ladder = orig.Task, orig.Features, orig.StatedClass, orig.RuleClass, orig.Class, orig.Ladder
		lr.task = journal.OpenTask{ID: orig.Task}
	} else {
		task, ok, err := j.CurrentTask(leadID)
		if err != nil {
			return nil, err
		}
		if !ok {
			root, err := workRootOf(s, opts)
			if err != nil {
				return nil, err
			}
			base, err := journal.Head(root)
			if err != nil {
				return nil, err
			}
			if task, err = j.StartTask(leadID, "(implicit)", base, time.Now()); err != nil {
				return nil, err
			}
			lr.implicit = true
		}
		lr.task = task
		rec.Task = task.ID
	}
	if c, ok := l.Classes[rec.Class]; ok {
		rec.RoleMismatch = !slices.Contains(c.Roles, role)
	}
	if lr.after != "" && opts.Entry == nil {
		if _, ok := learn.Choose(l, rec.Class, role, nil, lr.after); !ok {
			return nil, fmt.Errorf("class %s has no entry after %s; do it yourself or name another role", rec.Class, lr.after)
		}
	}
	lr.rec = rec
	if opts.Entry != nil {
		why := "forced"
		if opts.Person {
			why = "person"
		}
		ladder := rec.Ladder
		if opts.ReplayOf == "" {
			ladder, _ = l.Ladder(rec.Class, role)
		}
		e := config.LadderEntry{Agent: opts.Entry.Agent, Model: opts.Entry.Model, Effort: opts.Entry.Effort}
		lr.next = learnRoute{entry: e.Key(), ladder: ladder, why: why}
	}
	return lr, nil
}

// escalated returns the delegation the lead leaves, after checking that the
// reason is a known signal and that the left run says something about its
// entry's quality.
func escalated(j *journal.Journal, leadID string, opts Options) (journal.Delegation, error) {
	if !slices.Contains(learn.EscalationReasons, opts.EscalateReason) {
		return journal.Delegation{}, fmt.Errorf("unknown escalation reason %q; use one of %s", opts.EscalateReason, strings.Join(learn.EscalationReasons, ", "))
	}
	task, ok, err := j.CurrentTask(leadID)
	if err != nil {
		return journal.Delegation{}, err
	}
	recs, err := journal.ReadAll[journal.Delegation](j, delegationsFile)
	if err != nil {
		return journal.Delegation{}, err
	}
	i := slices.IndexFunc(recs, func(r journal.Delegation) bool {
		return ok && r.ID == opts.EscalateFrom && r.Task == task.ID && r.ProfileOf == ""
	})
	if i < 0 {
		return journal.Delegation{}, fmt.Errorf("delegation %q is not in the open task", opts.EscalateFrom)
	}
	left := recs[i]
	if left.Outcome == journal.OutcomeInfra {
		return journal.Delegation{}, fmt.Errorf("delegation %s ended on a provider limit, not on quality; run it again", left.ID)
	}
	if left.Outcome == journal.OutcomeLead {
		return journal.Delegation{}, fmt.Errorf("delegation %s went back to the lead before a worker ran; there is no run to escalate from", left.ID)
	}
	if !left.QualityRun() {
		return journal.Delegation{}, fmt.Errorf("delegation %s returned control to the lead without a quality result; there is no run to escalate from", left.ID)
	}
	if left.Entry == "" {
		return journal.Delegation{}, fmt.Errorf("delegation %s ran on its role's own target, not on a ladder entry; delegate again with --class or --use", left.ID)
	}
	if left.Why == "person" && (opts.Entry == nil || !opts.Person) {
		return journal.Delegation{}, fmt.Errorf("delegation %s was the person's explicit choice; ask them before changing it", left.ID)
	}
	return left, nil
}

// keepsOwnRoute reports whether role stays on its own routing under class: the
// task is unclassified, or the role stays on its own agent (balance: false),
// such as review_cross, and the class does not list it, so that no class
// ladder or learned order moves it.
func keepsOwnRoute(cfg *config.Config, role, class string) bool {
	if class == learn.Unclassified {
		return true
	}
	spec, ok := cfg.Roles[role]
	return ok && !spec.Balanced() && !slices.Contains(cfg.Learning.Classes[class].Roles, role)
}

// route picks the class's ladder entry as the target, or after an escalation
// the next entry after the one the lead left. With no class, or no entry
// whose agent is available, the role's own routing applies; an escalation
// with no available entry is an error instead. NoSelf is set only when the
// entry is the lead's own model, so that it is measured.
func (lr *learnRun) route(cfg *config.Config, role string, unavailable map[string]bool, ro *coreroute.Options) error {
	if lr == nil || lr.opts.Entry != nil {
		return nil
	}
	lr.next = learnRoute{why: "role"}
	if keepsOwnRoute(cfg, role, lr.rec.Class) {
		return nil
	}
	cursor := lr.after
	// A switched-off agent is skipped like an unavailable one. Resolve below
	// gets the caller's map and does the same for the role itself.
	skip := make(map[string]bool, len(unavailable))
	for name, v := range unavailable {
		skip[name] = v
	}
	for name := range cfg.Agents {
		if !cfg.AgentEnabled(name) {
			skip[name] = true
		}
	}
	var choice learn.Choice
	var ok bool
	for {
		choice, ok = learn.Choose(cfg.Learning, lr.rec.Class, role, skip, cursor)
		if !ok || lr.after == "" {
			break
		}
		candidate := *ro
		target := choice.Entry.Target()
		candidate.Use = &target
		route, err := coreroute.Resolve(cfg, role, unavailable, candidate)
		if err != nil {
			return err
		}
		if route.Target != lr.leftTarget {
			break
		}
		cursor = choice.Entry.Key()
	}
	if !ok {
		if lr.after != "" {
			return fmt.Errorf("class %s has no available entry after %s; do it yourself or name another role", lr.rec.Class, lr.after)
		}
		return nil
	}
	if lr.after != "" {
		choice.Why = "escalation"
	}
	t := choice.Entry.Target()
	ro.Use = &t
	ro.NoSelf = sameModel(t, ro.Lead)
	lr.next = learnRoute{entry: choice.Entry.Key(), ladder: choice.Ladder, why: choice.Why}
	return nil
}

// resolved starts an attempt with the route Resolve gave for the choice route
// made. The record is the final attempt's, so an earlier attempt's worker run
// (one that hit a quota and fell back) is forgotten.
func (lr *learnRun) resolved(route coreroute.Route) {
	if lr == nil {
		return
	}
	lr.cur = lr.next
	lr.cur.route = route
	if route.EffortCapped && lr.cur.entry != "" {
		if entry, err := config.ParseEntryKey(lr.cur.entry); err == nil {
			lr.cur.requested = lr.cur.entry
			entry.Effort = route.Effort
			lr.cur.entry = entry.Key()
		}
	}
	lr.ran, lr.usage, lr.quota, lr.before = false, run.Usage{}, false, ""
}

// start takes what finish compares against and marks the worker as run.
func (lr *learnRun) start(root string) error {
	if lr == nil {
		return nil
	}
	var decisions int64
	lr.leadDir, lr.workRoot = project.LeadHome(filepath.Dir(lr.stateDir)), root
	if info, err := os.Stat(filepath.Join(lr.leadDir, "decisions.ndjson")); err == nil {
		decisions = info.Size()
	}
	before, err := journal.Capture(root)
	if err != nil {
		return fmt.Errorf("learning: %w", err)
	}
	lr.ran, lr.started, lr.decisions, lr.before = true, time.Now(), decisions, before
	return nil
}

// add counts one runWorker call; a login retry runs the worker twice.
func (lr *learnRun) add(r run.Result) {
	if lr == nil {
		return
	}
	lr.usage.Add(r.Usage)
	lr.quota = r.QuotaEnded
}

// records are the journal's delegations, which estimate a task's cost before
// its admission; none without learning, or when the journal cannot be read,
// so the estimate takes its cold bound.
func (lr *learnRun) records() []journal.Delegation {
	if lr == nil {
		return nil
	}
	recs, err := journal.ReadAll[journal.Delegation](lr.j, delegationsFile)
	if err != nil {
		return nil
	}
	return recs
}

// note is appended to the result line: the delegation id, and what the lead
// should do differently next time.
func (lr *learnRun) note() string {
	if lr == nil {
		return ""
	}
	st := lr.cur
	s := "; id=" + lr.id + " class=" + lr.rec.Class
	if st.entry != "" {
		s += " entry=" + st.entry
	}
	if st.route.EffortCapped {
		s += "; effort capped at " + st.route.Effort
	}
	if lr.implicit {
		s += "; no task was open: opened one, run duolog task finish when this task ends"
	}
	if lr.rec.Class == learn.Unclassified {
		s += "; unclassified: add --features or --class"
	}
	if lr.rec.RoleMismatch {
		s += "; class " + lr.rec.Class + " does not list role " + lr.role
	}
	return s
}

// finish writes the journal record of a delegation that ran a worker,
// returned run here, or went back to the lead before any worker ran (outcome
// lead). For a live worker run it keeps a copy of its task, answer and
// patches under the task's folder.
func (lr *learnRun) finish(root, dir, copyPath string, out Outcome) error {
	if lr == nil || (!lr.ran && out.RunHere == nil && len(out.Needs) == 0) {
		return nil
	}
	st := lr.cur
	rec := lr.rec
	rec.Time = lr.started.UTC()
	rec.Entry, rec.Ladder, rec.Why = st.entry, st.ladder, st.why
	rec.RequestedEntry = st.requested
	rec.Agent, rec.Model, rec.Effort, rec.EffortCapped = st.route.Agent, st.route.Model, st.route.Effort, st.route.EffortCapped
	rec.RunHere = out.RunHere != nil
	rec.Interrupted = out.Result.Interrupted
	rec.Needs = out.Needs
	if len(rec.Needs) == 0 {
		rec.Needs = OwnNeeds(out.Result.Needs)
	}
	if !lr.ran || rec.RunHere || rec.Interrupted || len(rec.Needs) > 0 {
		rec.Outcome = journal.OutcomeLead
	}
	var errs []error
	if lr.ran {
		rec.Input, rec.Cached, rec.Output = lr.usage.Input, lr.usage.Cached, lr.usage.Output
		rec.CacheWrite, rec.CacheWriteHour = lr.usage.Write, lr.usage.WriteHour
		rec.USD, rec.Priced = usage.Cost(lr.cfg, rec.Agent, rec.Model, lr.usage)
		rec.Seconds = time.Since(lr.started).Seconds()
		rec.Done = out.Result.Done
		if lr.quota {
			rec.Outcome, rec.Done = journal.OutcomeInfra, false
		}
		var err error
		rec.GateDenials, rec.GateAsks, err = lr.gateCounts()
		errs = append(errs, err)
		if rec.ProfileOf == "" {
			errs = append(errs, lr.keep(&rec, root, dir, copyPath, st.route.Sandbox == "read-only", len(rec.Needs) > 0))
		}
	}
	if rec.ProfileOf == "" {
		recs, err := journal.ReadAll[journal.Delegation](lr.j, delegationsFile)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		rec.Step = 1
		for _, r := range recs {
			if r.ProfileOf == "" && r.Task == rec.Task {
				rec.Step++
			}
		}
	}
	errs = append(errs, lr.j.Append(delegationsFile, rec))
	return errors.Join(errs...)
}

// keep copies the task and answer, and outside a wait for the lead writes the
// patches: before.patch from the task's base to the tree the worker started
// from, result.patch from there to the tree it left.
func (lr *learnRun) keep(rec *journal.Delegation, root, dir, copyPath string, readOnly, waits bool) error {
	target := lr.j.TaskDir(rec.Task, rec.ID)
	if err := os.MkdirAll(target, 0700); err != nil {
		return err
	}
	if err := copyFile(copyPath, filepath.Join(target, "task.md")); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(dir, "answer.json"), filepath.Join(target, "answer.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rec.Base = lr.task.Base
	if waits || lr.before == "" {
		return nil
	}
	rec.BeforeTree = lr.before
	rec.AfterTree = lr.before
	if rec.Base != "" {
		patch, err := journal.Diff(root, rec.Base, lr.before)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, "before.patch"), patch, 0600); err != nil {
			return err
		}
	}
	var patch []byte
	if !readOnly {
		after, err := journal.Capture(root)
		if err != nil {
			return err
		}
		rec.AfterTree = after
		if patch, err = journal.Diff(root, lr.before, after); err != nil {
			return err
		}
		if rec.Files, err = journal.NumStat(root, lr.before, after); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(target, "result.patch"), patch, 0600)
}

func copyFile(from, to string) error {
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0600)
}

// gateCounts counts the deny and ask decisions for this role that the gate
// appended during the run. An ask the lead or the person settled is logged
// with the rule "ask lead" and the answer's kind: it counts as an ask, and a
// denied one as a denial too. The gate logs no worker id, so a parallel
// worker of the same role and checkout counts too.
func (lr *learnRun) gateCounts() (denials, asks int, err error) {
	f, err := os.Open(filepath.Join(lr.decisionDir(), "decisions.ndjson"))
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	if _, err := f.Seek(lr.decisions, io.SeekStart); err != nil {
		return 0, 0, err
	}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scan.Scan() {
		var r gate.Record
		if json.Unmarshal(scan.Bytes(), &r) != nil || r.Action.Role != lr.role || !lr.matchesWork(r.Action.Work) {
			continue
		}
		if r.Kind == policy.Ask || r.Rule == "ask lead" {
			asks++
		}
		if r.Kind == policy.Deny {
			denials++
		}
	}
	return denials, asks, scan.Err()
}

func (lr *learnRun) decisionDir() string {
	if lr.leadDir != "" {
		return lr.leadDir
	}
	return lr.stateDir
}

func (lr *learnRun) matchesWork(work string) bool {
	if lr.workRoot == "" {
		return true
	}
	if work == "" {
		return pathutil.Clean(pathutil.Resolved(lr.decisionDir())) == pathutil.Clean(pathutil.Resolved(filepath.Join(project.StateRoot(lr.workRoot), ".duolog")))
	}
	return pathutil.Clean(pathutil.Resolved(work)) == pathutil.Clean(pathutil.Resolved(lr.workRoot))
}
