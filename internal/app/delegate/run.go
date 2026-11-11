// Package delegate coordinates task handoffs, reusable threads and one fallback.
package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"crypto/sha256"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/gate"
	"github.com/lorem-dev/duolog/internal/app/launch"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/coauthor"
	coreagent "github.com/lorem-dev/duolog/internal/core/agent"
	"github.com/lorem-dev/duolog/internal/core/caps"
	"github.com/lorem-dev/duolog/internal/core/config"
	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
	"github.com/lorem-dev/duolog/internal/core/mode"
	"github.com/lorem-dev/duolog/internal/core/paidrules"
	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/core/policy"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/run"
	"github.com/lorem-dev/duolog/internal/core/taskspec"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/store"
)

type Outcome struct {
	Result  run.Result
	RunHere *Placement
	Line    string
	// Needs names the tools the worker or the capability check asked the lead
	// for. It is not a failure: the caller gives it a separate exit code.
	Needs []string
	// SkippedChecks are the Check lines the preflight did not read as a
	// command, for a run that started a worker in a write role.
	SkippedChecks []string
}

// NotStartedError is a delegation refused before any worker started: by the
// task, the lead, the route, trust or the preflight. Nothing ran, so a
// caller that charged the step can take the charge back.
type NotStartedError struct{ Err error }

func (e *NotStartedError) Error() string { return e.Err.Error() }
func (e *NotStartedError) Unwrap() error { return e.Err }

type Placement struct {
	Where               string
	Model, Effort, Task string
}

func (p Placement) String() string {
	if p.Where == "self" {
		return "run here: self task=" + p.Task
	}
	return fmt.Sprintf("run here: subagent model=%s effort=%s task=%s", p.Model, p.Effort, p.Task)
}

type Options struct {
	Lead             string
	AllowManualModel bool
	// Stderr receives what delegate tells the person beside the result line,
	// such as Read entries it ignores. Nil discards it.
	Stderr io.Writer
	// Needs lists "kind:name" capabilities the task relies on.
	Needs []string
	// Features and Class classify the delegation in learning mode.
	Features map[string]string
	Class    string // stated class, --class
	// Entry is a forced target: profile replays, the judge, the person's --use.
	Entry *config.Target
	// Person says Entry is the person's explicit choice (--use): Why "person".
	Person bool
	// ReplayOf is the delegation id this run replays; it needs no open task.
	ReplayOf string
	// Judge runs are never journalled.
	Judge bool
	// Force starts a worker even for a task that routing.small_task_files would
	// keep in the lead.
	Force bool
	// Calibration marks a calibration cell or statement rewrite: no task opens
	// and no journal record is written; the calibration run records it.
	Calibration bool
	// WorkRoot is the directory the worker runs in and trees are taken from;
	// it defaults to the current work root.
	WorkRoot string
	// MaxTurns caps the worker's turns where its CLI has a flag for it
	// (Claude); 0 sets no cap.
	MaxTurns                     int
	EscalateFrom, EscalateReason string
	// ProtectedRoots are the telemetry and balance roots the command resolved;
	// they reach the worker's launch settings.
	ProtectedRoots []string
	// Budget is what an approved calibration plan still allows this step; the
	// account gate admits each of its runs within it. Waiting hears when a run
	// starts and stops waiting for a session slot.
	Budget  *paidrules.StepBudget
	Waiting func(bool)
	// SkipPreflight starts the worker without judging the task's Check
	// commands with the gate first.
	SkipPreflight bool
	// Observe receives every worker run, failed ones included, with learning
	// on or off; nil observes nothing.
	Observe Observer
}

func Place(cfg *config.Config, taskPath string, route coreroute.Route) (Placement, error) {
	root, err := os.Getwd()
	if err != nil {
		return Placement{}, err
	}
	return place(cfg, taskPath, root, route)
}

