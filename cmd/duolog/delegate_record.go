package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
)

// runDelegateRecorded is runDelegate for a run of the person's own command: it
// writes the run's record in the state dir, so duolog runs and duolog watch
// show the run, and the outcome over it at the end. A worker's call writes no
// record. A record that cannot be written is told on stderr; the run and its
// exit code stay as they are.
func (d *cmdDeps) runDelegateRecorded(stderr io.Writer, role, taskPath, lead string) delegateFunc {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		if isWorker() {
			return d.runDelegate(ctx, r)
		}
		if lead == "" {
			lead = os.Getenv("DUOLOG_LEAD")
		}
		work := workRoot(filepath.Dir(r.StateDir))
		base := delegate.Record{ID: delegate.NewRecordID(), Role: role, Lead: lead, TaskPath: workRelative(work, taskPath), WorkRoot: work, PID: os.Getpid(), State: "running", Started: time.Now().UTC()}
		path, err := delegate.CreateRecord(delegate.RecordDir(r.StateDir), base, session.ProcessAlive)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "duolog delegate: cannot write the record: %v\n", err)
			return d.runDelegate(ctx, r)
		}
		r.Options.Begun = recordBegun(path, stderr, true)
		out, err := d.runDelegateRecording(path)(ctx, r)
		writeDelegateResult(stderr, path, role, base.TaskPath, out, err)
		return out, err
	}
}

// workRelative is taskPath relative to work, slash-separated, when it is under
// work; otherwise it is absolute.
func workRelative(work, taskPath string) string {
	abs, err := filepath.Abs(taskPath)
	if err != nil {
		return taskPath
	}
	rel, err := filepath.Rel(work, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return filepath.ToSlash(rel)
}

// recordBegun is the Begun option of a run with a record at path: it puts the
// resolved lead and the task folder in the running record and, with index,
// stores the run's work project in the lead's registry. A failure is told on
// stderr and never changes the exit code.
func recordBegun(path string, stderr io.Writer, index bool) func(lead, taskDir string) {
	return func(lead, taskDir string) {
		r, err := delegate.ReadRecord(path, nil)
		if err != nil || r.State != "running" {
			return
		}
		r.Lead, r.TaskDir = lead, taskDir
		if err := delegate.WriteRecord(path, r); err != nil {
			_, _ = fmt.Fprintf(stderr, "duolog delegate: cannot write the task folder to the record %s: %v\n", path, err)
		}
		if !index {
			return
		}
		stateDir := filepath.Dir(filepath.Dir(path))
		if err := delegate.IndexRecord(stateDir, lead, r.ID); err != nil {
			_, _ = fmt.Fprintf(stderr, "duolog delegate: cannot index the run %s: %v\n", r.ID, err)
		}
	}
}

// withBegun is run with begun set as the request's Begun option.
func withBegun(run delegateFunc, begun func(lead, taskDir string)) delegateFunc {
	return func(ctx context.Context, r delegate.Request) (delegate.Outcome, error) {
		r.Options.Begun = begun
		return run(ctx, r)
	}
}
