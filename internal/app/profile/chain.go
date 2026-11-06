package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lorem-dev/duolog/internal/journal"
)

type replayInput struct {
	task, before, result, leadDelta []byte
	summary                         string
	outputs                         [][]byte
}

// Read every input and verify chain history before starting a paid worker.
// Later steps must consume replay outputs through files, not pasted originals.
func prepareReplay(ctx context.Context, root string, j *journal.Journal, records []journal.Delegation) ([]replayInput, error) {
	inputs := make([]replayInput, len(records))
	chain := len(records) > 1
	for i, rec := range records {
		input := &inputs[i]
		dir := j.TaskDir(rec.Task, rec.ID)
		var err error
		if input.task, err = os.ReadFile(filepath.Join(dir, "task.md")); err != nil {
			return nil, err
		}
		if input.before, err = os.ReadFile(filepath.Join(dir, "before.patch")); err != nil {
			return nil, err
		}
		if input.result, err = os.ReadFile(filepath.Join(dir, "result.patch")); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(dir, "answer.json"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			var answer struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(data, &answer); err != nil {
				return nil, err
			}
			input.summary = answer.Summary
		}
		if chain && (rec.BeforeTree == "" || rec.AfterTree == "") {
			return nil, fmt.Errorf("chain cannot replay delegation %s: recorded before/after trees are missing", rec.ID)
		}
		for _, tree := range []string{rec.BeforeTree, rec.AfterTree} {
			if tree != "" {
				if _, err := git(ctx, root, "cat-file", "-e", tree+"^{tree}"); err != nil {
					return nil, fmt.Errorf("cannot replay delegation %s: recorded tree is unavailable: %w", rec.ID, err)
				}
			}
		}
		if chain {
			if summary := bytes.TrimSpace([]byte(input.summary)); len(summary) > 0 {
				input.outputs = append(input.outputs, summary)
			}
			files, err := journal.NumStat(root, rec.BeforeTree, rec.AfterTree)
			if err != nil {
				return nil, err
			}
			for _, file := range files {
				data, err := git(ctx, root, "show", rec.AfterTree+":"+file.Path)
				if err != nil {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					continue
				}
				if bytes.IndexByte(data, 0) < 0 && len(bytes.TrimSpace(data)) > 0 {
					input.outputs = append(input.outputs, bytes.TrimSpace(data))
				}
			}
		}
		if i == 0 {
			continue
		}
		input.leadDelta, err = journal.Diff(root, records[i-1].AfterTree, rec.BeforeTree)
		if err != nil {
			return nil, fmt.Errorf("chain cannot reconstruct lead changes before %s: %w", rec.ID, err)
		}
		contents := [][]byte{input.task}
		changes, err := journal.NumStat(root, records[i-1].AfterTree, rec.BeforeTree)
		if err != nil {
			return nil, err
		}
		for _, file := range changes {
			// Deleted files have no new contents to pass to the next step.
			if data, err := git(ctx, root, "show", rec.BeforeTree+":"+file.Path); err == nil {
				contents = append(contents, data)
			}
		}
		for _, content := range contents {
			if bytes.Contains(content, []byte(".duolog/journal/")) || bytes.Contains(content, []byte(".duolog/tasks/")) {
				return nil, fmt.Errorf("chain cannot replay %s: its inputs reference original delegation artifacts; replay one step instead", rec.ID)
			}
			for k := 0; k < i; k++ {
				for _, output := range inputs[k].outputs {
					if bytes.Contains(content, output) {
						return nil, fmt.Errorf("chain cannot replay %s: its inputs embed original output from %s; pass outputs through files or replay one step", rec.ID, records[k].ID)
					}
				}
			}
		}
	}
	return inputs, nil
}

func recreateWorktree(ctx context.Context, root, wt, base string) error {
	if err := removeWorktree(root, wt); err != nil {
		return err
	}
	_, err := git(ctx, root, "worktree", "add", "--detach", wt, base)
	return err
}

func verifyWorktree(wt string, identity []byte) error {
	info, err := os.Lstat(filepath.Join(wt, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("profiling worktree .git file is missing or changed")
	}
	current, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil || !bytes.Equal(current, identity) {
		return fmt.Errorf("profiling worktree .git file is missing or changed")
	}
	return nil
}