// Run delegates one task to a worker, reaching outside the package only
// through d.
func Run(ctx context.Context, d Deps, r Request) (out Outcome, err error) {
	started, runs := time.Now(), newRunAttempts(r.Options)
	cfg, role, taskPath, thread, opts := r.Config, r.Role, r.TaskPath, r.Thread, runs.opts
	s := &store.Store{Dir: r.StateDir}
	// workerStarted says a worker ran; until then every refusal is a
	// NotStartedError. A calibration step needs a worker's result, so for one
	// a placement in the lead or a wait for the lead is a refusal too.
	workerStarted := false
	defer func() {
		if runs.complete(&out, workerStarted) {
			return
		}
		if err == nil && opts.Calibration && (out.RunHere != nil || len(out.Needs) > 0) {
			err = errors.New(out.Line)
		}
		if err != nil {
			err = &NotStartedError{Err: err}
		}
	}()
	defer func() {
		if out.Line == "" && err != nil {
			out.Line = oneLine(fmt.Sprintf("role %s: %v", role, err))
		}
	}()
	if launch.IsWorker() {
		return out, fmt.Errorf("nested delegation refused (DUOLOG_DEPTH=%s)", os.Getenv("DUOLOG_DEPTH"))
	}
	for _, need := range opts.Needs {
		if _, _, err = caps.ParseNeed(need); err != nil {
			return out, err
		}
	}
	root, err := filepath.Abs(filepath.Dir(s.Dir))
	if err != nil {
		return out, err
	}
	root = project.CurrentWorkRoot(root)
	lookup := taskLookup(taskPath)
	writes, reads, original, plan, err := readTaskPlan(root, lookup, opts)
	if err != nil {
		return out, err
	}
	if opts.WorkRoot != "" {
		if root, err = workRootOf(s, opts); err != nil {
			return out, err
		}
	}
	tasksDir := filepath.Join(s.Dir, "tasks")
	if opts.Calibration {
		// A cell's tree is its own repository outside the project, and
		// task.Inline reads a copy only from the work root's state.
		tasksDir = filepath.Join(project.StateRoot(root), ".duolog", "tasks")
	}
	dir, copyPath, err := copyTask(tasksDir, original)
	if err != nil {
		return out, err
	}
	// Calibration exports use the originating work project's lead, even
	// when their temporary checkout is an independent repository. The lead's
	// session cap comes from the lead home's config, not the work project's.
	reg, err := session.OpenLead(project.LeadHome(filepath.Dir(s.Dir)), cfg, filepath.Dir(s.Dir))
	if err != nil {
		return out, err
	}
	lead, reminders, err := submoduleLead(reg, opts.Lead, filepath.Dir(s.Dir))
	if err != nil {
		return out, err
	}
	lead, leadMode, err := observedLead(d, reg, lead, started)
	if err != nil {
		return out, err
	}
	leadID, leadAgent, address := lead.ID, lead.Agent, session.Address(lead)
	reg.LeadID = leadID
	lr, err := beginLearning(cfg, s, leadID, role, filepath.Base(dir), opts)
	if err != nil {
		return out, err
	}
	att := newAttempts(opts, s.Dir, role, lr)
	// Runs last, after the line is final: a journal error never hides the answer.
	defer func() {
		if learnErr := lr.finish(root, dir, copyPath, out); learnErr != nil {
			err = errors.Join(err, fmt.Errorf("learning: %w", learnErr))
		}
	}()
	leadRules, err := gate.ImportLeadForWork(root, gate.LeadRoot(lead, reg), lead.Agent)
	if err != nil {
		return out, fmt.Errorf("lead permissions: %w", err)
	}
	b, err := bus.Open(reg.Dir, cfg.Wire)
	if err != nil {
		return out, err
	}
	ref := filepath.ToSlash(filepath.Join("tasks", filepath.Base(dir), "task.md"))
	worker := "duolog/worker"
	var warning string
	// notices are migration lines claimed for this lead and the line of a
	// free run that spent paid overage; every line that ends this run
	// carries them, whichever way it ends.
	notices := reminders
	var firstFailure error
	// quotaEnded says an agent's quota ended in this run, so the line that
	// hands the task to the lead names it as answerPointer names paths.
	quotaEnded := false
	send := func(from, to, kind, text, reference string) error {
		_, e := b.Send(bus.Message{From: from, To: to, K: kind, Th: thread, Ref: reference, S: short(oneLine(text), cfg.Wire.MaxSummaryBytes)})
		return e
	}
	defer func() {
		if err != nil && firstFailure != nil {
			err = errors.Join(firstFailure, fmt.Errorf("%s: %w", worker, err))
		}
		if out.Line == "" && err != nil {
			out.Line = oneLine(fmt.Sprintf("role %s: %v", role, err))
		}
		out.Line = finalLine(out.Line, notices, dir)
	}()
	if line := smallTaskLine(cfg, role, root, taskPath, writes, plan.folders, opts, leadMode); line != "" {
		out.RunHere, out.Line = &Placement{Where: "self", Task: taskPath}, line
		return out, nil
	}
	unavailable, err := reg.Unavailable()
	if err != nil {
		return out, err
	}
	// Every worker run is admitted by the account gate, one permit per run.
	accounts, err := d.PaidGate(cfg)
	if err != nil {
		return out, err
	}
	// Every free candidate of the role, use then fallback, comes before any
	// paid one: a free pass, then a paid pass over the agents that allow it.
	passes, paidAgents := variants(d, cfg, role, opts)
	pass, variant := 0, passes[0]
	nextPass := func() bool {
		if pass+1 >= len(passes) {
			return false
		}
		pass++
		variant = passes[pass]
		unavailable = map[string]bool{}
		for _, agent := range d.Names {
			unavailable[agent] = !paidAgents[agent]
		}
		return true
	}
	// refusals say why the gate refused each candidate, and failures why a
	// run failed before the next candidate; when no candidate is left, the
	// task goes to the lead with them.
	var refusals, failures []string
	// inLead is the lead's fallback placement, kept while a paid pass is
	// tried first.
	var inLead *Placement
	// later reports whether a pass or a refusal follows, so that running
	// out of candidates in this pass is not yet the end.
	later := func() bool { return pass+1 < len(passes) || len(refusals) > 0 }
	priorAgent := ""
	placementTask := taskPath
	cacheDir := filepath.Join(s.Dir, "cache")
	loginTried := false
	var capNotes string
	// Pace balancing reads the quota once per task; a pinned target is never
	// balanced, and the paid pass is never balanced either.
	var balance *coreroute.Balance
	if opts.Entry == nil {
		balance = paidruns.DefaultPaceBalance(ctx, cfg)
	}
	for attempt := 0; ; attempt++ {
		// Use and fallback make one pass; then the paid pass, if any.
		if attempt == 2 {
			if !nextPass() {
				break
			}
			priorAgent, attempt = "", 0
		}
		ro := coreroute.Options{Lead: &lead.Target, AllowManualModel: opts.AllowManualModel}
		if pass == 0 {
			ro.Balance = balance
		}
		if opts.Entry != nil {
			if unavailable[opts.Entry.Agent] {
				if later() {
					attempt = 1
					continue
				}
				return out, fmt.Errorf("requested target %s/%s/%s is unavailable; ask the person before choosing another target", opts.Entry.Agent, opts.Entry.Model, opts.Entry.Effort)
			}
			ro.Use, ro.NoSelf = opts.Entry, true
		} else if err := lr.route(cfg, role, unavailable, &ro); err != nil {
			return out, err
		}
		route, routeErr := coreroute.Resolve(cfg, role, unavailable, ro)
		if routeErr != nil {
			// No candidate is left in this pass.
			if (attempt > 0 || pass > 0) && later() {
				attempt = 1
				continue
			}
			return out, plan.refusal(routeErr)
		}
		lr.resolved(route)
		worker = route.Agent + "/" + role
		paidruns.SaveDefaultPaceState(route.Ahead)
		if route.IsFallback {
			from := priorAgent
			if from == "" {
				from = cfg.Roles[role].Use.Agent
				if ro.Use != nil {
					from = ro.Use.Agent
				}
			}
			moved := fmt.Sprintf("role %s moved from %s to %s model=%s effort=%s", role, from, route.Agent, route.Model, route.Effort)
			if route.Reason != "" {
				moved += "; " + route.Reason
			}
			if err = send(worker, address, "status", moved, ref); err != nil {
				return out, err
			}
		}
		if leadMode == mode.Plan && route.Sandbox != "read-only" {
			return out, fmt.Errorf("mode plan refuses write role %s", role)
		}
		if err = plan.inLead(route); err != nil {
			return out, err
		}
		if route.Self || route.InLead {
			p := Placement{Where: "self", Task: placementTask}
			if !route.Self {
				p, err = place(cfg, placementTask, root, route)
				if err != nil {
					return out, err
				}
			}
			if route.InLead && !route.Self && len(refusals) > 0 && pass+1 < len(passes) {
				// A refused agent's paid variant comes before the lead's
				// fallback: the paid pass runs first, and the lead gets this
				// placement only if no paid run is admitted.
				inLead = &p
				attempt = 1
				continue
			}
			out.RunHere = &p
			out.Line = shownTask(p, quotaEnded).String()
			if len(refusals) > 0 || len(failures) > 0 {
				out.Line = leadLine(role, append(failures, refusals...), shownTask(p, quotaEnded))
			}
			return out, nil
		}
		// Resolved before resolveModel replaces the alias, which names the family.
		wm := resolveWorkerMode(cfg, role, route, lead, leadMode)
		skip, err := plan.check(preflightRun{cfg, leadRules, leadMode, role, root, filepath.Dir(s.Dir), opts.ProtectedRoots, reads, opts.Stderr, opts.Entry != nil}, route, wm, attempt, &out)
		if err != nil {
			return out, err
		}
		if skip {
			priorAgent = route.Agent
			unavailable[route.Agent] = true
			continue
		}
		capNotes = ""
		// The bridge must not replace a variable the worker already gets. The
		// worker mode's DUOLOG_NATIVE, DUOLOG_MODE when an entry applies, and
		// the task's Read paths go through the build with the run id, so the
		// account gate admits the environment the worker runs with.
		runEnv := workerRunEnv(att.run, wm, reads)
		var base corelaunch.Process
		if base, err = workerEnv(d.Build, workerInput(cfg, route, variant, leadID, role, root, opts.ProtectedRoots, runEnv, opts.Needs)); err != nil {
			return out, err
		}
		env := base.Env
		var bridged *run.MCP
		if len(opts.Needs) > 0 {
			// The worker's inventory is what its own environment's settings
			// folders hold; the lead's is duolog's own.
			target, scanErr := d.scan(ctx, route.Agent, root, env)
			if scanErr != nil {
				return out, scanErr
			}
			lead := caps.Inventory{Agent: leadAgent, Unknown: true}
			if leadAgent != "" {
				if lead, scanErr = d.leadInventory(ctx, leadAgent, route.Agent, root); scanErr != nil {
					return out, scanErr
				}
			}
			plan := capabilityPlan(caps.Resolve(target, lead, opts.Needs, cfg.Capabilities))
			if r := plan.Refuse; r != nil {
				out.Line = oneLine("role " + role + ": " + r.Hint)
				return out, fmt.Errorf("capability %s missing: %s", r.Need, r.Hint)
			}
			notes, lacking, reroute := plan.Notes, plan.Lacking, plan.Reroute
			bridge := plan.Bridge
			bridged = nil
			if len(bridge) > 0 {
				var failed map[string]string
				bridged, failed = d.bridge(leadAgent, route.Agent, root, bridge, cfg.Capabilities.MCPTools, env)
				for _, name := range bridge {
					if why, ok := failed[name]; ok {
						need := caps.KindMCP + ":" + name
						lacking = append(lacking, need)
						notes = append(notes, caps.BridgeFallback(need, route.Agent, why))
					}
				}
			}
			if len(lacking) > 0 {
				out.Needs = lacking
				out.Line = oneLine("role " + role + " waits for lead: " + strings.Join(notes, " "))
				return out, nil
			}
			if reroute {
				if attempt == 1 {
					return out, fmt.Errorf("capabilities missing on both routes: %s", strings.Join(notes, " "))
				}
				priorAgent = route.Agent
				unavailable[route.Agent] = true
				continue
			}
			if needsKind(opts.Needs, caps.KindMCP) {
				notes = append(notes, AuthWorkerLine)
			}
			for _, n := range notes {
				capNotes += n + "\n"
			}
			if capNotes != "" {
				if err = os.WriteFile(copyPath, []byte(annotate(string(original), capNotes)), 0600); err != nil {
					return out, err
				}
				// The copy is duolog's own file, read where it was written.
				if err = taskspec.Validate(fileFS(copyPath)); err != nil {
					return out, err
				}
			}
		}
		// The worker's process, built after every choice that moves its route:
		// the variant, the context and the bridged servers' env. Its gate
		// preflight, its login check, its quota read and its run all use it.
		extra := runEnv
		if bridged != nil {
			extra = append(slices.Clone(runEnv), bridged.Env...)
		}
		in := admittedInput(d, accounts, workerInput(cfg, route, variant, leadID, role, root, opts.ProtectedRoots, extra, opts.Needs), route, wm)
		if accounts.Layers != nil {
			if in.Layers, err = accounts.Layers(ctx, in); err != nil {
				return out, err
			}
		}
		var pre corelaunch.Process
		if pre, err = workerEnv(d.Build, in); err != nil {
			return out, err
		}
		// The hooks, approvals and folder trust the worker loads are those its
		// own environment selects, not duolog's.
		protected := append(slices.Clone(opts.ProtectedRoots), filepath.Join(root, ".duolog"), s.Dir)
		agentTrust := d.Agents[route.Agent].Trust
		state, folder, trustErr := workerGateState(d, s.Dir, route.Agent, root, pre.Env, opts)
		noHome := errors.Is(trustErr, coreagent.ErrNoHome)
		switch {
		case noHome && wm.needsPreflight():
			// Nothing the worker's agent loads can be read, so nothing shows
			// its gate.
			return out, wm.gatePreflight(d, route.Agent, root, pre.Env, protected)
		case noHome:
			// The worker runs as one without duolog's hooks would, and the lead
			// learns why. Claude needs no hook approval or folder trust.
			claude := route.Agent == "claude"
			state, folder, trustErr = coreagent.TrustState{Trusted: claude}, claude, nil
		}
		if trustErr != nil {
			return out, trustErr
		}
		// A calibration cell's promise is that none runs without the gate.
		if opts.Calibration {
			if err = calibrationGate(agentTrust, route.Agent, root, pre.Env); err != nil {
				return out, trustGap{route.Agent == "codex", agentTrust, state, folder, root, s.Dir, pre.Env}.refusal(err)
			}
		}
		if warning, err = trustWarning(route, role, root, state, folder); err != nil {
			if noHome {
				err = fmt.Errorf("%w; %s", err, noHomeProblem())
			}
			return out, trustGap{route.Agent == "codex", agentTrust, state, folder, root, s.Dir, pre.Env}.refusal(err)
		}
		if noHome {
			warning += "; " + noHomeProblem()
		}
		// A looser worker, or one that only the gate keeps read-only, whose gate
		// cannot be shown to run does not start, and the other agent does not
		// run in its place.
		if wm.needsPreflight() {
			if err = wm.gatePreflight(d, route.Agent, root, pre.Env, protected); err != nil {
				return out, trustGap{route.Agent == "codex", agentTrust, state, folder, root, s.Dir, pre.Env}.refusal(err)
			}
		}
		warning += wm.line()
		if bridged != nil {
			warning += "; bridged mcp:" + strings.Join(bridged.Names, ",mcp:") + " from " + leadAgent
		}
		// The brief and the worker lines count against the inline limit, so a large
		// brief leaves less room for Read files instead of growing every handoff.
		brief := workerBrief(root)
		budget, budgetErr := inlineBudget(cfg.Wire.InlineMaxBytes, len(brief), len(workerLines))
		if budgetErr != nil {
			return out, budgetErr
		}
		readRules := leadRules
		readRules.RoleSandbox = route.Sandbox
		inline, inlineErr := inlineTask(root, copyPath, budget, func(path string) bool {
			if pathutil.IsAbs(path) {
				if rel, err := pathutil.Relative(root, path); err == nil {
					path = rel
				}
			}
			// The same work root and store roots the worker's gate hook gets, so a
			// Read reference cannot hand a private store over before that hook runs.
			read := policy.Action{Kind: "read", Target: pathutil.Clean(path), Role: role, Work: root, ProtectedRoots: opts.ProtectedRoots}
			return policy.Evaluate(cfg.Policy, readRules, leadMode, read).Kind == policy.Allow
		})
		if inlineErr != nil {
			return out, inlineErr
		}
		claimed, noticeErr := ClaimMigration(d.DroppedKeys, reg, cfg, leadID, route.Agent)
		if noticeErr != nil {
			return out, noticeErr
		}
		notices = append(notices, claimed...)
		checker := d.checker(cacheDir, pre)
		// A worker that cannot sign in would fail after the lead waited for it,
		// so its agent and the servers it needs are checked first.
		// The servers are checked only once the agent is logged in, so a login
		// that works checks them next.
		for problems := d.preflight(ctx, checker, route.Agent, leadAgent, root, opts.Needs, bridged, pre.Env); len(problems) > 0; {
			p := problems[0]
			ok, authErr := settleAuth(ctx, d, cfg, reg, checker, leadID, role, p, &loginTried, &out)
			if authErr != nil || !ok {
				return out, authErr
			}
			if p.Server == "" {
				problems = d.preflight(ctx, checker, route.Agent, leadAgent, root, opts.Needs, bridged, pre.Env)
			} else {
				problems = problems[1:]
			}
		}
		model, modelErr := d.resolveModel(ctx, cfg, filepath.Join(s.Dir, "cache"), route.Target)
		if modelErr != nil {
			if missingBinary(modelErr) {
				err = modelErr
			} else {
				return out, modelErr
			}
		} else {
			route.Model = model
			stdin := append(append(brief, inline...), workerLines...)
			// The admitted process (current.launch sets it) already holds the
			// resolved native flags and the Codex sandbox pins; the runner
			// refuses it if this spec asks for other ones.
			spec := workerSpec(d, cfg, opts, route, wm, dir, root, stdin, bridged, pre.Env, workerName(d, role, lead))
			var entry *session.Entry
			if route.Session == "resume" {
				entry, _, err = reg.LookupWork(leadID, route.Agent, role, thread, root)
				if err != nil {
					return out, err
				}
				if entry != nil {
					if !keepThread(entry, cfg.Sessions, time.Now()) {
						if err = reg.Close(entry.ID); err != nil {
							return out, err
						}
						entry = nil
					} else {
						spec.ThreadID = entry.ThreadID
						// The thread already holds the brief from its first turn.
						spec.Stdin = append(append([]byte{}, inline...), workerLines...)
					}
				}
			}
			// Each actual run takes its own permit: a slot, the task's estimate
			// and, for a paid run, its share of the cap. A refusal moves on to
			// the next candidate; nothing has started.
			class := opts.Class
			if lr != nil {
				class = lr.rec.Class
			}
			records := lr.records()
			acquire := func() (*permitRun, *paidruns.Refusal, error) {
				a, refusal, err := admit(ctx, accounts, cfg, in, route, role, class, records, opts)
				if err != nil || refusal != nil {
					return nil, refusal, err
				}
				if err := a.sameRoute(ctx, d, pre, cacheDir); err != nil {
					return nil, nil, err
				}
				// The process that launches is the one admitted: its gate is
				// shown to run once more, from its own environment.
				if wm.needsPreflight() {
					if err := wm.gatePreflight(d, route.Agent, root, a.permit.Process.Env, protected); err != nil {
						return nil, nil, errors.Join(err, a.release(ctx))
					}
				}
				if opts.Calibration {
					if err := calibrationGate(agentTrust, route.Agent, root, a.permit.Process.Env); err != nil {
						return nil, nil, errors.Join(err, a.release(ctx))
					}
				}
				return a, nil, nil
			}
			current, refusal, admitErr := acquire()
			if admitErr != nil {
				return out, admitErr
			}
			if refusal != nil {
				refusals = append(refusals, RefusalLine(refusal))
				priorAgent = route.Agent
				unavailable[route.Agent] = true
				continue
			}
			if err = send(address, worker, "task", "role "+role+" task="+ref, ref); err != nil {
				return out, errors.Join(err, current.release(ctx))
			}
			var before map[string]string
			if route.Sandbox != "read-only" {
				var snapErr error
				before, snapErr = snapshot(root)
				if snapErr != nil {
					return out, errors.Join(snapErr, current.release(ctx))
				}
			}
			if err = lr.start(root); err != nil {
				return out, errors.Join(err, current.release(ctx))
			}
			token := filepath.Base(dir)
			if err = reg.SetWaiting(leadID, token, os.Getpid()); err != nil {
				return out, errors.Join(err, current.release(ctx))
			}
			creditBefore := before
			gateLogBefore := gateLogSize(reg.Dir)
			// retryRefusal is the gate's answer to a run once more after a login.
			var retryRefusal *paidruns.Refusal
			for retry := false; ; retry = true {
				if retry {
					// A retry is a new run: a new permit, under the cap as it is now.
					if current, retryRefusal, err = acquire(); err != nil || retryRefusal != nil {
						_ = reg.SetWaiting(leadID, token, 0)
						if err != nil {
							return out, err
						}
						break
					}
				}
				if err = current.launch(ctx, &spec); err != nil {
					_ = reg.SetWaiting(leadID, token, 0)
					return out, errors.Join(err, current.release(ctx))
				}
				started := att.begin()
				workerStarted = true
				out.Result, err = func() (run.Result, error) {
					defer func() { _ = reg.SetWaiting(leadID, token, 0) }() // best effort: the entry is only a wait marker
					return runs.run(ctx, spec, d.Agents[route.Agent].Worker.Run)
				}()
				att.worker(started, route, retry, out.Result, err)
				// Settled once, with what this run measured, before a retry
				// replaces the result.
				if settleErr := runs.settle(ctx, current, cfg, route, out.Result); settleErr != nil {
					return out, errors.Join(err, settleErr)
				}
				// A run admitted as free that spent paid overage: the ledger
				// charged it, admission now sees subscription_overage, and
				// every line that ends this delegation says so.
				if current.permit.Assessment.Verdict != paidrules.Paid && out.Result.Billing.UsingOverage {
					if n := route.Agent + " worker ran on paid overage; duolog stopped further runs"; !slices.Contains(notices, n) {
						notices = append(notices, n)
					}
				}
				var after map[string]string
				var snapErr error
				if before != nil {
					after, snapErr = snapshot(root)
				}
				files := make(map[string]bool)
				for _, name := range out.Result.Files {
					files[name] = true
				}
				for _, name := range changes(creditBefore, after) {
					files[name] = true
				}
				creditErr := coauthor.Append(s.Dir, cfg.Coauthors, coauthor.Record{Lead: leadID, Agent: route.Agent, Model: route.Model, Files: len(files)})
				if snapErr != nil || creditErr != nil {
					return out, errors.Join(err, snapErr, creditErr)
				}
				// A login retry is a new run with its own work-tree baseline.
				creditBefore = after
				lr.add(out.Result)
				if usageErr := appendUsage(s.Dir, role, route, out.Result, wm.Native); usageErr != nil {
					return out, errors.Join(err, usageErr)
				}
				problems := d.runProblems(route.Agent, leadAgent, root, out.Result, bridged, opts.Needs, current.permit.Process.Env)
				if len(problems) == 0 || out.Result.Interrupted || out.Result.QuotaEnded {
					break
				}
				// A login that works lets the worker run once more; settleAuth
				// tries one login per delegation, so this ends.
				ok, authErr := settleAuth(ctx, d, cfg, reg, checker, leadID, role, problems[0], &loginTried, &out)
				if authErr != nil || !ok {
					out.Result = run.Result{}
					return out, authErr
				}
				if err = reg.SetWaiting(leadID, token, os.Getpid()); err != nil {
					return out, err
				}
			}
			if retryRefusal != nil {
				// The gate refused the run once more: the next candidate.
				refusals = append(refusals, RefusalLine(retryRefusal))
				priorAgent = route.Agent
				unavailable[route.Agent] = true
				continue
			}
			if out.Result.Interrupted {
				out.Line = "role " + role + " interrupted"
				if sendErr := send(worker, address, "status", out.Line, ref); sendErr != nil {
					err = errors.Join(err, sendErr)
				}
				if err == nil {
					err = fmt.Errorf("worker interrupted")
				}
				return out, err
			}
			if !out.Result.QuotaEnded && err == nil {
				sessionID := out.Result.SessionID
				if sessionID == "" && entry != nil {
					sessionID = entry.ThreadID
				}
				if route.Session == "resume" && sessionID != "" {
					e := session.Entry{ID: filepath.Base(dir), Lead: leadID, Agent: route.Agent, Role: role, Thread: thread, ThreadID: sessionID, Input: out.Result.Usage.Input, WorkRoot: root}
					if entry != nil {
						e.ID = entry.ID
						e.Input += entry.Input
					}
					if err = reg.RecordLimited(e, reg.Limit); err != nil {
						return out, err
					}
				}
				needs := ""
				if len(out.Result.Needs) > 0 {
					out.Needs = OwnNeeds(out.Result.Needs)
					needs = " needs=" + strings.Join(out.Result.Needs, ",")
				}
				if len(out.Result.Decisions) > 0 {
					needs += fmt.Sprintf(" decisions=%d", len(out.Result.Decisions))
				}
				lineWarning := warning
				if before != nil {
					if after, snapErr := snapshot(root); snapErr == nil {
						changed := changes(before, after)
						if outside := outsideWrite(changed, writes); len(outside) > 0 {
							lineWarning += "; outside=" + strings.Join(outside, ",")
						}
						// A looser worker that changed files while the gate logged
						// nothing likely ran without its hooks.
						if wm.needsGate() && len(changed) > 0 && gateLogSize(reg.Dir) <= gateLogBefore {
							lineWarning += "; gate saw no actions in native " + wm.Native
						}
					}
				}
				lineWarning += lr.note()
				lineWarning += joinNotices(notices)
				out.Line = resultLine(out.Result.Done, needs, out.Result.Summary, strings.Join(out.Result.Files, ","), lineWarning, answerPointer(dir))
				return out, nil
			}
			if out.Result.QuotaEnded {
				quotaEnded = true
				if markErr := reg.MarkUnavailable(route.Agent, out.Result.ResetAt); markErr != nil {
					return out, errors.Join(err, markErr)
				}
				if entry != nil {
					if closeErr := reg.Close(entry.ID); closeErr != nil {
						return out, errors.Join(err, closeErr)
					}
				}
				if err == nil {
					err = fmt.Errorf("quota ended")
				}
				var after map[string]string
				if before != nil {
					var snapErr error
					after, snapErr = snapshot(root)
					if snapErr != nil {
						return out, errors.Join(err, snapErr)
					}
				}
				changed := changes(before, after)
				if len(changed) > 0 {
					note := "Partial work from " + route.Agent + ": " + strings.Join(changed, ", ") + ". Inspect these files before continuing.\n"
					updated := annotate(string(original), capNotes+note)
					if writeErr := os.WriteFile(copyPath, []byte(updated), 0600); writeErr != nil {
						return out, writeErr
					}
					placementTask = copyPath
				}
			} else if !missingBinary(err) {
				return out, err
			}
		}
		failure := fmt.Errorf("%s: %w", route.Agent, err)
		if attempt == 1 && !later() {
			return out, failure
		}
		if firstFailure == nil {
			firstFailure = failure
		}
		failures = append(failures, oneLine(failure.Error()))
		priorAgent = route.Agent
		unavailable[route.Agent] = true
	}
	if len(refusals) == 0 {
		return out, firstFailure
	}
	if plan.blocked != nil {
		return out, plan.blocked
	}
	// The gate refused every candidate left: the task goes to the lead with
	// one line, as a task whose needs no worker meets does, in the placement
	// of the lead's fallback when the router chose one.
	p := Placement{Where: "self", Task: placementTask}
	if inLead != nil {
		p = *inLead
	}
	out.RunHere = &p
	out.Line = leadLine(role, append(failures, refusals...), shownTask(p, quotaEnded))
	return out, nil
}

