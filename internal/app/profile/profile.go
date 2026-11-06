// Package profile replays recorded delegations in a disposable Git worktree.
package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/core/config"
	"github.com/lorem-dev/duolog/internal/core/record"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/store"
)

// WorktreeDir is relative to the shared state directory, .duolog.
const WorktreeDir = "profile/worktree"

type Options struct {
	Entry                   config.Target
	Chain, AllowManualModel bool
	Lead                    string
	// ProtectedRoots and Observe pass to each replayed delegation.
	ProtectedRoots []string
	Observe        record.Observer
}

type Side struct {
	Entry   string
	Patch   []byte
	Summary string
	USD     float64
	Checks  []journal.Check
}

type Replay struct {
	Delegations               []string
	Task, Class, Ladder, Role string
	Original, Profile         Side
}

// Run reconstructs the recorded starting tree without changing the process
// directory or the main checkout. The lock also protects profile numbering.
func Run(ctx context.Context, d Deps, cfg *config.Config, s *store.Store, delegation string, opts Options) (replay Replay, err error) {
	lock := flock.New(filepath.Join(s.Dir, "profile.lock"))
	defer lock.Close()
	locked, err := lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil {
		return replay, err
	}
	if !locked {
		return replay, fmt.Errorf("profile lock unavailable")
	}
	j := &journal.Journal{Dir: filepath.Join(s.Dir, "journal")}
	records, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
	if err != nil {
		return replay, err
	}
	selected, err := selectRecords(records, delegation, opts.Chain)
	if err != nil {
		return replay, err
	}
	first := selected[0]
	beforePatch, err := os.ReadFile(filepath.Join(j.TaskDir(first.Task, first.ID), "before.patch"))
	if first.Base == "" || os.IsNotExist(err) {
		return replay, fmt.Errorf("delegation %s has no base commit; it was not in a Git repository", delegation)
	}
	if err != nil {
		return replay, err
	}
	wt, err := filepath.Abs(filepath.Join(s.Dir, WorktreeDir))
	if err != nil {
		return replay, err
	}
	root, err := filepath.Abs(filepath.Dir(s.Dir))
	if err != nil {
		return replay, err
	}
	inputs, err := prepareReplay(ctx, root, j, selected)
	if err != nil {
		return replay, err
	}
	last := selected[len(selected)-1]
	var originalTreePatch []byte
	if last.AfterTree != "" {
		originalTreePatch, err = journal.Diff(root, first.Base, last.AfterTree)
		if err != nil {
			return replay, err
		}
	}
	// Install cleanup before add, which can fail after creating the directory.
	defer func() { err = errors.Join(err, removeWorktree(root, wt)) }()
	if err = recreateWorktree(ctx, root, wt, first.Base); err != nil {
		return replay, err
	}
	identity, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		return replay, err
	}
	if err = apply(wt, beforePatch); err != nil {
		return replay, err
	}
	before, err := journal.Capture(wt)
	if err != nil {
		return replay, err
	}
	if first.BeforeTree != "" && before != first.BeforeTree {
		return replay, fmt.Errorf("delegation %s starting patch differs from its recorded tree", first.ID)
	}
	replay.Task, replay.Class, replay.Ladder, replay.Role = first.Task, first.Class, first.Ladder, first.Role
	replay.Original.Entry = recordEntry(first)
	replay.Profile.Entry = entryKey(opts.Entry)
	var summaries, originalSummaries []string
	// Keep the existing worker answer shape for the combined profile answer.
	answer := struct {
		Done      bool     `json:"done"`
		Summary   string   `json:"summary"`
		Files     []string `json:"files"`
		Needs     []string `json:"needs"`
		Decisions []string `json:"decisions,omitempty"`
	}{Done: true, Files: []string{}, Needs: []string{}}
	for i, rec := range selected {
		if err = ctx.Err(); err != nil {
			return replay, err
		}
		replay.Delegations = append(replay.Delegations, rec.ID)
		if err = verifyWorktree(wt, identity); err != nil {
			return replay, err
		}
		if err = apply(wt, inputs[i].leadDelta); err != nil {
			return replay, fmt.Errorf("chain lead changes before %s conflict with the replay: %w", rec.ID, err)
		}
		taskPath := filepath.Join(wt, ".duolog", "task.md")
		if err = os.MkdirAll(filepath.Dir(taskPath), 0700); err != nil {
			return replay, err
		}
		if err = os.WriteFile(taskPath, inputs[i].task, 0600); err != nil {
			return replay, err
		}
		entry := opts.Entry
		if i > 0 {
			entry = config.Target{Agent: rec.Agent, Model: rec.Model, Effort: rec.Effort}
		}
		out, e := d.Delegate(ctx, delegate.Request{Config: cfg, StateDir: s.Dir, Role: rec.Role, TaskPath: taskPath,
			Options: delegate.Options{Entry: &entry, ReplayOf: rec.ID, Lead: opts.Lead, AllowManualModel: opts.AllowManualModel, WorkRoot: wt, ProtectedRoots: opts.ProtectedRoots, Observe: opts.Observe}})
		if e != nil {
			if strings.Contains(e.Error(), "Codex hooks untrusted") {
				e = fmt.Errorf("%w; profiling worktree: %s; open /hooks there once", e, wt)
			}
			return replay, e
		}
		if err = ctx.Err(); err != nil {
			return replay, err
		}
		if out.Result.Interrupted {
			return replay, context.Canceled
		}
		if err = verifyWorktree(wt, identity); err != nil {
			return replay, err
		}
		if out.RunHere != nil || len(out.Needs) > 0 || len(out.Result.Needs) > 0 {
			return replay, fmt.Errorf("replay %s did not run a worker: %s", rec.ID, out.Line)
		}
		summaries = append(summaries, out.Result.Summary)
		answer.Done = answer.Done && out.Result.Done
		answer.Files = append(answer.Files, out.Result.Files...)
		answer.Needs = append(answer.Needs, out.Result.Needs...)
		answer.Decisions = append(answer.Decisions, out.Result.Decisions...)
	}
	after, err := journal.Capture(wt)
	if err != nil {
		return replay, err
	}
	if replay.Profile.Patch, err = journal.Diff(wt, before, after); err != nil {
		return replay, err
	}
	replay.Profile.Summary = strings.Join(summaries, "\n")
	newRecords, err := journal.ReadAll[journal.Delegation](j, "delegations.ndjson")
	if err != nil {
		return replay, err
	}
	seen := make(map[string]bool, len(records))
	for _, rec := range records {
		seen[rec.ID] = true
	}
	for _, rec := range newRecords {
		if !seen[rec.ID] && slices.Contains(replay.Delegations, rec.ProfileOf) {
			replay.Profile.USD += rec.USD
			if rec.ProfileOf == first.ID && rec.Entry != "" {
				replay.Profile.Entry = recordEntry(rec)
			}
		}
	}
	commands := cfg.Learning.Classes[first.Class].Checks
	replay.Profile.Checks = RunChecks(ctx, d, wt, commands)
	if err = ctx.Err(); err != nil {
		return replay, err
	}
	// Always remove from the known state root. No destructive Git command runs
	// inside a directory whose .git file a worker or check may have damaged.
	if err = recreateWorktree(ctx, root, wt, first.Base); err != nil {
		return replay, err
	}
	if last.AfterTree != "" {
		if err = apply(wt, originalTreePatch); err != nil {
			return replay, err
		}
	} else {
		// Single-step legacy records may have patches without tree metadata.
		input := inputs[len(inputs)-1]
		if err = apply(wt, input.before); err != nil {
			return replay, err
		}
		if err = apply(wt, input.result); err != nil {
			return replay, err
		}
	}
	for i, rec := range selected {
		if inputs[i].summary != "" {
			originalSummaries = append(originalSummaries, inputs[i].summary)
		}
		replay.Original.USD += rec.USD
	}
	after, err = journal.Capture(wt)
	if err != nil {
		return replay, err
	}
	if replay.Original.Patch, err = journal.Diff(wt, before, after); err != nil {
		return replay, err
	}
	replay.Original.Summary = strings.Join(originalSummaries, "\n")
	replay.Original.Checks = RunChecks(ctx, d, wt, commands)
	if err = ctx.Err(); err != nil {
		return replay, err
	}
	answer.Summary = replay.Profile.Summary
	data, err := json.Marshal(answer)
	if err != nil {
		return replay, err
	}
	dir, err := nextDir(j.TaskDir(first.Task, first.ID))
	if err != nil {
		return replay, err
	}
	if err = os.WriteFile(filepath.Join(dir, "side.patch"), replay.Profile.Patch, 0600); err != nil {
		return replay, err
	}
	err = os.WriteFile(filepath.Join(dir, "answer.json"), append(data, '\n'), 0600)
	return replay, err
}

