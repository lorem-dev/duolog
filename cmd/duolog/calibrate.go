package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/app/calibrate"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/profile"
	"github.com/lorem-dev/duolog/internal/core/budget"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/spf13/cobra"
)

// surveyChecks runs the baseline checks; tests replace it with a fake.
var surveyChecks calibration.CheckRunner = profile.RunChecks

// mineChecks runs the class checks that validate mined tasks, and mineGH
// the gh CLI for pull request texts; tests replace both.
var (
	mineChecks calibration.CheckRunner = profile.RunChecks
	mineGH     calibration.GH          = runGH
)

// cellChecks runs the class checks of a calibration cell; tests replace it.
var cellChecks calibration.CheckRunner = profile.RunChecks

func runGH(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// calibrationDone is the step of a run that has nothing left to do; any
// other step is unfinished.
const calibrationDone = "done"

// calibrationNext names the command after each step.
var calibrationNext = budget.NextStep

func newCalibrateCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "calibrate", Short: "Build a starting model order for this project, on the person's request", Args: cobra.NoArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if isWorker() {
				return fmt.Errorf("workers cannot run duolog calibrate")
			}
			return nil
		}}
	cmd.AddCommand(newCalibrateSurveyCommand(), newCalibrateClassesCommand(), newCalibrateMineCommand(), newCalibratePlanCommand(), newCalibrateRunCommand(), newCalibrateResumeCommand(), newCalibrateJudgeCommand(), newCalibrateDropCommand(), newCalibrateProposeCommand(), newCalibrateStatusCommand())
	return cmd
}

func newCalibrateSurveyCommand() *cobra.Command {
	var fresh bool
	cmd := &cobra.Command{Use: "survey", Short: "Survey the project and start a calibration run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if err := calibration.CheckGitVersion(cmd.Context()); err != nil {
			return err
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		if latest, ok, err := calibration.LatestRun(s.Dir); err != nil {
			return err
		} else if ok && latest.Step != calibrationDone && !fresh {
			return fmt.Errorf("calibration run %s is unfinished at step %s; see duolog calibrate status, or start over with duolog calibrate survey --new", latest.ID, latest.Step)
		}
		survey, err := calibration.RunSurvey(cmd.Context(), root, cfg, surveyChecks)
		if err != nil {
			return err
		}
		run, err := calibration.NewRun(s.Dir, time.Now())
		if err != nil {
			return err
		}
		if err := calibration.SaveSurvey(s.Dir, run.ID, survey); err != nil {
			return err
		}
		run.Step = "survey"
		if err := run.Save(s.Dir); err != nil {
			return err
		}
		return writeSurvey(cmd.OutOrStdout(), run.ID, cfg, survey)
	}}
	cmd.Flags().BoolVar(&fresh, "new", false, "Start a new run even when an unfinished one exists")
	return cmd
}

func writeSurvey(out io.Writer, id string, cfg *config.Config, s calibration.Survey) error {
	list := func(items []string, sep string) string {
		if len(items) == 0 {
			return "none"
		}
		return strings.Join(items, sep)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "calibration run %s\nsurvey:\n", id)
	fmt.Fprintf(&b, "  languages: %s\n", list(s.Facts.Languages, ", "))
	fmt.Fprintf(&b, "  build: %s\n", list(s.Facts.Build, "; "))
	fmt.Fprintf(&b, "  test: %s\n", list(s.Facts.Test, "; "))
	fmt.Fprintf(&b, "  components: %s\n", list(s.Facts.Components, ", "))
	fmt.Fprintf(&b, "  history: %d tracked files, %d commits over %d days, median %d changed lines\n", s.Files, s.Commits, s.HistoryDays, s.MedianLines)
	fmt.Fprintf(&b, "  check time: %.1f s on HEAD\n", s.CheckSeconds)
	for _, note := range s.Notes {
		fmt.Fprintf(&b, "  note: %s\n", note)
	}
	b.WriteString("checks per class:\n")
	for _, class := range slices.Sorted(maps.Keys(cfg.Learning.Classes)) {
		fmt.Fprintf(&b, "  %s: %s\n", class, list(s.Checks[class], "; "))
	}
	b.WriteString("next: duolog calibrate classes\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func newCalibrateClassesCommand() *cobra.Command {
	var apply bool
	var skip []string
	cmd := &cobra.Command{Use: "classes", Short: "Propose classes and checks for the project as a config diff", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		run, ok, err := calibration.LatestRun(s.Dir)
		if err != nil {
			return err
		}
		if !ok || run.Step == calibrationDone {
			return fmt.Errorf("run duolog calibrate survey first")
		}
		survey, err := calibration.LoadSurvey(s.Dir, run.ID)
		if err != nil {
			return err
		}
		// Pull request texts change statements, not classes, so no gh call.
		cands, _, err := calibration.Collect(cmd.Context(), root, cfg, nil, time.Now())
		if err != nil {
			return err
		}
		p := calibration.ProposeClassesFrom(cfg, cands, survey)
		out := cmd.OutOrStdout()
		if apply {
			old, updated, err := calibration.ApplyClasses(root, cfg, p, skip)
			if err != nil {
				return err
			}
			run.Step = "classes"
			if err := run.Save(s.Dir); err != nil {
				return err
			}
			if !bytes.Equal(old, updated) {
				fmt.Fprintf(out, "updated .duolog/config.yaml:\n%s", learn.LineDiff(old, updated))
			}
			_, err = fmt.Fprintln(out, "next: duolog calibrate mine")
			return err
		}
		old, updated, err := previewConfig(root, func(tmp string) ([]byte, []byte, error) {
			return calibration.ApplyClasses(tmp, cfg, p, skip)
		})
		if err != nil {
			return err
		}
		return writeClasses(out, run.ID, cfg, p, skip, old, updated)
	}}
	cmd.Flags().BoolVar(&apply, "apply", false, "Write the proposed classes and checks to .duolog/config.yaml")
	cmd.Flags().StringArrayVar(&skip, "skip", nil, "Leave out the named class proposal or checks:<class>; repeatable")
	return cmd
}

