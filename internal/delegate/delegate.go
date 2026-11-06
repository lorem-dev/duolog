// Package delegate coordinates task handoffs, reusable threads and one fallback.
package delegate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"crypto/sha256"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/auth"
	"github.com/lorem-dev/duolog/internal/bus"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/gate"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/models"
	"github.com/lorem-dev/duolog/internal/paidruns"
	"github.com/lorem-dev/duolog/internal/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/router"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/task"
	"github.com/lorem-dev/duolog/internal/trust"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

type Outcome struct {
	Result  runner.Result
	RunHere *Placement
	Line    string
	// Needs names the tools the worker or the capability check asked the lead
	// for. It is not a failure: the caller gives it a separate exit code.
	Needs []string
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
	Budget  *paidruns.StepBudget
	Waiting func(bool)
	// Observe receives every worker run, failed ones included, with learning
	// on or off; nil observes nothing.
	Observe Observer
}
type optionsKey struct{}

func WithOptions(ctx context.Context, opts Options) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

// OptionsFrom returns the options Run takes from ctx.
func OptionsFrom(ctx context.Context) Options {
	opts, _ := ctx.Value(optionsKey{}).(Options)
	return opts
}

var runWorker = runner.Run
var resolveModel = models.Resolve
var readLeadState = session.ReadLeadState
var scanCapabilities = capability.Scan
var bridgeMCP = capability.Bridge

// newChecker checks a worker's logins on the worker's own built process.
var newChecker = auth.NewForProcess

func Place(cfg *config.Config, taskPath string, route router.Route) (Placement, error) {
	root, err := os.Getwd()
	if err != nil {
		return Placement{}, err
	}
	return place(cfg, taskPath, root, route)
}
func place(cfg *config.Config, taskPath, root string, route router.Route) (Placement, error) {
	p := Placement{Where: "self", Model: route.Model, Effort: route.Effort, Task: taskPath}
	refs, err := task.References(taskPath, root)
	if err != nil {
		return p, err
	}
	files := make(map[string]bool)
	lines := 0
	for _, ref := range refs {
		files[ref.Path] = true
		data, err := ref.Text(root)
		if err != nil {
			p.Where = "subagent"
			return p, nil
		}
		lines += strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			lines++
		}
	}
	if len(files) > cfg.Limits.SelfMaxReadFiles || lines > cfg.Limits.SelfMaxReadLines {
		p.Where = "subagent"
	}
	return p, nil
}