// keepThread reports whether the live thread of a session entry may take
// another turn: it stays under the input token limit and has not idled past
// the close time. An idle close time of 0 never closes a thread.
func keepThread(entry *session.Entry, limits config.Sessions, now time.Time) bool {
	over := entry.Input > int64(limits.ResumeMaxInputTokens)
	idle := limits.IdleCloseMinutes > 0 && now.Sub(entry.UpdatedAt) > time.Duration(limits.IdleCloseMinutes)*time.Minute
	return !over && !idle
}

// inlineBudget is the bytes left for inlined task text once the brief and the
// worker lines are taken from wire.inline_max_bytes.
func inlineBudget(maxBytes, briefBytes, linesBytes int) (int, error) {
	budget := maxBytes - briefBytes - linesBytes
	if budget <= 0 {
		return 0, fmt.Errorf(".duolog/brief.md and the worker lines take %d of wire.inline_max_bytes (%d); shorten the brief", briefBytes+linesBytes, maxBytes)
	}
	return budget, nil
}

func missingBinary(err error) bool {
	var pathErr *os.PathError
	return errors.Is(err, run.ErrBinaryMissing) || (errors.As(err, &pathErr) && pathErr.Op == "fork/exec" && errors.Is(err, os.ErrNotExist))
}

// ClaimMigration returns a migration line for each billing key that agent's
// workers no longer get, the first time this lead launches the agent.
// Delegate and peer open share the claim, so the lead reads each line once.
// Without a lead there is nothing to remember it by, and the lines come every
// time.
func ClaimMigration(dropped launch.DroppedKeys, reg *session.Registry, cfg *config.Config, leadID, agent string) ([]string, error) {
	var lines []string
	for _, name := range dropped(cfg, os.Environ())[agent] {
		claimed := true
		if leadID != "" {
			var err error
			if claimed, err = reg.ClaimNotice(leadID, "migration:"+agent+":"+name); err != nil {
				return lines, err
			}
		}
		if claimed {
			lines = append(lines, corelaunch.MigrationLine(agent, name))
		}
	}
	return lines, nil
}