func writeClasses(out io.Writer, id string, cfg *config.Config, p calibration.ClassesProposal, skip []string, old, updated []byte) error {
	var b strings.Builder
	fmt.Fprintf(&b, "calibration run %s\n", id)
	for _, q := range p.Proposals {
		fmt.Fprintf(&b, "class proposal %s:\nevidence: %s\n", q.Name, q.Evidence)
	}
	for _, class := range slices.Sorted(maps.Keys(p.Checks)) {
		was := "none"
		if current := cfg.Learning.Classes[class].Checks; len(current) > 0 {
			was = strings.Join(current, "; ")
		}
		fmt.Fprintf(&b, "%s: %s (was %s)\n", calibration.ChecksName(class), strings.Join(p.Checks[class], "; "), was)
	}
	if bytes.Equal(old, updated) {
		b.WriteString("no change to .duolog/config.yaml\n")
	} else {
		fmt.Fprintf(&b, "proposed .duolog/config.yaml:\n%s", learn.LineDiff(old, updated))
		b.WriteString("leave one out with: --skip <name>\n")
	}
	b.WriteString("apply with: duolog calibrate classes --apply")
	for _, name := range skip {
		fmt.Fprintf(&b, " --skip %s", name)
	}
	b.WriteString("\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func newCalibrateMineCommand() *cobra.Command {
	return &cobra.Command{Use: "mine", Short: "Find past tasks, validate them and sample the calibration tasks", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		run, ok, err := calibration.LatestRun(s.Dir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no calibration run; start one with duolog calibrate survey")
		}
		if run.Step != "classes" && run.Step != "mine" {
			next := calibrationNext[run.Step]
			if next == "" {
				next = "survey --new"
			}
			return fmt.Errorf("calibration run %s is at step %s; next: duolog calibrate %s", run.ID, run.Step, next)
		}
		survey, err := calibration.LoadSurvey(s.Dir, run.ID)
		if err != nil {
			return err
		}
		j := &journal.Journal{Dir: filepath.Join(s.Dir, "journal")}
		ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "calibration run %s\nvalidation: up to %.1f s per task (6 x %.1f s of checks), at most %d candidates checked per class\n",
			run.ID, 6*survey.CheckSeconds, survey.CheckSeconds, calibration.ValidationBudget(survey))

		ctx := cmd.Context()
		now := time.Now()
		candidates, notes, err := calibration.Collect(ctx, root, cfg, mineGH, now)
		if err != nil {
			return err
		}
		m, err := calibration.Mine(ctx, calibration.MineInput{
			Root: root, StateDir: s.Dir, Cfg: cfg, Survey: survey, Candidates: candidates,
			Means: calibration.JournalMeans(ds), Seed: run.Seed, Now: now,
			Validate: func(ctx context.Context, c calibration.Candidate) (string, string, error) {
				return calibration.Validate(ctx, root, c, classChecks(cfg, survey, c.Class), calibration.CheckLimit(cfg.Calibration), mineChecks)
			},
		})
		if err != nil {
			return err
		}
		runDir := run.Dir(s.Dir)
		if err := os.RemoveAll(filepath.Join(runDir, calibration.TasksDir)); err != nil {
			return err
		}
		if err := calibration.SaveManifest(runDir, m); err != nil {
			return err
		}
		for _, c := range candidates {
			if _, kept := m.Verification[c.ID]; kept {
				if err := calibration.WriteTaskFiles(ctx, runDir, root, c); err != nil {
					return err
				}
			}
		}
		run.Step = "mine"
		if err := run.Save(s.Dir); err != nil {
			return err
		}
		return writeMine(out, m, notes)
	}}
}

// classChecks are the checks configured for class, or else the ones the
// survey proposed.
func classChecks(cfg *config.Config, survey calibration.Survey, class string) []string {
	if checks := cfg.Learning.Classes[class].Checks; len(checks) > 0 {
		return checks
	}
	return survey.Checks[class]
}