func selectRecords(records []journal.Delegation, id string, chain bool) ([]journal.Delegation, error) {
	i := slices.IndexFunc(records, func(r journal.Delegation) bool {
		return r.ID == id && r.ProfileOf == "" && r.QualityRun()
	})
	if i < 0 {
		return nil, fmt.Errorf("delegation %s is not a live delegation in the journal", id)
	}
	first := records[i]
	selected := []journal.Delegation{first}
	if !chain {
		return selected, nil
	}
	var following []journal.Delegation
	for _, r := range records {
		if r.Task == first.Task && r.Step > first.Step && r.ProfileOf == "" && r.QualityRun() {
			following = append(following, r)
		}
	}
	slices.SortStableFunc(following, func(a, b journal.Delegation) int {
		if a.Step < b.Step {
			return -1
		}
		if a.Step > b.Step {
			return 1
		}
		return 0
	})
	for _, r := range following {
		if r.Class != first.Class {
			break
		}
		selected = append(selected, r)
	}
	return selected, nil
}

func entryKey(t config.Target) string {
	return (config.LadderEntry{Agent: t.Agent, Model: t.Model, Effort: t.Effort}).Key()
}

func recordEntry(rec journal.Delegation) string {
	if rec.EffortCapped {
		if entry, err := config.ParseEntryKey(rec.Entry); err == nil {
			entry.Effort = rec.Effort
			return entry.Key()
		}
	}
	return rec.Entry
}

