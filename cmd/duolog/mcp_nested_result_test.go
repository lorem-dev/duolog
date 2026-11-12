package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/app/delegate"
)

func TestMCPNestedResultWithoutLocation(t *testing.T) {
	d, f, _, _ := submoduleDelegateProject(t)
	id := "d-nested"
	task := ".duolog/tasks/" + id + "/task.md"
	path := filepath.Join(delegate.ProjectRecordDir(f.Submodule), id+".json")
	r := delegate.Record{ID: id, Lead: "lead-mcp", Role: "docs", WorkRoot: f.Submodule,
		TaskPath: task, State: "done", Done: true, Summary: "Finished", Started: time.Now().UTC()}
	if err := delegate.WriteRecord(path, r); err != nil {
		t.Fatal(err)
	}
	// Older or interrupted runs need not have a registry location entry.
	for _, key := range []string{
		filepath.ToSlash(filepath.Join("module", task)),
		filepath.Join(f.Submodule, task),
		id,
	} {
		t.Run(key, func(t *testing.T) {
			got, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: key})
			if err != nil || got.ID != id || got.State != "done" || got.Done == nil || !*got.Done || got.Summary != r.Summary {
				t.Fatalf("result %s: %+v (%v)", key, got, err)
			}
			sameFile(t, got.AnswerPath, path)
		})
	}
}