func writeMine(out io.Writer, m calibration.Manifest, notes []string) error {
	var b strings.Builder
	for _, class := range slices.Sorted(maps.Keys(m.Counts)) {
		count := m.Counts[class]
		fmt.Fprintf(&b, "class %s: %d candidates, %d selected, %d reserve, %d valid", class, count.Candidates, count.Selected, count.Reserve, count.Valid)
		if count.WindowDays == 0 {
			b.WriteString(", all history")
		} else {
			fmt.Fprintf(&b, ", last %d days", count.WindowDays)
		}
		if count.Skipped != "" {
			fmt.Fprintf(&b, "; skipped: %s", count.Skipped)
		}
		b.WriteString("\n")
	}
	reasons := map[string]int{}
	for _, x := range m.Excluded {
		reasons[x.Reason]++
	}
	keys := slices.Sorted(maps.Keys(reasons))
	slices.SortStableFunc(keys, func(a, b string) int { return reasons[b] - reasons[a] })
	var parts []string
	for _, reason := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", reasons[reason], reason))
	}
	if len(parts) == 0 {
		parts = []string{"none"}
	}
	fmt.Fprintf(&b, "excluded: %s\n", strings.Join(parts, ", "))
	fmt.Fprintf(&b, "repository: %d tracked files, %d days of history\n", m.RepoFiles, m.HistoryDays)
	fmt.Fprintf(&b, "check time: %.1f s per run\n", m.CheckSeconds)
	if len(m.Round2) > 0 {
		fmt.Fprintf(&b, "round 2: %d tasks\n", len(m.Round2))
	}
	if len(m.Rewrite) > 0 {
		fmt.Fprintf(&b, "statements to rewrite in the run: %d\n", len(m.Rewrite))
	}
	if len(m.Tasks) > 0 {
		b.WriteString("estimates per cell:\n")
	}
	for _, task := range m.Tasks {
		e := m.Estimates[task.ID]
		large := ""
		if e.Large {
			large = ", large"
		}
		fmt.Fprintf(&b, "  %s %s (%s): %d files, %d lines, %d-%d tokens, %.0f min%s\n",
			task.ID, task.Class, task.Verification, e.Files, e.Lines, e.TokensLow, e.TokensHigh, e.Minutes, large)
	}
	for _, note := range notes {
		fmt.Fprintf(&b, "note: %s\n", note)
	}
	b.WriteString("next: duolog calibrate plan\n")
	_, err := io.WriteString(out, b.String())
	return err
}

func newCalibratePlanCommand() *cobra.Command {
	var approve string
	cmd := &cobra.Command{Use: "plan", Short: "Show the costed calibration plan, or approve it once", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		run, ok, err := calibration.LatestRun(s.Dir)
		if err != nil {
			return err
		}
		var runID, step string
		if ok {
			runID, step = run.ID, run.Step
		}
		if err := budget.PlanStep(runID, step, calibrationNext); err != nil {
			return err
		}
		runDir := run.Dir(s.Dir)
		out := cmd.OutOrStdout()
		if approve != "" {
			approvedHash := ""
			if run.Approved != nil {
				approvedHash = run.Approved.Hash
			}
			// A plan shown after an approval waits in plan-next.json. The plan
			// approved already is refused before a missing plan is.
			p, loadErr := calibration.LoadPlanToApprove(runDir)
			if loadErr == nil || approvedHash == approve {
				if err := budget.CheckApproval(approve, approvedHash, p); err != nil {
					return err
				}
			}
			if os.IsNotExist(loadErr) {
				return fmt.Errorf("no saved plan; run duolog calibrate plan first")
			}
			if loadErr != nil {
				return loadErr
			}
			if err := calibration.Approve(run, p, time.Now()); err != nil {
				return err
			}
			approval := budget.Approve(run.Step, run.Cells)
			if err := calibration.SavePlan(runDir, p); err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(runDir, calibration.NextPlanFile)); err != nil && !os.IsNotExist(err) {
				return err
			}
			run.Step = approval.Step
			if err := run.Save(s.Dir); err != nil {
				return err
			}
			if approval.Kept > 0 {
				fmt.Fprintf(out, "kept %d cells that ran\n", approval.Kept)
			}
			_, err = fmt.Fprintf(out, "approved plan %s: %d cells\nnext: duolog calibrate %s\n", p.Hash, len(run.Cells), approval.Next)
			return err
		}

		m, err := calibration.LoadManifest(runDir)
		if err != nil {
			return err
		}
		rates, err := calibration.ReadRates(s.Dir)
		if err != nil {
			return err
		}
		survey, err := calibration.LoadSurvey(s.Dir, run.ID)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		ctx := cmd.Context()
		// Quota and billing come from the account gate, with the person's
		// config, as the cells will run.
		accounts := newCalibrationAccounts(cfg, root)
		quotaSource := accounts.Quota()
		quotas := map[string][]calibration.Window{}
		for _, agent := range []string{"claude", "codex"} {
			ws, err := quotaSource.Get(ctx, agent, false)
			if err != nil {
				fmt.Fprintf(out, "note: quota of %s unavailable: %v\n", agent, err)
				continue
			}
			quotas[agent] = ws
		}
		budgets, err := accounts.Budgets(ctx)
		if err != nil {
			return err
		}
		now := time.Now()
		// Planned again, the cells that ran keep their cost and the run's
		// spend counts toward the cap. A paid route is costed in USD and needs
		// both paid_runs opt-ins.
		p, err := calibration.BuildPlan(calibration.PlanInput{
			Cfg: planConfig(cfg, survey), Manifest: m, Rates: rates, Quota: quotas, RunID: run.ID,
			Ages: calibration.TaskAges(ctx, root, runDir, m, now), Cells: run.Cells, Spent: run.Spend.WeeklyPct,
			Billing: budgets,
		})
		if err != nil {
			return err
		}
		// The approved plan stays in force until the person approves a new
		// one, which keeps the cells that ran.
		approvedHash := ""
		if run.Approved != nil {
			approvedHash = run.Approved.Hash
		}
		switch budget.ChoosePlanSave(approvedHash, run.Approved != nil, p.Hash) {
		case budget.SavePlan:
			err = calibration.SavePlan(runDir, p)
		case budget.SaveNextPlan:
			err = calibration.SaveNextPlan(runDir, p)
		case budget.RemoveNextPlan:
			err = os.Remove(filepath.Join(runDir, calibration.NextPlanFile))
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			return err
		}
		if err := calibration.RenderPlan(out, p); err != nil {
			return err
		}
		if run.Approved != nil {
			if run.Approved.Hash == p.Hash {
				fmt.Fprintln(out, "this plan is approved")
			} else {
				fmt.Fprintf(out, "plan %s stays approved until you approve this one\n", run.Approved.Hash)
			}
		}
		if err := writePlanTree(ctx, out, root, p); err != nil {
			return err
		}
		return writeJudgeSlot(ctx, out, cfg, root, p)
	}}
	cmd.Flags().StringVar(&approve, "approve", "", "Approve the saved plan with this hash and create its cells")
	return cmd
}