func apply(dir string, patch []byte) error {
	if len(bytes.TrimSpace(patch)) == 0 {
		return nil
	}
	return journal.Apply(dir, patch)
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, err := journal.GitCommand(ctx, dir, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Cleanup uses its own context so cancellation cannot leave a registered worktree.
func removeWorktree(root, wt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, removeErr := git(ctx, root, "worktree", "remove", "--force", wt)
	if removeErr != nil {
		// A crash may leave only the directory or only Git's registration.
		if err := os.RemoveAll(wt); err != nil {
			return errors.Join(removeErr, err)
		}
	}
	_, err := git(ctx, root, "worktree", "prune")
	return err
}

func nextDir(parent string) (string, error) {
	for n := 1; ; n++ {
		dir := filepath.Join(parent, fmt.Sprintf("profile-%d", n))
		if err := os.Mkdir(dir, 0700); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		return dir, nil
	}
}

// RunChecks runs argument lists, never shell text. A missing program is a check
// failure with exit -1, rather than a failure of the profiling operation. Once
// ctx ends, the running check is stopped with every process it started, and
// the checks after it fail without running.
func RunChecks(ctx context.Context, d Deps, dir string, commands []string) []journal.Check {
	checks := make([]journal.Check, 0, len(commands))
	for _, command := range commands {
		started := time.Now()
		check := journal.Check{Command: command, Exit: -1}
		if ctx.Err() == nil {
			check.Exit = d.RunCheck(ctx, dir, command)
		}
		check.Seconds = time.Since(started).Seconds()
		checks = append(checks, check)
	}
	return checks
}
