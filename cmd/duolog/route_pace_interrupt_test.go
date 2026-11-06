package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lorem-dev/duolog/internal/testbin"
)

// An interrupt during a pace read ends the read's whole process tree: the
// agent's child does not outlive the command.
func TestInterruptDuringPaceReadStopsDescendant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native Windows signal test pending")
	}
	_, source, _, _ := runtime.Caller(0)
	binary := filepath.Join(t.TempDir(), "duolog")
	build := exec.Command("go", "build", "-o", binary, "./cmd/duolog")
	build.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	isolatedRouteConfig(t)
	routeLead(t)
	writeLocalConfig(t, "routing: {balance: pace}\n")
	// The login check answers at once; the usage read hangs with a child.
	fake := testbin.Install(t, "claude", testbin.Scenario{SpawnChild: true, SleepMS: 60000,
		Responses: map[string]testbin.Scenario{"auth status": {Stdout: `{"loggedIn":true}`}}})
	var output bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "route", "implement")
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(fake + ".child-pid"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake agent did not start", output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("route did not end after the interrupt", output.String())
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(fake + ".child-done"); !os.IsNotExist(err) {
		t.Fatal("agent descendant outlived the interrupted pace read", err)
	}
}