// writePlanTree prepares and names the tree folder of a plan with a Codex
// entry, with the project's Codex hooks in it, so the person can trust it
// and approve them in Codex before the run.
func writePlanTree(ctx context.Context, out io.Writer, root string, p calibration.Plan) error {
	if !p.HasAgent("codex") {
		return nil
	}
	tree, err := calibration.TreeSlot(root, 0)
	if err != nil {
		return err
	}
	if err := calibration.PrepareTree(ctx, root, tree); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "tree: %s (open Codex in this folder once, trust it and approve its hooks in /hooks; duolog never edits Codex config)\n", tree)
	return err
}

// writeJudgeSlot readies the judge slot of a plan with a Codex judge call or
// rewrite, with the project's hooks in it, and names it, so the person can
// trust the folder and approve its hooks in Codex before the run.
func writeJudgeSlot(ctx context.Context, out io.Writer, cfg *config.Config, root string, p calibration.Plan) error {
	if !p.HasJudge("codex") {
		return nil
	}
	slot, err := calibration.JudgeSlot(root)
	if err != nil {
		return err
	}
	claudeEnv, err := calibration.WorkerEnviron(cfg, "claude")
	if err != nil {
		return err
	}
	if _, err := calibration.NewJudgeSlot(ctx, root, slot, nil, claudeEnv); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "judge folder: %s (open Codex in this folder once, trust it and approve its hooks in /hooks; judge calls and rewrites run there)\n", slot)
	return err
}

// planConfig is cfg with each class's checks as the run uses them: the
// configured ones, or else the ones the survey proposed.
func planConfig(cfg *config.Config, survey calibration.Survey) *config.Config {
	c := *cfg
	c.Learning.Classes = maps.Clone(cfg.Learning.Classes)
	for name, class := range c.Learning.Classes {
		class.Checks = classChecks(cfg, survey, name)
		c.Learning.Classes[name] = class
	}
	return &c
}

func newCalibrateRunCommand() *cobra.Command {
	var lead string
	cmd := &cobra.Command{Use: "run", Short: "Run the approved calibration plan cell by cell", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return withCalibrationRunner(cmd, lead, func(r *calibration.Runner) error {
			_, err := r.Execute(cmd.Context())
			return err
		})
	}}
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}

func newCalibrateResumeCommand() *cobra.Command {
	var lead string
	cmd := &cobra.Command{Use: "resume", Short: "Continue a stopped, paused or interrupted calibration run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return withCalibrationRunner(cmd, lead, func(r *calibration.Runner) error {
			_, err := r.Resume(cmd.Context())
			return err
		})
	}}
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}

