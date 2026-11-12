package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/adapter/mcp"
	"github.com/lorem-dev/duolog/internal/adapter/session"
	"github.com/lorem-dev/duolog/internal/app/delegate"
	"github.com/lorem-dev/duolog/internal/app/paidruns"
	"github.com/lorem-dev/duolog/internal/core/config"
)

// fakeMCPWorkerStart stands for the child's admission handshake in tests
// which inspect detached argv without running a worker.
func fakeMCPWorkerStart(argv []string) error {
	i := slices.Index(argv, "--result-file")
	if i < 0 || i+1 >= len(argv) {
		return errors.New("missing result file")
	}
	r, err := delegate.ReadRecord(argv[i+1], nil)
	if err != nil {
		return err
	}
	r.Agent = "codex"
	return delegate.WriteRecord(argv[i+1], r)
}

func TestMCPDelegateReportsAdmissionWaitUntilHandback(t *testing.T) {
	for _, reason := range []string{"waiting for codex quota", "waiting for a codex session slot"} {
		t.Run(reason, func(t *testing.T) {
			d, _, _ := mcpDelegateProject(t)
			shortWait(t, time.Second)
			release, finished := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(func() { unblock(); <-finished })
			d.RunDelegate = func(_ context.Context, r delegate.Request) (delegate.Outcome, error) {
				r.Options.AdmissionWait(reason)
				<-release
				r.Options.AdmissionWait("")
				p := &delegate.Placement{Where: "self", Task: r.TaskPath}
				return delegate.Outcome{RunHere: p, Line: p.String()}, nil
			}
			d.startDetached = func(argv []string, _, _ string) error {
				go func() {
					defer close(finished)
					var log bytes.Buffer
					cmd := newRootCommand(d)
					cmd.SetOut(&log)
					cmd.SetErr(&log)
					cmd.SetArgs(argv[1:])
					_ = cmd.Execute()
				}()
				return nil
			}
			out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: "plan", Task: testTask, Wait: &no})
			if err != nil || out.State != "running" || out.WaitReason != reason {
				t.Fatalf("delegate wait: %+v %v", out, err)
			}
			result, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: out.ID, Wait: &no})
			if err != nil || result.WaitReason != reason {
				t.Fatalf("result wait: %+v %v", result, err)
			}
			status, err := d.mcpStatus(context.Background())
			if err != nil || delegationRow(t, status.Delegations, out.AnswerPath).WaitReason != reason {
				t.Fatalf("status wait: %+v %v", status, err)
			}
			unblock()
			result, err = d.mcpDelegate(context.Background(), mcp.DelegateInput{Action: "result", TaskPath: out.ID})
			if err != nil || result.State != "run_here" || result.WaitReason != "" {
				t.Fatalf("after wait: %+v %v", result, err)
			}
		})
	}
}

func TestMCPDelegateWaitFalseReturnsEarlyPlacement(t *testing.T) {
	for _, scenario := range []string{"self", "fallback to lead", "small task"} {
		t.Run(scenario, func(t *testing.T) {
			d, root, stateDir := mcpDelegateProject(t)
			startInProcess(t, d)
			reg, err := session.Open(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			lead := session.Lead{ID: "lead-mcp", Target: config.Target{Agent: "claude", Model: "opus"}, PID: os.Getpid()}
			if scenario == "fallback to lead" {
				lead.Target = config.Target{Agent: "codex", Model: "sol"}
				if err := reg.MarkUnavailable("claude", time.Time{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := reg.RegisterLead(lead); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "parse.go"), []byte("package parser\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			d.Delegate.PaidGate = func(*config.Config) (*paidruns.Gate, error) {
				t.Fatal("early placement opened the account gate")
				return nil, nil
			}
			role := "plan"
			if scenario == "small task" {
				role = "implement"
			}
			out, err := d.mcpDelegate(context.Background(), mcp.DelegateInput{Role: role, Task: testTask, Wait: &no})
			if err != nil || out.State != "run_here" || out.RunHere == nil || out.RunHere.Task != out.TaskPath {
				t.Fatalf("early placement: %+v %v", out, err)
			}
		})
	}
}

func TestMCPStatusNamesAdmissionWait(t *testing.T) {
	d, root, _ := mcpDelegateProject(t)
	// Read the record as JSON so the test also checks its persisted field.
	var r delegate.Record
	if err := json.Unmarshal([]byte(`{"id":"d-aaaaaa","lead":"lead-mcp","role":"plan","state":"running","wait_reason":"waiting for a codex session slot"}`), &r); err != nil {
		t.Fatal(err)
	}
	r.Started = time.Now().UTC()
	path := writeStatusRecord(t, root, r)
	out, err := d.mcpStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(delegationRow(t, out.Delegations, path))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(data, &row); err != nil {
		t.Fatal(err)
	}
	if row["wait_reason"] != "waiting for a codex session slot" {
		t.Fatalf("status hides admission wait: %s", data)
	}
}
