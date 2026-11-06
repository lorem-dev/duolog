package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/adapter/models"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/app/skilltest"
	"github.com/lorem-dev/duolog/internal/capability"
	"github.com/lorem-dev/duolog/internal/core/config"
	coreroute "github.com/lorem-dev/duolog/internal/core/route"
	"github.com/lorem-dev/duolog/internal/core/usage"
	"github.com/lorem-dev/duolog/internal/runner"
	"github.com/lorem-dev/duolog/internal/session"
	"github.com/lorem-dev/duolog/internal/store"
	"github.com/lorem-dev/duolog/internal/workerenv"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func newSkillCommand(d *cmdDeps) *cobra.Command {
	parent := &cobra.Command{Use: "skill", Short: "Test skill instructions across models"}
	var planPath, modelList, remember string
	var yes, allowManual bool
	cmd := &cobra.Command{Use: "test <SKILL.md>", Short: "Run a situation plan without tools", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if isWorker() {
			return fmt.Errorf("workers cannot run skill tests or change their preference")
		}
		root, err := projectRoot()
		if err != nil {
			return err
		}
		stateRoot := root
		root = workRoot(root)
		if cmd.Flags().Changed("remember") {
			if len(args) != 0 || planPath != "" || modelList != "" || yes || allowManual {
				return fmt.Errorf("--remember is used alone")
			}
			return rememberSkillTests(root, remember)
		}
		if len(args) != 1 {
			return fmt.Errorf("name a SKILL.md or use --remember ask|always|never")
		}
		skill, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		name := filepath.Base(filepath.Dir(args[0]))
		if name == "." {
			cwd, _ := os.Getwd()
			name = filepath.Base(cwd)
		}
		if name == ".." || name == string(filepath.Separator) {
			return fmt.Errorf("skill needs a named directory")
		}
		if planPath == "" {
			planPath = filepath.Join(root, ".duolog", "skill-tests", name+".plan.yaml")
		}
		data, err := os.ReadFile(planPath)
		if os.IsNotExist(err) {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Write a situation plan to %s, then run again.\n", planPath)
			return err
		}
		if err != nil {
			return err
		}
		plan, err := skilltest.ReadPlan(strings.NewReader(string(data)))
		if err != nil {
			return fmt.Errorf("%s: %w", planPath, err)
		}
		cfg, err := configfile.Load(root)
		if err != nil {
			return err
		}
		if cfg.SkillTests == "never" {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Skipped: skill_tests is never. Use --remember ask to change it.")
			return err
		}
		reg, err := session.Open(filepath.Join(stateRoot, ".duolog"))
		if err != nil {
			return err
		}
		unavailable, err := reg.Unavailable()
		if err != nil {
			return err
		}
		for _, agent := range []string{"claude", "codex"} {
			if _, err := exec.LookPath(agent); err != nil {
				unavailable[agent] = true
			}
		}
		targets := skilltest.DefaultTargets(cfg, unavailable)
		if modelList != "" {
			targets, err = skilltest.ParseTargets(cfg, modelList, allowManual)
			if err != nil {
				return err
			}
		}
		if len(targets) == 0 {
			return fmt.Errorf("no available agents with configured models")
		}
		estimated := 0.0
		runs := 0
		for _, target := range targets {
			if unavailable[target.Agent] {
				return fmt.Errorf("agent %s is unavailable", target.Agent)
			}
			selected := plan.ForAgent(target.Agent)
			if len(selected.Situations) == 0 {
				continue
			}
			runs++
			prompt := selected.Prompt(string(skill))
			cost, ok := usage.Cost(cfg, target.Agent, target.Model, usage.Tokens{Input: int64((len(prompt) + 3) / 4), Output: int64(len(selected.Situations) * 200)})
			if !ok {
				return fmt.Errorf("no configured price for %s", target.Model)
			}
			estimated += cost
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "%d runs, %d situations; estimated cost USD %.6f (prompt bytes / 4, 200 output tokens per situation).\n", runs, len(plan.Situations), estimated)
		for _, target := range targets {
			fmt.Fprintf(out, "  %s/%s/%s\n", target.Agent, target.Model, target.Effort)
		}
		if runs > 0 && !yes && cfg.SkillTests != "always" {
			fmt.Fprint(out, "Run these tests? [y/N] ")
			line, readErr := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}
			answer := strings.ToLower(strings.TrimSpace(line))
			if answer != "y" && answer != "yes" {
				fmt.Fprintln(out, "Skipped.")
				return nil
			}
		}
		return runSkillTests(d, cmd, root, stateRoot, name, cfg, plan, string(skill), targets)
	}}
	cmd.Flags().StringVar(&planPath, "plan", "", "YAML situation plan")
	cmd.Flags().StringVar(&modelList, "models", "", "Comma-separated agent/model/effort runs")
	cmd.Flags().StringVar(&remember, "remember", "", "Save ask, always or never for this repository")
	cmd.Flags().BoolVar(&yes, "yes", false, "Approve the printed test cost without prompting")
	cmd.Flags().BoolVar(&allowManual, "allow-manual-model", false, "Allow --models to name models reserved for manual selection")
	parent.AddCommand(cmd)
	return parent
}