// withCalibrationRunner holds the calibration lock and calls f with a
// Runner for the latest run, whose plan must be approved and unchanged.
// A billing key that env_passthrough lists stops nothing here: every step
// launches through workerenv.Build, whose free variant drops the key, and the
// account gate admits a paid route only with paid_runs.<agent>.calibrate
// under the approved plan's budget.
func withCalibrationRunner(cmd *cobra.Command, lead string, f func(*calibration.Runner) error) error {
	root, err := projectRoot()
	if err != nil {
		return err
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		return err
	}
	s, err := store.Open(root)
	if err != nil {
		return err
	}
	unlock, err := calibration.Lock(s.Dir)
	if err != nil {
		return err
	}
	defer unlock()
	run, ok, err := calibration.LatestRun(s.Dir)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no calibration run; start one with duolog calibrate survey")
	}
	if run.Approved == nil {
		return calibration.ErrNotApproved
	}
	if run.Step != "plan" {
		next := calibrationNext[run.Step]
		if next == "" {
			next = "survey --new"
		}
		return fmt.Errorf("calibration run %s is at step %s; next: duolog calibrate %s", run.ID, run.Step, next)
	}
	runDir := run.Dir(s.Dir)
	p, err := calibration.LoadPlan(runDir)
	if err != nil {
		return err
	}
	if p.Hash != run.Approved.Hash || p.ComputeHash() != p.Hash {
		return fmt.Errorf("plan hash does not match the approval; run duolog calibrate plan again")
	}
	m, err := calibration.LoadManifest(runDir)
	if err != nil {
		return err
	}
	survey, err := calibration.LoadSurvey(s.Dir, run.ID)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	tree, err := calibration.TreeSlot(root, 0)
	if err != nil {
		return err
	}
	// The run and its cells keep to the approved caps and cell limits, or to
	// the config's where they are stricter.
	runCfg := p.Limit(planConfig(cfg, survey))
	env := cellEnv(root, s, runDir, lead, runCfg)
	env.Checks = cellChecks
	// The approved budgets, or today's where they are stricter: a lower cap,
	// a withdrawn opt-in, a smaller share, a route that turned paid.
	accounts := newCalibrationAccounts(runCfg, root)
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	current, err := accounts.Budgets(ctx)
	if err != nil {
		return err
	}
	return f(&calibration.Runner{
		StateDir: s.Dir, Root: root, Cfg: runCfg, Run: run, Plan: p, Manifest: m,
		Quota: accounts.Quota(), Now: time.Now, Billing: p.LimitBilling(current), Accounts: accounts,
		Cell: func(ctx context.Context, c calibration.Cell, t calibration.TaskRecord, start calibration.StartFunc) (calibration.CellResult, error) {
			return calibration.RunCell(ctx, env, c, t, start)
		},
		Rewrite: rewriteStatement(root, runCfg, s, runDir, lead),
		Out:     cmd.OutOrStdout(),
		Tree:    tree,
	})
}

// cellEnv is what calibration cells and judge calls delegate with. Their
// workers get the telemetry and balance roots in their launch settings, as
// every other delegation does.
func cellEnv(root string, s *store.Store, runDir, lead string, cfg *config.Config) calibration.CellEnv {
	return calibration.CellEnv{Root: root, StateDir: s.Dir, RunDir: runDir, Lead: lead, Cfg: cfg, Store: s, Delegate: runDelegate, ProtectedRoots: launchProtectedRoots()}
}

// rewriteStatement asks the first judge for a statement written from a
// task's reference change and subject. The worker runs in the judge slot,
// which then holds only those two files, and its brief names their paths
// there, so the brief under the run folder holds no solution. A slot Codex
// does not trust refuses the rewrite before start admits and charges it.
func rewriteStatement(root string, cfg *config.Config, s *store.Store, runDir, lead string) calibration.RewriteFunc {
	return func(ctx context.Context, t calibration.TaskRecord, start calibration.StartFunc) (_ string, _ int64, err error) {
		if len(cfg.Learning.Judges) == 0 {
			return "", 0, fmt.Errorf("no judge configured to rewrite statements")
		}
		reference, err := os.ReadFile(filepath.Join(runDir, calibration.TasksDir, t.ID, "reference.patch"))
		if err != nil {
			return "", 0, err
		}
		work, err := calibration.JudgeSlot(root)
		if err != nil {
			return "", 0, err
		}
		target := cfg.Learning.Judges[0].Target()
		claudeEnv, err := calibration.WorkerEnviron(cfg, "claude")
		if err != nil {
			return "", 0, err
		}
		workerEnv, err := calibration.WorkerEnviron(cfg, target.Agent)
		if err != nil {
			return "", 0, err
		}
		cleanup, err := calibration.NewJudgeSlot(ctx, root, work, map[string][]byte{"reference.patch": reference, "subject.txt": []byte(t.Subject + "\n")}, claudeEnv)
		if err != nil {
			return "", 0, err
		}
		defer func() { err = errors.Join(err, cleanup()) }()
		if err := calibration.CheckTreeTrust(target.Agent, work, workerEnv); err != nil {
			return "", 0, err
		}
		dir := filepath.Join(runDir, "rewrites", t.ID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", 0, err
		}
		taskPath := filepath.Join(dir, "task.md")
		if err := os.WriteFile(taskPath, []byte(rewriteBrief(t)), 0600); err != nil {
			return "", 0, err
		}
		if err := calibration.StartStep(start); err != nil {
			return "", 0, err
		}
		// The rewrite's budget and wait notice, which its start set.
		budget, waiting := calibration.StepOptions(ctx)
		out, err := runDelegate(ctx, delegate.Request{Config: cfg, StateDir: s.Dir, Role: "docs", TaskPath: taskPath,
			Options: delegate.Options{Lead: lead, Entry: &target, WorkRoot: work, Calibration: true, ProtectedRoots: launchProtectedRoots(),
				Budget: budget, Waiting: waiting}})
		tokens := out.Result.Usage.Input + out.Result.Usage.Output
		if err != nil {
			return "", tokens, err
		}
		if !out.Result.Done || out.RunHere != nil {
			return "", tokens, fmt.Errorf("the rewrite did not finish")
		}
		return strings.TrimSpace(out.Result.Summary), tokens, nil
	}
}