func Run(ctx context.Context, cfg *config.Config, s *store.Store, role, taskPath, thread string) (out Outcome, err error) {
	started := time.Now()
	opts := OptionsFrom(ctx)
	// workerStarted says a worker ran; until then every refusal is a
	// NotStartedError. A calibration step needs a worker's result, so for one
	// a placement in the lead or a wait for the lead is a refusal too.
	workerStarted := false
	defer func() {
		if workerStarted {
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
	if workerenv.IsWorker() {
		return out, fmt.Errorf("nested delegation refused (DUOLOG_DEPTH=%s)", os.Getenv("DUOLOG_DEPTH"))
	}
	for _, need := range opts.Needs {
		if _, _, err = capability.ParseNeed(need); err != nil {
			return out, err
		}
	}
	root, err := filepath.Abs(filepath.Dir(s.Dir))
	if err != nil {
		return out, err
	}
	root = project.CurrentWorkRoot(root)
	lookup := pathutil.Native(taskPath)
	if !filepath.IsAbs(lookup) {
		if cwd, e := os.Getwd(); e == nil {
			candidate := filepath.Join(cwd, lookup)
			if _, e := os.Stat(candidate); e == nil {
				lookup = candidate
			}
		}
	}
	source, err := task.Resolve(root, lookup)
	if err != nil {
		return out, fmt.Errorf("task file: %w; write it inside the project, such as .duolog/task.md, which Git ignores", err)
	}
	if err = task.Validate(source); err != nil {
		return out, err
	}
	// The paths Write lists, to name what the worker changed outside them.
	writes, err := task.WritePaths(source)
	if err != nil {
		return out, err
	}
	original, err := os.ReadFile(source)
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
	if err = os.MkdirAll(tasksDir, 0755); err != nil {
		return out, err
	}
	dir, err := os.MkdirTemp(tasksDir, "task-")
	if err != nil {
		return out, err
	}
	copyPath := filepath.Join(dir, "task.md")
	if err = os.WriteFile(copyPath, original, 0600); err != nil {
		return out, err
	}
	reg, err := session.Open(s.Dir)
	if err != nil {
		return out, err
	}
	lead, err := reg.ResolveLead(opts.Lead)
	if err != nil {
		return out, err
	}
	lead, leadMode, err := observedLead(reg, lead, started)
	if err != nil {
		return out, err
	}
	leadID, leadAgent, address := lead.ID, lead.Agent, session.Address(lead)
	leadTarget := &lead.Target
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
	leadRules, err := gate.ImportLead(root, lead.Agent)
	if err != nil {
		return out, fmt.Errorf("lead permissions: %w", err)
	}
	b, err := bus.Open(s.Dir, cfg.Wire)
	if err != nil {
		return out, err
	}
	ref := filepath.ToSlash(filepath.Join("tasks", filepath.Base(dir), "task.md"))
	worker := "duolog/worker"
	warning := ""
	// notices are migration lines claimed for this lead and the line of a
	// free run that spent paid overage; every line that ends this run
	// carries them, whichever way it ends.
	var notices []string
	var firstFailure error
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
		extra := ""
		for _, n := range notices {
			if !strings.Contains(out.Line, n) {
				extra += "; " + n
			}
		}
		if len(out.Line)+len(extra) > maxLine {
			// A claimed notice does not come again in this lead, so it leaves
			// the text that is cut and follows it whole.
			extra = ""
			for _, n := range notices {
				out.Line = withoutNotice(out.Line, n)
				extra += "; " + n
			}
			tail := " ... [cut; " + answerPointer(dir) + "]"
			out.Line = short(out.Line, max(0, maxLine-len(tail)-len(extra))) + tail
		}
		if out.Line == "" {
			extra = strings.TrimPrefix(extra, "; ")
		}
		out.Line += extra
	}()
	unavailable, err := reg.Unavailable()
	if err != nil {
		return out, err
	}
	// Every worker run is admitted by the account gate, one permit per run.
	accounts, err := NewGate(cfg)
	if err != nil {
		return out, err
	}
	// Every free candidate of the role, use then fallback, comes before any
	// paid one: a free pass, then a paid pass over the agents that allow it.
	passes, paidAgents := variants(cfg, role, opts)
	pass, variant := 0, passes[0]
	nextPass := func() bool {
		if pass+1 >= len(passes) {
			return false
		}
		pass++
		variant = passes[pass]
		unavailable = map[string]bool{}
		for _, agent := range []string{"claude", "codex"} {
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
	capNotes := ""
	// Pace balancing reads the quota once per task; a pinned target is never
	// balanced, and the paid pass is never balanced either.
	var balance *router.Balance
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
		ro := router.Options{Lead: leadTarget, AllowManualModel: opts.AllowManualModel}
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
		route, routeErr := router.Resolve(cfg, role, unavailable, ro)
		if routeErr != nil {
			// No candidate is left in this pass.
			if (attempt > 0 || pass > 0) && later() {
				attempt = 1
				continue
			}
			return out, routeErr
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
			out.Line = p.String()
			if len(refusals) > 0 || len(failures) > 0 {
				out.Line = leadLine(role, append(failures, refusals...), p)
			}
			return out, nil
		}
		capNotes = ""
		// The bridge must not replace a variable the worker already gets.
		runEnv := []string{"DUOLOG_RUN=" + att.run}
		var base workerenv.Process
		if base, err = workerEnv(workerInput(cfg, route, variant, leadID, role, root, opts.ProtectedRoots, runEnv)); err != nil {
			return out, err
		}
		env := base.Env
		var bridge []string
		var bridged *runner.MCP
		if len(opts.Needs) > 0 {
			target, scanErr := scanCapabilities(ctx, route.Agent, root)
			if scanErr != nil {
				return out, scanErr
			}
			lead := capability.Inventory{Agent: leadAgent, Unknown: true}
			if leadAgent != "" {
				if lead, scanErr = scanCapabilities(ctx, leadAgent, root); scanErr != nil {
					return out, scanErr
				}
			}
			var notes, lacking []string
			reroute := false
			bridge = nil
			for _, r := range capability.Resolve(target, lead, opts.Needs, cfg.Capabilities) {
				switch r.Action {
				case capability.ActionBridge:
					bridge = append(bridge, strings.TrimPrefix(r.Need, capability.KindMCP+":"))
				case capability.ActionRefuse:
					out.Line = oneLine("role " + role + ": " + r.Hint)
					return out, fmt.Errorf("capability %s missing: %s", r.Need, r.Hint)
				case capability.ActionLeadFirst:
					lacking = append(lacking, r.Need)
					notes = append(notes, r.Hint)
				case capability.ActionReroute:
					reroute = true
					notes = append(notes, r.Hint)
				default:
					if r.Hint != "" {
						notes = append(notes, r.Hint)
					}
				}
			}
			bridged = nil
			if len(bridge) > 0 {
				var failed map[string]string
				bridged, failed = bridgeMCP(leadAgent, route.Agent, root, bridge, cfg.Capabilities.MCPTools, env)
				for _, name := range bridge {
					if why, ok := failed[name]; ok {
						need := capability.KindMCP + ":" + name
						lacking = append(lacking, need)
						notes = append(notes, capability.BridgeFallback(need, route.Agent, why))
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
			if needsKind(opts.Needs, capability.KindMCP) {
				notes = append(notes, auth.WorkerLine)
			}
			for _, n := range notes {
				capNotes += n + "\n"
			}
			if capNotes != "" {
				if err = os.WriteFile(copyPath, []byte(annotate(string(original), capNotes)), 0600); err != nil {
					return out, err
				}
				if err = task.Validate(copyPath); err != nil {
					return out, err
				}
			}
		}
		state, trustErr := trust.Hooks(route.Agent, root)
		if trustErr != nil {
			return out, trustErr
		}
		folder, trustErr := trust.Folder(route.Agent, root)
		if trustErr != nil {
			return out, trustErr
		}
		if warning, err = trustWarning(route, role, state, folder); err != nil {
			return out, err
		}
		if bridged != nil {
			warning += "; bridged mcp:" + strings.Join(bridged.Names, ",mcp:") + " from " + leadAgent
		}
		// The brief and the worker lines count against the inline limit, so a large
		// brief leaves less room for Read files instead of growing every handoff.
		brief := workerBrief(root)
		budget := cfg.Wire.InlineMaxBytes - len(brief) - len(workerLines)
		if budget <= 0 {
			return out, fmt.Errorf(".duolog/brief.md and the worker lines take %d of wire.inline_max_bytes (%d); shorten the brief", len(brief)+len(workerLines), cfg.Wire.InlineMaxBytes)
		}
		readRules := leadRules
		readRules.RoleSandbox = route.Sandbox
		inline, inlineErr := task.Inline(copyPath, root, budget, func(path string) bool {
			if pathutil.IsAbs(path) {
				if rel, err := pathutil.Relative(root, path); err == nil {
					path = rel
				}
			}
			// The same work root and store roots the worker's gate hook gets, so a
			// Read reference cannot hand a private store over before that hook runs.
			read := gate.Action{Kind: "read", Target: pathutil.Clean(path), Role: role, Work: root, ProtectedRoots: opts.ProtectedRoots}
			return gate.Evaluate(cfg.Policy, readRules, leadMode, read).Kind == gate.Allow
		})
		if inlineErr != nil {
			return out, inlineErr
		}
		claimed, noticeErr := ClaimMigration(reg, cfg, leadID, route.Agent)
		if noticeErr != nil {
			return out, noticeErr
		}
		notices = append(notices, claimed...)
		// The worker's process, built after every choice that moves its route:
		// the variant, the context and the bridged servers' env. Its login check,
		// its quota read and its run all use it.
		extra := runEnv
		if bridged != nil {
			extra = append(slices.Clone(runEnv), bridged.Env...)
		}
		in := workerInput(cfg, route, variant, leadID, role, root, opts.ProtectedRoots, extra)
		in.ProviderEnvKey = accounts.Cache.ProviderEnvKey(route.Agent)
		if accounts.Layers != nil {
			if in.Layers, err = accounts.Layers(ctx, in); err != nil {
				return out, err
			}
		}
		var pre workerenv.Process
		if pre, err = workerEnv(in); err != nil {
			return out, err
		}
		checker := newChecker(cacheDir, pre)
		// A worker that cannot sign in would fail after the lead waited for it,
		// so its agent and the servers it needs are checked first.
		// The servers are checked only once the agent is logged in, so a login
		// that works checks them next.
		for problems := preflight(ctx, checker, route.Agent, leadAgent, root, opts.Needs, bridged); len(problems) > 0; {
			p := problems[0]
			ok, authErr := settleAuth(ctx, cfg, reg, checker, leadID, role, p, &loginTried, &out)
			if authErr != nil || !ok {
				return out, authErr
			}
			if p.Server == "" {
				problems = preflight(ctx, checker, route.Agent, leadAgent, root, opts.Needs, bridged)
			} else {
				problems = problems[1:]
			}
		}
		model, modelErr := resolveModel(ctx, cfg, filepath.Join(s.Dir, "cache"), route.Target)
		if modelErr != nil {
			if missingBinary(modelErr) {
				err = modelErr
			} else {
				return out, modelErr
			}
		} else {
			route.Model = model
			stdin := append(append(brief, inline...), workerLines...)
			spec := runner.Spec{Route: route, Mode: leadMode, TaskDir: dir, Cwd: root, Prompt: capability.NeedsLine, Stdin: stdin, Persistent: route.Session == "resume", KeepMCP: needsKind(withoutBridged(opts.Needs, bridged), capability.KindMCP, capability.KindPlugin), KeepSkills: needsKind(opts.Needs, capability.KindSkill, capability.KindPlugin), MCP: bridged, ProtectedRoots: opts.ProtectedRoots}
			spec.MCP = limitOwn(spec, opts.Needs, cfg.Capabilities.MCPTools)
			spec.MaxTurns = opts.MaxTurns
			if route.Sandbox == "read-only" {
				spec.Mode = mode.Plan
			}
			var entry *session.Entry
			if route.Session == "resume" {
				entry, _, err = reg.Lookup(leadID, route.Agent, role, thread)
				if err != nil {
					return out, err
				}
				if entry != nil {
					over := entry.Input > int64(cfg.Sessions.ResumeMaxInputTokens)
					idle := cfg.Sessions.IdleCloseMinutes > 0 && time.Since(entry.UpdatedAt) > time.Duration(cfg.Sessions.IdleCloseMinutes)*time.Minute
					if over || idle {
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
				return a, nil, a.sameRoute(ctx, pre, cacheDir)
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
				run := att.begin()
				workerStarted = true
				out.Result, err = func() (runner.Result, error) {
					defer reg.SetWaiting(leadID, token, 0)
					return runWorker(ctx, spec)
				}()
				att.worker(run, route, retry, out.Result, err)
				// Settled once, with what this run measured, before a retry
				// replaces the result.
				if settleErr := current.finish(ctx, cfg, route, out.Result); settleErr != nil {
					return out, errors.Join(err, settleErr)
				}
				// A run admitted as free that spent paid overage: the ledger
				// charged it, admission now sees subscription_overage, and
				// every line that ends this delegation says so.
				if current.permit.Assessment.Verdict != paidruns.Paid && out.Result.Billing.UsingOverage {
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
				if usageErr := appendUsage(s.Dir, role, route, out.Result); usageErr != nil {
					return out, errors.Join(err, usageErr)
				}
				problems := runProblems(route.Agent, leadAgent, root, out.Result, bridged, opts.Needs)
				if len(problems) == 0 || out.Result.Interrupted || out.Result.QuotaEnded {
					break
				}
				// A login that works lets the worker run once more; settleAuth
				// tries one login per delegation, so this ends.
				ok, authErr := settleAuth(ctx, cfg, reg, checker, leadID, role, problems[0], &loginTried, &out)
				if authErr != nil || !ok {
					out.Result = runner.Result{}
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
					e := session.Entry{ID: filepath.Base(dir), Lead: leadID, Agent: route.Agent, Role: role, Thread: thread, ThreadID: sessionID, Input: out.Result.Usage.Input}
					if entry != nil {
						e.ID = entry.ID
						e.Input += entry.Input
					}
					if err = reg.RecordLimited(e, cfg.Limits.MaxLiveSessions); err != nil {
						return out, err
					}
				}
				needs := ""
				if len(out.Result.Needs) > 0 {
					out.Needs = out.Result.Needs
					needs = " needs=" + strings.Join(out.Result.Needs, ",")
				}
				if len(out.Result.Decisions) > 0 {
					needs += fmt.Sprintf(" decisions=%d", len(out.Result.Decisions))
				}
				lineWarning := warning
				if before != nil {
					if after, snapErr := snapshot(root); snapErr == nil {
						if outside := outsideWrite(changes(before, after), writes); len(outside) > 0 {
							lineWarning += "; outside=" + strings.Join(outside, ",")
						}
					}
				}
				lineWarning += lr.note()
				for _, n := range notices {
					lineWarning += "; " + n
				}
				out.Line = resultLine(out.Result.Done, needs, out.Result.Summary, strings.Join(out.Result.Files, ","), lineWarning, answerPointer(dir))
				return out, nil
			}
			if out.Result.QuotaEnded {
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
	// The gate refused every candidate left: the task goes to the lead with
	// one line, as a task whose needs no worker meets does, in the placement
	// of the lead's fallback when the router chose one.
	p := Placement{Where: "self", Task: placementTask}
	if inLead != nil {
		p = *inLead
	}
	out.RunHere = &p
	out.Line = leadLine(role, append(failures, refusals...), p)
	return out, nil
}

// annotate puts lines at the start of the Limits section.
func annotate(text, lines string) string {
	return task.Annotate(text, lines)
}
func missingBinary(err error) bool {
	var pathErr *os.PathError
	return errors.Is(err, exec.ErrNotFound) || (errors.As(err, &pathErr) && pathErr.Op == "fork/exec" && errors.Is(err, os.ErrNotExist))
}

// ClaimMigration returns a migration line for each billing key that agent's
// workers no longer get, the first time this lead launches the agent.
// Delegate and peer open share the claim, so the lead reads each line once.
// Without a lead there is nothing to remember it by, and the lines come every
// time.
func ClaimMigration(reg *session.Registry, cfg *config.Config, leadID, agent string) ([]string, error) {
	var lines []string
	for _, name := range workerenv.DroppedKeys(cfg, os.Environ())[agent] {
		claimed := true
		if leadID != "" {
			var err error
			if claimed, err = reg.ClaimNotice(leadID, "migration:"+agent+":"+name); err != nil {
				return lines, err
			}
		}
		if claimed {
			lines = append(lines, workerenv.MigrationLine(agent, name))
		}
	}
	return lines, nil
}

// withoutNotice takes the last copy of notice out of line, with the "; " that
// leads it.
func withoutNotice(line, notice string) string {
	i := strings.LastIndex(line, notice)
	if i < 0 {
		return line
	}
	head := strings.TrimSuffix(strings.TrimRight(line[:i], " "), ";")
	return oneLine(head + " " + line[i+len(notice):])
}

// migrationLines names the billing keys a refused agent login may have lost.
func migrationLines(cfg *config.Config, p auth.Problem) []string {
	if p.Server != "" {
		return nil
	}
	var lines []string
	for _, name := range workerenv.DroppedKeys(cfg, os.Environ())[p.Agent] {
		lines = append(lines, workerenv.MigrationLine(p.Agent, name))
	}
	return lines
}

// observedLead brings the registered lead up to date with its transcript, and
// returns it with the mode that limits workers.
func observedLead(reg *session.Registry, lead session.Lead, started time.Time) (session.Lead, mode.Mode, error) {
	if lead.TranscriptPath != "" {
		observed, _ := readLeadState(lead.Agent, lead.TranscriptPath)
		if observed.Model != "" {
			lead.Model = observed.Model
		}
		if observed.Effort != "" {
			lead.Effort = observed.Effort
		}
		if lead.Agent == "codex" {
			if observed.Mode != "" {
				lead.Mode = observed.Mode
			} else {
				lead.Mode = mode.Lower(lead.Mode, mode.Ask)
			}
		}
		o := session.LeadObservation{ID: lead.ID, Agent: lead.Agent, Model: &lead.Model, Effort: &lead.Effort}
		if lead.Agent == "codex" {
			o.Mode = &lead.Mode
		}
		var err error
		if lead, err = reg.ObserveLead(o, started); err != nil {
			return lead, mode.Ask, err
		}
	}
	return reg.EffectiveMode(lead.ID)
}

// trustWarning says what the lead must know about the worker's rules. A write role
// with untrusted Codex hooks would run with no gate, so it does not start.
func trustWarning(route router.Route, role string, state trust.State, folder bool) (string, error) {
	warning := ""
	if route.Agent == "claude" && !state.Installed {
		warning = "; Claude has no duolog hooks: rules are off; sandbox only"
	}
	if !state.Trusted {
		if route.Sandbox != "read-only" {
			return "", fmt.Errorf("Codex hooks untrusted; open /hooks before running write role %s", role)
		}
		warning = "; rules are off; open /hooks"
	}
	if !folder {
		warning += "; folder trust missing"
	}
	return warning, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// maxLine is what the lead reads for one worker. It costs tokens on every turn.
const maxLine = 512

// resultLine gives the summary only the room that needs, files and warnings leave.
// A warning such as "rules are off" or "outside=" must reach the lead, so a line
// that is still too long after the summary is cut gets its files list cut at a
// comma, never a warning. The summary and the files are also in answer.json.
func resultLine(done bool, needs, summary, files, warning, pointer string) string {
	line := func(summary, files string) string {
		return oneLine(fmt.Sprintf("done=%t%s summary=%s files=%s%s", done, needs, summary, files, warning))
	}
	summary = oneLine(summary)
	if over := len(line(summary, files)) - maxLine; over > 0 {
		cut := " ... [cut; " + pointer + "]"
		summary = short(summary, max(0, len(summary)-over-len(cut))) + cut
		if over = len(line(summary, files)) - maxLine; over > 0 {
			// A cut list ends in ... so that it does not read as the whole list.
			const more = ",..."
			files = short(files, max(0, len(files)-over-len(more)))
			if i := strings.LastIndex(files, ","); i >= 0 {
				files = files[:i] + more
			} else {
				files = more[1:]
			}
		}
	}
	return line(summary, files)
}

// answerPointer names the answer as a path from the current folder, which differs
// from the state folder in a linked worktree. A worker that failed wrote none.
func answerPointer(dir string) string {
	path := filepath.Join(dir, "answer.json")
	label := "answer="
	if _, err := os.Stat(path); err != nil {
		path, label = dir, "task="
	}
	if cwd, err := os.Getwd(); err == nil {
		if relative, err := filepath.Rel(cwd, path); err == nil {
			path = relative
		}
	}
	return label + filepath.ToSlash(path)
}
func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

type usageRecord struct {
	Role         string `json:"role"`
	Agent        string `json:"agent"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	EffortCapped bool   `json:"effort_capped"`
	runner.Usage
}

// RecordUsage appends the same locked usage record as a delegation.
func RecordUsage(dir, role string, route router.Route, result runner.Result) error {
	return appendUsage(dir, role, route, result)
}

func appendUsage(dir, role string, route router.Route, result runner.Result) error {
	lock := flock.New(filepath.Join(dir, "usage.lock"))
	if err := lock.Lock(); err != nil {
		_ = lock.Close()
		return err
	}
	defer lock.Close()
	f, err := os.OpenFile(filepath.Join(dir, "usage.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writeErr := json.NewEncoder(f).Encode(usageRecord{role, route.Agent, route.Model, route.Effort, route.EffortCapped, result.Usage})
	return errors.Join(writeErr, f.Close())
}

// snapshot asks Git for relevant working-tree paths without reading project files.
// Outside a repository (or when Git is unavailable), partial-work notes are skipped.
func snapshot(root string) (map[string]string, error) {
	cmd := exec.Command("git", "--no-optional-locks", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules", "--", ".", ":(exclude).duolog")
	data, err := cmd.Output()
	if err != nil {
		return nil, nil
	}
	result := make(map[string]string)
	records := bytes.Split(data, []byte{0})
	for i := 0; i < len(records); i++ {
		record := string(records[i])
		if len(record) < 4 {
			continue
		}
		status, path := record[:2], record[3:]
		data, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		result[path] = fmt.Sprintf("%s %x", status, sha256.Sum256(data))
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
func Snapshot(root string) (map[string]string, error)  { return snapshot(root) }
func Changes(before, after map[string]string) []string { return changes(before, after) }

// workerLines close every worker's input.
var workerLines = "\n" + capability.NeedsLine + "\n" + gateRetryLine + "\n" + ownershipLine + "\n" + decisionsLine + "\n" + summaryLine + "\n"

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

// needsKind reports whether a task names a need of one of these kinds. A Claude
// worker keeps MCP servers only for an mcp or plugin need, since a plugin can
// bring a server, and a minimal worker keeps skills only for a skill or plugin
// need.
func needsKind(needs []string, kinds ...string) bool {
	for _, need := range needs {
		if kind, _, err := capability.ParseNeed(need); err == nil && slices.Contains(kinds, kind) {
			return true
		}
	}
	return false
}

// withoutBridged drops the needs that bridged servers meet: a Claude worker
// whose only MCP needs are bridged starts with those servers and no others.
func withoutBridged(needs []string, bridged *runner.MCP) []string {
	if bridged == nil {
		return needs
	}
	var out []string
	for _, need := range needs {
		kind, name, err := capability.ParseNeed(need)
		if err == nil && kind == capability.KindMCP && slices.Contains(bridged.Names, name) {
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
// the lead's definition, which is what the worker gets.
func preflight(ctx context.Context, checker auth.Checker, agent, leadAgent, root string, needs []string, bridged *runner.MCP) []auth.Problem {
	var out []auth.Problem
	if p := checker.Agent(ctx, agent); p != nil {
		return append(out, *p)
	}
	own, lent := capability.Servers(agent, root), map[string]capability.Server{}
	if bridged != nil {
		lent = capability.Servers(leadAgent, root)
	}
	for _, need := range needs {
		kind, name, err := capability.ParseNeed(need)
		if err != nil || kind != capability.KindMCP {
			continue
		}
		var def *capability.Server
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
func runProblems(agent, leadAgent, root string, r runner.Result, bridged *runner.MCP, taskNeeds []string) []auth.Problem {
	// A finished run stays finished, whatever its log said about a login.
	if r.Done {
		return nil
	}
	var problems []auth.Problem
	for _, p := range auth.FromNeeds(agent, r.Needs) {
		// The worker writes these needs, so a server counts only when the task
		// named it: its name reaches a login command and the line for the lead.
		if p.Server == "" || slices.Contains(taskNeeds, capability.KindMCP+":"+p.Server) {
			problems = append(problems, p)
		}
	}
	// The server name comes from the worker's errors, so it too counts only
	// when the task named it.
	if r.AuthFailed && (r.AuthServer == "" || slices.Contains(taskNeeds, capability.KindMCP+":"+r.AuthServer)) {
		p := auth.Problem{Agent: agent, Server: r.AuthServer, Reason: "the worker's agent refused its login"}
		if p.Server != "" {
			p.Reason = "the server refused the worker's credentials"
		}
		problems = append([]auth.Problem{p}, problems...)
	}
	for i, p := range problems {
		if p.Server == "" {
			continue
		}
		defs := capability.Servers(agent, root)
		if bridged != nil && slices.Contains(bridged.Names, p.Server) {
			defs = capability.Servers(leadAgent, root)
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
func settleAuth(ctx context.Context, cfg *config.Config, reg *session.Registry, checker auth.Checker, leadID, role string, p auth.Problem, tried *bool, out *Outcome) (bool, error) {
	action := cfg.Capabilities.OnAuthFailure
	if action == "" {
		action = auth.LoginThenLead
	}
	reason := p.Subject() + ": " + p.Reason
	// An agent login refused while a dropped key is in use most likely relied
	// on that key. A browser login would switch the person's billing without
	// asking, so none runs and the lead gets the migration line.
	if migration := strings.Join(migrationLines(cfg, p), " "); migration != "" {
		if action == auth.Fail {
			return false, fmt.Errorf("%s; %s", reason, migration)
		}
		out.Needs = []string{p.Need()}
		out.Line = oneLine("role " + role + " waits for lead: " + reason + "; " + migration + " Do the task here, or allow paid runs in paid_runs." + p.Agent + " and delegate again.")
		return false, nil
	}
	if action == auth.Fail {
		return false, fmt.Errorf("%s", reason)
	}
	command := p.Command()
	switch {
	case p.Static:
		reason += "; fix the credential in " + p.Where
		command = ""
	case action == auth.LoginThenLead && !*tried:
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
		case auth.LoggedIn:
			var again *auth.Problem
			if p.Server == "" {
				again = checker.Agent(ctx, p.Agent)
			} else if !p.Static {
				again = checker.Server(ctx, p.Agent, p.Server, nil)
			}
			if again == nil {
				return true, nil
			}
			reason += "; still refused after the login"
		case auth.NeedsTerminal:
			out.Needs = []string{p.AuthNeed()}
			out.Line = oneLine("role " + role + " waits for lead: " + reason + "; the login needs a terminal. Ask the person to run: ! " + command)
			return false, nil
		case auth.TimedOut:
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

// limitOwn adds capabilities.mcp_tools for the worker's own servers that the
// task needs, so that the list filters them as it filters a bridged server.
func limitOwn(spec runner.Spec, needs []string, tools map[string][]string) *runner.MCP {
	var own []string
	for _, need := range withoutBridged(needs, spec.MCP) {
		if kind, name, err := capability.ParseNeed(need); err == nil && kind == capability.KindMCP {
			own = append(own, name)
		}
	}
	if spec.Route.Agent == "codex" {
		// enabled_tools for a server Codex does not define, such as a plugin's,
		// would make a table with no transport, which Codex refuses.
		defs := capability.Servers("codex", spec.Cwd)
		own = slices.DeleteFunc(own, func(name string) bool { _, ok := defs[name]; return !ok })
	}
	codexArgs, allowed := capability.Limit(spec.Route.Agent, own, tools)
	m := spec.MCP
	if m == nil {
		if len(codexArgs) == 0 && len(allowed) == 0 {
			return nil
		}
		m = &runner.MCP{}
	} else {
		copied := *m
		m = &copied
	}
	m.CodexArgs = append(append([]string(nil), m.CodexArgs...), codexArgs...)
	m.AllowedTools = append(append([]string(nil), m.AllowedTools...), allowed...)
	return m
}

// outsideWrite lists the changed paths that no Write entry covers: a file, a
// folder that holds it, or a glob. A task whose Write lists no path gets no
// list, since it did not say what the worker may change. A change the lead
// made while the worker ran shows up here too.
func outsideWrite(changed, writes []string) []string {
	if len(writes) == 0 {
		return nil
	}
	var outside []string
	for _, name := range changed {
		clean := pathutil.Clean(name)
		covered := false
		for _, w := range writes {
			match, _ := doublestar.Match(w, clean)
			if match || w == "." || clean == w || strings.HasPrefix(clean, w+"/") {
				covered = true
				break
			}
		}
		if !covered {
			outside = append(outside, name)
		}
	}
	return outside
}