// migrationLines names the billing keys a refused agent login may have lost.
func migrationLines(dropped launch.DroppedKeys, cfg *config.Config, p Problem) []string {
	if p.Server != "" {
		return nil
	}
	var lines []string
	for _, name := range dropped(cfg, os.Environ())[p.Agent] {
		lines = append(lines, corelaunch.MigrationLine(p.Agent, name))
	}
	return lines
}

type usageRecord struct {
	Role         string `json:"role"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	EffortCapped bool   `json:"effort_capped"`
	// Native is the native permission mode the worker started in.
	Native string `json:"native,omitempty"`
	run.Usage
}

// RecordUsage appends the same locked usage record as a delegation.
func RecordUsage(dir, role string, route coreroute.Route, result run.Result) error {
	return appendUsage(dir, role, route, result, "")
}

func appendUsage(dir, role string, route coreroute.Route, result run.Result, native string) error {
	unlock, err := store.LockFile(filepath.Join(dir, "usage.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = unlock() }()
	f, err := os.OpenFile(filepath.Join(dir, "usage.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(f).Encode(usageRecord{role, route.Agent, route.Model, route.Effort, route.EffortCapped, native, result.Usage})
	return errors.Join(writeErr, f.Close())
}

// snapshot asks Git for relevant working-tree paths without reading project files.
// Outside a repository (or when Git is unavailable), partial-work notes are skipped.
func snapshot(root string) (map[string]string, error) {
	data, err := project.Git(context.Background(), nil, "--no-optional-locks", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules", "--", ".", ":(exclude).duolog")
	if err != nil {
		return nil, nil //nolint:nilerr // outside Git there is no snapshot, and no changes are reported
	}
	result := make(map[string]string)
	records := bytes.Split(data, []byte{0})
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if len(record) < 4 {
			continue
		}
		status, path := record[:2], record[3:]
		sum := sha256.New()
		result[path] = fmt.Sprintf("%s %x", status, snapshotHash(root, filepath.FromSlash(path), sum))
		if strings.ContainsAny(status, "RC") && i+1 < len(records) {
			i++
			result[string(records[i])] = status + " -> " + path
		}
	}
	return result, nil
}
func changes(before, after map[string]string) []string {
	if before == nil || after == nil {
		return nil
	}
	var names []string
	for path, status := range after {
		if old, ok := before[path]; !ok || old != status {
			names = append(names, path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			names = append(names, path)
		}
	}
	sort.Strings(names)
	return names
}

// Snapshot and Changes expose the working-tree comparison to live peer sessions,
// which report unfinished work the same way a one-off worker does.
func Changes(before, after map[string]string) []string { return changes(before, after) }

// workerLines close every worker's input.
var workerLines = "\n" + caps.NeedsLine + "\n" + gateRetryLine + "\n" + ownershipLine + "\n" + workflowLine + "\n" + handOnLine + "\n" + decisionsLine + "\n" + summaryLine + "\n"

const summaryLine = "When everything went as planned, keep the summary to one short sentence; give details only for a problem, an open question or a choice you made."

// decisionsLine makes a worker name what it settled alone. In the cost study
// Codex workers decided one to three open points of a skeleton without saying
// so, and the lead could not see them in the diff.
const decisionsLine = "List in decisions each choice the task left open that you made yourself, such as a library, a name, a file layout or a default. Leave it empty when the task settled everything."

// gateRetryLine keeps a worker going after a refusal it can avoid: most refused
// commands only had quotes, globs or pipes the gate cannot read, and a stopped
// worker costs the lead a full restart.
const gateRetryLine = "If the gate refuses a command, retry once with a plainer form: one command, no pipes, globs, redirects or quoted text with spaces (search with rg -n -F word path). If that is refused too, stop and return done false with the decision in needs."

// maxBriefBytes bounds the brief: it is sent to every worker.
const maxBriefBytes = 32 * 1024

// withoutBridged drops the needs that bridged servers meet: a Claude worker
// whose only MCP needs are bridged starts with those servers and no others.
func withoutBridged(needs []string, bridged *run.MCP) []string {
	if bridged == nil {
		return needs
	}
	var out []string
	for _, need := range needs {
		kind, name, err := caps.ParseNeed(need)
		if err == nil && kind == caps.KindMCP && slices.Contains(bridged.Names, name) {
			continue
		}
		out = append(out, need)
	}
	return out
}

// ownershipLine keeps the steps that need the user's or the lead's judgment
// with the lead: a worker that commits or asks the user stalls or surprises the
// run. Project rules often ask for a changelog entry or a ticket; the task's
// Write section says whether this worker writes them.
const ownershipLine = "Do not commit, push or ask the user. Edit a changelog or docs only if Write lists them; otherwise the lead writes them, so do not ask about a ticket or an entry. Put any other such step in needs; the lead does it."

// workerBrief returns .duolog/brief.md, the project rules the lead keeps for
// every worker, as the first part of the worker's input. The same prefix in
// every task lets the provider serve it from its prompt cache.
// It must be a regular file, not a link that could point at a secret, and small.
func workerBrief(root string) []byte {
	path := filepath.Join(project.StateRoot(root), ".duolog", "brief.md")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBriefBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return append(append([]byte("Project brief from the lead (applies to every task):\n\n"), data...), []byte("\n\n")...)
}

// preflight checks the worker's login and the login of every MCP server the
// task needs, bridged or the worker's own. A bridged server is checked with
// the lead's definition, which is what the worker gets; the worker's own
// servers are those the settings folders of workerEnv, its environment,
// define.
func (d Deps) preflight(ctx context.Context, checker Checker, agent, leadAgent, root string, needs []string, bridged *run.MCP, workerEnv []string) []Problem {
	var out []Problem
	if p := checker.Agent(ctx, agent); p != nil {
		return append(out, *p)
	}
	own, lent := d.servers(agent, root, workerEnv), map[string]caps.Server{}
	if bridged != nil {
		lent = d.servers(leadAgent, root, os.Environ())
	}
	for _, need := range needs {
		kind, name, err := caps.ParseNeed(need)
		if err != nil || kind != caps.KindMCP {
			continue
		}
		var def *caps.Server
		if bridged != nil && slices.Contains(bridged.Names, name) {
			if s, ok := lent[name]; ok {
				def = &s
			}
		} else if s, ok := own[name]; ok {
			def = &s
		}
		if def != nil && def.Inactive {
			// The agent would not start it; a check would send its credentials
			// to a server the person never approved.
			continue
		}
		if p := checker.Server(ctx, agent, name, def); p != nil {
			out = append(out, *p)
		}
	}
	return out
}

// runProblems reads a refused login from a finished run: an error the runner
// classified, or auth: entries the worker put in needs.
// The worker's own servers are those of workerEnv, its environment.
func (d Deps) runProblems(agent, leadAgent, root string, r run.Result, bridged *run.MCP, taskNeeds, workerEnv []string) []Problem {
	// A finished run stays finished, whatever its log said about a login.
	if r.Done {
		return nil
	}
	var problems []Problem
	for _, p := range fromNeeds(agent, r.Needs) {
		// The worker writes these needs, so a server counts only when the task
		// named it: its name reaches a login command and the line for the lead.
		if p.Server == "" || slices.Contains(taskNeeds, caps.KindMCP+":"+p.Server) {
			problems = append(problems, p)
		}
	}
	// The server name comes from the worker's errors, so it too counts only
	// when the task named it.
	if r.AuthFailed && (r.AuthServer == "" || slices.Contains(taskNeeds, caps.KindMCP+":"+r.AuthServer)) {
		p := Problem{Agent: agent, Server: r.AuthServer, Reason: "the worker's agent refused its login"}
		if p.Server != "" {
			p.Reason = "the server refused the worker's credentials"
		}
		problems = append([]Problem{p}, problems...)
	}
	for i, p := range problems {
		if p.Server == "" {
			continue
		}
		defs := d.servers(agent, root, workerEnv)
		if bridged != nil && slices.Contains(bridged.Names, p.Server) {
			defs = d.servers(leadAgent, root, os.Environ())
		}
		if def, ok := defs[p.Server]; ok && def.Transport == "http" && def.Static() {
			problems[i].Static, problems[i].Where = true, def.File
		} else if ok && def.Transport != "http" {
			// A stdio server has no login to run: its credentials are in env.
			problems[i].Static, problems[i].Where = true, def.File
		}
	}
	return problems
}

// settleAuth decides what a refused login does to the run. It returns true
// when the worker may start, after a login that worked. Otherwise the task goes
// to the lead, as with on_missing lead_first, and out says why; the reason
// names the agent or server and the config entry, never a value.
func settleAuth(ctx context.Context, d Deps, cfg *config.Config, reg *session.Registry, checker Checker, leadID, role string, p Problem, tried *bool, out *Outcome) (bool, error) {
	// A problem from a worker's needs or its log has no login command yet.
	p = d.withLogin(p)
	action := cfg.Capabilities.OnAuthFailure
	if action == "" {
		action = AuthLoginThenLead
	}
	reason := p.Subject() + ": " + p.Reason
	// An agent login refused while a dropped key is in use most likely relied
	// on that key. A browser login would switch the person's billing without
	// asking, so none runs and the lead gets the migration line.
	if migration := strings.Join(migrationLines(d.DroppedKeys, cfg, p), " "); migration != "" {
		if action == AuthFail {
			return false, fmt.Errorf("%s; %s", reason, migration)
		}
		out.Needs = []string{p.Need()}
		out.Line = oneLine("role " + role + " waits for lead: " + reason + "; " + migration + " Do the task here, or allow paid runs in paid_runs." + p.Agent + " and delegate again.")
		return false, nil
	}
	if action == AuthFail {
		return false, fmt.Errorf("%s", reason)
	}
	command := p.Command()
	switch {
	case p.Static:
		reason += "; fix the credential in " + p.Where
		command = ""
	case action == AuthLoginThenLead && !*tried:
		declined, err := reg.AuthDeclined(leadID, p.Key())
		if err != nil {
			return false, err
		}
		if declined {
			reason += "; the login was declined earlier in this session"
			break
		}
		*tried = true
		timeout := time.Duration(cfg.Capabilities.LoginTimeoutSeconds) * time.Second
		switch checker.Login(ctx, p, timeout) {
		case LoggedIn:
			var again *Problem
			if p.Server == "" {
				again = checker.Agent(ctx, p.Agent)
			} else if !p.Static {
				again = checker.Server(ctx, p.Agent, p.Server, nil)
			}
			if again == nil {
				return true, nil
			}
			reason += "; still refused after the login"
		case NeedsTerminal:
			out.Needs = []string{p.AuthNeed()}
			out.Line = oneLine("role " + role + " waits for lead: " + reason + "; the login needs a terminal. Ask the person to run: ! " + command)
			return false, nil
		case TimedOut:
			// Not recorded as declined: the person may finish it from a terminal.
			reason += "; the login timed out"
		default:
			if err := reg.DeclineAuth(leadID, p.Key()); err != nil {
				return false, err
			}
			reason += "; the login was closed or failed"
		}
	}
	if command != "" {
		reason += "; to log in, run: ! " + command
	}
	tail := " Do this part first and pass the artifact in Read."
	if p.Server == "" {
		tail = " Do the task here, or log in and delegate again."
	}
	out.Needs = []string{p.Need()}
	out.Line = oneLine("role " + role + " waits for lead: " + reason + "." + tail)
	return false, nil
}