// rewriteBrief asks for a statement of what the change does, in observable
// requirements; it names the hidden tests' names, which are not a solution.
func rewriteBrief(t calibration.TaskRecord) string {
	var b strings.Builder
	b.WriteString("## Goal\n\nWrite the task statement a developer would get before making the change in `reference.patch`: what it must do, as observable requirements. ")
	b.WriteString("Name every public name or signature the tests call. Do not copy code, the commit subject in `subject.txt` or any commit id, and do not use code blocks. ")
	b.WriteString("Leave out the names and email addresses of people, and any cost, quota, usage or token figure.\n\n")
	b.WriteString("## Read\n\n- `reference.patch`\n- `subject.txt`\n\n")
	b.WriteString("## Write\n\nNone.\n\n")
	b.WriteString("## Limits\n\nRead only the listed files.\n\n")
	b.WriteString("## Check\n\nThe answer's summary is the statement and nothing else")
	if len(t.HiddenNames) > 0 {
		fmt.Fprintf(&b, ", and it names %s", strings.Join(t.HiddenNames, ", "))
	}
	b.WriteString(".\n")
	return b.String()
}

func newCalibrateStatusCommand() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Show the latest calibration run", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		// Reading must not create state, so no store.Open here.
		stateDir := filepath.Join(root, ".duolog")
		run, ok, err := calibration.LatestRun(stateDir)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if !ok {
			_, err := fmt.Fprintln(out, "no calibration run")
			return err
		}
		// Before the plan step there is no plan, and so no cap to show.
		p, err := calibration.LoadPlan(run.Dir(stateDir))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := calibration.WriteStatus(out, run, p, time.Now()); err != nil {
			return err
		}
		// A config that does not load has nothing to note; the run is shown.
		if cfg, err := configfile.Load(root); err == nil {
			for _, note := range cfg.Calibration.Ignored {
				if _, err := fmt.Fprintf(out, "note: %s\n", note); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}
func newCalibrateJudgeCommand() *cobra.Command {
	var class, lead string
	cmd := &cobra.Command{Use: "judge", Short: "Judge the results that passed the same task, blind, for the person to confirm", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		if _, ok := cfg.Learning.Classes[class]; class != "" && !ok {
			return fmt.Errorf("unknown class %q", class)
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		run, ok, err := calibration.LatestRun(s.Dir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no calibration run; start one with duolog calibrate survey")
		}
		if run.Step != "run" && run.Step != "judge" {
			next := calibrationNext[run.Step]
			if next == "" {
				next = "survey --new"
			}
			return fmt.Errorf("calibration run %s is at step %s; next: duolog calibrate %s", run.ID, run.Step, next)
		}
		runDir := run.Dir(s.Dir)
		m, err := calibration.LoadManifest(runDir)
		if err != nil {
			return err
		}
		j, err := journal.Open(s.Dir)
		if err != nil {
			return err
		}
		// Check results count without the person's confirmation.
		if err := calibration.RecordResults(j, cfg, run, m, time.Now()); err != nil {
			return err
		}
		vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
		if err != nil {
			return err
		}
		// A pair judged before keeps its verdict, so judging again spends nothing.
		judged := map[calibration.Pair]journal.Verdict{}
		for _, v := range vs {
			if v.Kind == "verdict" && v.Source == "calibration" && v.Run == run.ID {
				judged[calibration.Pair{Task: v.Task, Class: v.Class, A: v.Original, B: v.Profile}] = v
			}
		}
		env := calibration.JudgeEnv{
			CellEnv: cellEnv(root, s, runDir, lead, cfg),
			Journal: j,
		}
		var pairs []calibration.Pair
		for _, p := range calibration.Pairs(run) {
			if class == "" || p.Class == class {
				pairs = append(pairs, p)
			}
		}
		// Judge calls keep to the approved caps, or to the config's where they
		// are stricter, as the cells do, and to the approved judges and rubrics.
		judgeCfg := cfg
		var approvedPlan *calibration.Plan
		if run.Approved != nil {
			p, err := calibration.LoadPlan(runDir)
			if err != nil {
				return err
			}
			if p.Hash != run.Approved.Hash || p.ComputeHash() != p.Hash {
				return fmt.Errorf("plan hash does not match the approval; run duolog calibrate plan again")
			}
			if err := p.CheckJudges(cfg); err != nil {
				return err
			}
			judgeCfg = p.Limit(cfg)
			approvedPlan = &p
		}
		out := cmd.OutOrStdout()
		// Each judge call is admitted and charged as a cell is, under the
		// approved budgets or today's where they are stricter.
		accounts := newCalibrationAccounts(judgeCfg, root)
		r := &calibration.Runner{StateDir: s.Dir, Root: root, Cfg: judgeCfg, Run: run, Manifest: m,
			Quota: accounts.Quota(), Now: time.Now, Out: out, Accounts: accounts}
		if approvedPlan != nil {
			current, err := accounts.Budgets(cmd.Context())
			if err != nil {
				return err
			}
			r.Billing = approvedPlan.LimitBilling(current)
		}
		o, vs, err := r.Judge(cmd.Context(), env, pairs, judged)
		if err != nil {
			return err
		}
		byClass := map[string][]journal.Verdict{}
		for _, v := range vs {
			byClass[v.Class] = append(byClass[v.Class], v)
		}
		next := "next: confirm or correct every verdict, then duolog calibrate propose\n"
		switch {
		case o.Done:
		case o.Stopped == "approved cap":
			// The cap does not reset, so judging again would only stop again.
			fmt.Fprintf(out, "%d pairs left unjudged at the approved cap\n", len(pairs)-len(vs))
		default:
			// A pause or another stop ends judging for now: the step stays at
			// run, so status and propose send the person back to judge.
			run.Step = "run"
			if err := run.Save(s.Dir); err != nil {
				return err
			}
			next = "next: duolog calibrate judge\n"
		}
		var rewritten []calibration.TaskRecord
		for _, id := range m.Rewrite {
			t, err := calibration.ReadTask(runDir, id)
			if err != nil {
				return err
			}
			if class == "" || t.Class == class {
				rewritten = append(rewritten, t)
			}
		}
		if o.Done || o.Stopped == "approved cap" {
			run.Step = "judge"
			if err := run.Save(s.Dir); err != nil {
				return err
			}
		}
		// A cell whose candidate changed the check setup gives no result; it
		// is shown for the person to decide on its task.
		var setup []calibration.Cell
		for _, c := range run.Cells {
			if c.State == calibration.CellSetupChanged && (class == "" || c.Class == class) {
				setup = append(setup, c)
			}
		}
		classes := slices.Sorted(maps.Keys(byClass))
		if class != "" {
			classes = []string{class}
		}
		return writeJudge(out, run.ID, runDir, rewritten, setup, classes, byClass, next)
	}}
	cmd.Flags().StringVar(&class, "class", "", "Judge and show only this class")
	cmd.Flags().StringVar(&lead, "lead", "", "Registered lead session ID (defaults to DUOLOG_LEAD)")
	return cmd
}

func newCalibrateDropCommand() *cobra.Command {
	// One task is checked in RunE, so a worker with no task still hears
	// that it cannot run duolog calibrate.
	return &cobra.Command{Use: "drop <task>", Short: "Drop a task of the latest run so none of its results count", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("name one task to drop")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		s, err := store.Open(root)
		if err != nil {
			return err
		}
		unlock, err := calibration.Lock(s.Dir)
		if err != nil {
			return err
		}
		defer unlock()
		run, ok, err := calibration.LatestRun(s.Dir)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no calibration run; start one with duolog calibrate survey")
		}
		j, err := journal.Open(s.Dir)
		if err != nil {
			return err
		}
		if err := calibration.Drop(j, run, args[0], time.Now()); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "dropped task %s from calibration run %s\n", args[0], run.ID)
		return err
	}}
}