func rememberSkillTests(root, value string) error {
	if !slices.Contains([]string{"ask", "always", "never"}, value) {
		return fmt.Errorf("--remember must be ask, always or never")
	}
	if _, err := store.Open(root); err != nil {
		return err
	}
	path := filepath.Join(root, ".duolog", "config.local.yaml")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return err
		}
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	mapping := doc.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must contain a mapping", path)
	}
	found := false
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "skill_tests" {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Tag = "!!str"
			mapping.Content[i+1].Kind = yaml.ScalarNode
			found = true
		}
	}
	if !found {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "skill_tests"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	data, err = yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func runSkillTests(d *cmdDeps, cmd *cobra.Command, root, stateRoot, name string, cfg *config.Config, plan skilltest.Plan, skill string, targets []config.Target) error {
	dir := filepath.Join(root, ".duolog", "skill-tests", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	type outcome struct {
		label  string
		graded map[string]bool
		cost   float64
	}
	var results []outcome
	total := 0.0
	var runErrors []error
	for _, target := range targets {
		if err := cmd.Context().Err(); err != nil {
			runErrors = append(runErrors, err)
			break
		}
		label := target.Agent + "/" + target.Model + "/" + target.Effort
		rawPath := filepath.Join(dir, strings.ReplaceAll(label, "/", "-")+".json")
		logPath := strings.TrimSuffix(rawPath, ".json") + ".log"
		// Clear earlier answers and logs even if model resolution or process startup fails.
		if err := os.WriteFile(rawPath, []byte("{}\n"), 0600); err != nil {
			return err
		}
		if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		work, err := os.MkdirTemp("", "duolog-skill-test-")
		if err != nil {
			return err
		}
		selected := plan.ForAgent(target.Agent)
		var result runner.Result
		route := coreroute.Route{Target: target}
		var runErr error
		if len(selected.Situations) > 0 {
			result, route, runErr = askSkill(d, cmd, cfg, root, work, selected.Prompt(skill), target)
		}
		if runErr != nil {
			runErr = keepRunLog(runErr, filepath.Join(work, "stdout.log"), logPath)
		}
		graded := selected.Grade(result.RawAnswer)
		if runErr != nil {
			for id := range graded {
				graded[id] = false
			}
			runErrors = append(runErrors, fmt.Errorf("%s: %w", label, runErr))
		}
		raw := struct {
			Raw    string          `json:"raw"`
			Graded map[string]bool `json:"graded"`
			Error  string          `json:"error,omitempty"`
		}{Raw: result.RawAnswer, Graded: graded}
		if runErr != nil {
			raw.Error = runErr.Error()
		}
		data, err := json.MarshalIndent(raw, "", "  ")
		if err == nil {
			err = os.WriteFile(rawPath, append(data, '\n'), 0600)
		}
		// Logs and worker answers live only for this invocation.
		cleanupErr := os.RemoveAll(work)
		if err != nil || cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		if len(selected.Situations) > 0 {
			if err := delegate.RecordUsage(filepath.Join(stateRoot, ".duolog"), "skill_test", route, result); err != nil {
				return err
			}
		}
		cost, _ := usage.Cost(cfg, route.Agent, route.Model, result.Usage)
		total += cost
		results = append(results, outcome{label, graded, cost})
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprint(w, "situation")
	for _, r := range results {
		fmt.Fprintf(w, "\t%s", r.label)
	}
	fmt.Fprintln(w)
	scores := make([]int, len(results))
	for _, s := range plan.Situations {
		fmt.Fprint(w, s.ID)
		for i, r := range results {
			mark := "--"
			if ok, applies := r.graded[s.ID]; !applies {
				mark = ".."
			} else if ok {
				mark = "ok"
				scores[i]++
			}
			fmt.Fprint(w, "\t"+mark)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprint(w, "score")
	for i, score := range scores {
		fmt.Fprintf(w, "\t%d/%d", score, len(results[i].graded))
	}
	fmt.Fprintln(w)
	fmt.Fprint(w, "USD")
	for _, r := range results {
		fmt.Fprintf(w, "\t%.6f", r.cost)
	}
	fmt.Fprintln(w)
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Total cost USD (from recorded usage): %.6f\nRaw answers: %s\n", total, dir)
	return errors.Join(runErrors...)
}

// keepRunLog copies the run's combined stdout and stderr out of the temporary
// folder and points the error at the copy. Without a log to copy, the error
// stays as it is.
func keepRunLog(runErr error, tempLog, logPath string) error {
	data, err := os.ReadFile(tempLog)
	if err != nil {
		return runErr
	}
	if err := os.WriteFile(logPath, data, 0600); err != nil {
		return errors.Join(runErr, err)
	}
	return errors.New(strings.ReplaceAll(runErr.Error(), tempLog, logPath))
}

func askSkill(d *cmdDeps, cmd *cobra.Command, cfg *config.Config, root, work, prompt string, target config.Target) (result runner.Result, route coreroute.Route, err error) {
	route = coreroute.Route{Target: target}
	model, err := models.Resolve(cmd.Context(), cfg, filepath.Join(root, ".duolog", "cache"), target)
	if err != nil {
		return runner.Result{}, route, err
	}
	route.Model = model
	// The free worker process: no API key, fast mode pinned off, minimal
	// context and no state folder, as a skill test needs. The account gate
	// admits it like any worker, in the free variant only: a skill test never
	// spends money, so a paid route is refused before any model call.
	ctx := cmd.Context()
	g, err := d.Delegate.PaidGate(cfg)
	if err != nil {
		return runner.Result{}, route, err
	}
	home, _ := os.UserHomeDir()
	environ := os.Environ()
	in := workerenv.ProcessInput{Agent: target.Agent, Shape: workerenv.Worker, Variant: workerenv.Free,
		Config: cfg, Environ: environ, Role: "skill_test", Minimal: true,
		Locations: workerenv.Locations{Home: home, Root: work, GOOS: runtime.GOOS, Environ: environ},
		// Codex's exec with its read-only sandbox is admitted with the rest.
		Launch: runner.Launch(runner.Spec{Route: route, SkillTest: true})}
	permit, err := g.Acquire(ctx, paidruns.Request{Input: in, Family: coreroute.Normalize(target.Agent, model),
		Role: "skill_test", Owner: paidruns.ProcessOwner(os.Getpid()), Wait: paidruns.SessionWait(cfg)})
	var refusal *paidruns.Refusal
	if errors.As(err, &refusal) {
		return runner.Result{}, route, fmt.Errorf("refused, no model call made; a skill test runs only free: %s", delegate.RefusalLine(refusal))
	}
	if err != nil {
		return runner.Result{}, route, err
	}
	started := false
	defer func() {
		// One settlement of what the run measured, also when it failed or
		// was cancelled; a run that never started gives its hold back.
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), skillSettleTimeout)
		defer cancel()
		s := paidruns.Settlement{Input: result.Usage.Input, Cached: result.Usage.Cached, Output: result.Usage.Output,
			ReportedUSD: result.ReportedUSD, Evidence: result.Billing, Started: started, CompleteUsage: started && result.UsageComplete}
		if priced, ok := usage.Cost(cfg, route.Agent, route.Model, result.Usage); ok && priced >= 0 {
			s.PricedUSD = priced
		}
		err = errors.Join(err, g.Finish(settleCtx, permit, s))
	}()
	if err = g.Store.Launch(ctx, permit.Reservation.ID); err != nil {
		return runner.Result{}, route, err
	}
	// A fresh cwd removes project instructions. Explicit overrides disable every
	// MCP server inherited from the user's Codex configuration.
	bridge := &runner.MCP{}
	if target.Agent == "codex" {
		bridge.CodexArgs = capability.CodexOffEnv(work, permit.Process.Env)
	}
	spec := runner.Spec{Route: route, SkillTest: true, TaskDir: work, Cwd: work, Stdin: []byte(prompt), Process: &permit.Process, MCP: bridge, Timeout: 10 * time.Minute}
	spec.Started = func(pid int) error {
		started = true
		attachCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), skillSettleTimeout)
		defer cancel()
		return g.Store.Attach(attachCtx, permit.Reservation.ID, paidruns.ProcessOwner(pid))
	}
	result, err = runner.Run(ctx, spec)
	return result, route, err
}

// skillSettleTimeout bounds recording and settling a skill test run, also
// after the command was cancelled.
const skillSettleTimeout = 10 * time.Second