func newCalibrateProposeCommand() *cobra.Command {
	var applyProject bool
	cmd := &cobra.Command{Use: "propose", Short: "Propose the learned order per class and the shared project part", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		root, err := projectRoot()
		if err != nil {
			return err
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		// Without a run there is nothing to save, and reading must not
		// create state.
		stateDir := filepath.Join(root, ".duolog")
		run, hasRun, err := calibration.LatestRun(stateDir)
		if err != nil {
			return err
		}
		if hasRun || applyProject {
			if _, err := store.Open(root); err != nil {
				return err
			}
			unlock, err := calibration.Lock(stateDir)
			if err != nil {
				return err
			}
			defer unlock()
			if run, hasRun, err = calibration.LatestRun(stateDir); err != nil {
				return err
			}
		}
		if applyProject && !hasRun {
			return fmt.Errorf("no calibration run; start one with duolog calibrate survey")
		}
		if hasRun && run.Step != "judge" && run.Step != calibrationDone {
			next := calibrationNext[run.Step]
			if next == "" {
				next = "survey --new"
			}
			return fmt.Errorf("calibration run %s is at step %s; next: duolog calibrate %s", run.ID, run.Step, next)
		}

		j := &journal.Journal{Dir: filepath.Join(stateDir, "journal")}
		ds, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
		if err != nil {
			return err
		}
		ts, err := journal.ReadAll[journal.Task](j, "tasks.ndjson")
		if err != nil {
			return err
		}
		vs, err := journal.ReadAll[journal.Verdict](j, "verdicts.ndjson")
		if err != nil {
			return err
		}
		cs, err := journal.ReadAll[journal.CalibrationResult](j, journal.CalibrationFile)
		if err != nil {
			return err
		}
		shared, hasShared, err := calibration.ReadProject(stateDir)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if run != nil {
			fmt.Fprintf(out, "calibration run %s\n", run.ID)
		}
		// ProposeGated uses the seed only without personal results.
		var seed learn.Stats
		if hasShared {
			seed = calibration.SeedStats(cfg, shared)
			if !slices.ContainsFunc(cs, func(c journal.CalibrationResult) bool { return c.Kind == "result" }) {
				fmt.Fprintln(out, "seeded from the shared project part")
			}
		}
		if err := sayLearningOff(out, cfg); err != nil {
			return err
		}
		p, _, kept := learn.ProposeGated(cfg, ds, ts, vs, cs, seed)
		orderChanged := learn.Changed(cfg, p)
		if !orderChanged {
			fmt.Fprintln(out, "no change to the learned order")
		} else {
			old, err := os.ReadFile(filepath.Join(stateDir, config.LearnedRoutingFile))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			data, err := learn.Render(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "proposed .duolog/routing.learned.yaml:\n%s", learn.LineDiff(old, data))
		}
		for _, key := range slices.Sorted(maps.Keys(kept)) {
			fmt.Fprintf(out, "class %s: %s, keeps the configured order\n", key, kept[key])
		}

		projectChanged := false
		if hasRun {
			survey, err := calibration.LoadSurvey(stateDir, run.ID)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			m, err := calibration.LoadManifest(run.Dir(stateDir))
			if err != nil {
				return err
			}
			m.Tasks = calibration.RanTasks(m, run.ID, cs)
			part := calibration.BuildProjectPart(survey, m, calibration.Summarize(cfg, cs, vs, time.Now(), version))
			people, err := calibration.HistoryNames(cmd.Context(), root)
			if err != nil {
				return err
			}
			const name = ".duolog/calibration/" + calibration.ProjectFile
			if applyProject {
				old, updated, err := calibration.WriteProject(stateDir, part, people...)
				if err != nil {
					return err
				}
				if bytes.Equal(old, updated) {
					fmt.Fprintf(out, "no change to %s\n", name)
				} else {
					fmt.Fprintf(out, "updated %s:\n%s", name, learn.LineDiff(old, updated))
				}
			} else {
				if err := calibration.ValidateProject(part, people...); err != nil {
					return err
				}
				updated, err := calibration.RenderProject(part)
				if err != nil {
					return err
				}
				old, err := os.ReadFile(filepath.Join(calibration.Dir(stateDir), calibration.ProjectFile))
				if err != nil && !os.IsNotExist(err) {
					return err
				}
				if projectChanged = !bytes.Equal(old, updated); projectChanged {
					fmt.Fprintf(out, "proposed %s:\n%s", name, learn.LineDiff(old, updated))
				} else {
					fmt.Fprintf(out, "no change to %s\n", name)
				}
			}
		}
		if orderChanged {
			fmt.Fprintln(out, "apply the order with: duolog learn --apply")
		}
		if projectChanged {
			fmt.Fprintln(out, "apply the project part with: duolog calibrate propose --apply-project")
		}
		// The proposal is the last step: a new survey may start after it.
		if hasRun && run.Step != calibrationDone {
			run.Step = calibrationDone
			return run.Save(stateDir)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&applyProject, "apply-project", false, "Write the shared project part to .duolog/calibration/project.yaml")
	return cmd
}

// writeJudge shows the rewritten statements first, so the person can drop a
// task before confirming its verdicts, then one batch of verdicts per class.
func writeJudge(out io.Writer, id, runDir string, rewritten []calibration.TaskRecord, setup []calibration.Cell, classes []string, byClass map[string][]journal.Verdict, next string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "calibration run %s\n", id)
	if len(rewritten) == 0 {
		b.WriteString("rewritten statements: none\n")
	} else {
		b.WriteString("rewritten statements:\n")
	}
	for _, t := range rewritten {
		fmt.Fprintf(&b, "  %s, %s:\n", t.ID, t.Class)
		for _, line := range strings.Split(strings.TrimSpace(t.Statement), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		fmt.Fprintf(&b, "  drop with: duolog calibrate drop %s\n", t.ID)
	}
	if len(setup) > 0 {
		b.WriteString("check setup changed, no pass or failure; read the patch, then keep it out or drop the task:\n")
	}
	for _, c := range setup {
		checks := "checks failed"
		if c.Passed {
			checks = "checks passed"
		}
		if c.Reason != "" {
			checks += ", " + c.Reason
		}
		fmt.Fprintf(&b, "  cell %s: task %s, %s, changed %s; %s\n", c.ID, c.Task, c.Entry, strings.Join(c.SetupChanged, ", "), checks)
		fmt.Fprintf(&b, "  patch: %s\n", filepath.Join(runDir, calibration.CellsDir, c.ID, "result.patch"))
		fmt.Fprintf(&b, "  drop with: duolog calibrate drop %s\n", c.Task)
	}
	for _, class := range classes {
		vs := byClass[class]
		fmt.Fprintf(&b, "class %s: %d verdicts\n", class, len(vs))
		for _, v := range vs {
			fmt.Fprintf(&b, "  verdict %s: task %s, A %s, B %s, judges %s", v.ID, v.Task, v.Original, v.Profile, v.Judge)
			if v.Disputed {
				b.WriteString(", disputed")
			}
			b.WriteString("\n")
			for _, s := range v.Scores {
				a, p := strconv.Itoa(s.Original), strconv.Itoa(s.Profile)
				if s.OriginalCount != nil && s.ProfileCount != nil {
					a += fmt.Sprintf(" (count %d)", *s.OriginalCount)
					p += fmt.Sprintf(" (count %d)", *s.ProfileCount)
				}
				fmt.Fprintf(&b, "    %s: A %s, B %s\n", s.Criterion, a, p)
			}
			fmt.Fprintf(&b, "    confirm: duolog verdict %s confirm\n", v.ID)
			if len(v.Scores) > 0 {
				s := v.Scores[0]
				fmt.Fprintf(&b, "    correct: duolog verdict %s correct \"%s=%d/%d\"\n", v.ID, s.Criterion, s.Original, s.Profile)
			}
		}
	}
	b.WriteString(next)
	_, err := io.WriteString(out, b.String())
	return err
}
