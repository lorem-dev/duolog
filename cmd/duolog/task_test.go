package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/mode"
	"github.com/lorem-dev/duolog/internal/session"
)

func learningRepo(t *testing.T, enabled bool) string {
	t.Helper()
	isolatedRouteConfig(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("DUOLOG_LEAD", "")
	t.Setenv("DUOLOG_DEPTH", "")
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatal(string(out), err)
		}
	}
	if enabled {
		os.MkdirAll(filepath.Join(root, ".duolog"), 0755)
		if err := os.WriteFile(filepath.Join(root, ".duolog", "config.yaml"), []byte("learning:\n  enabled: true\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := session.Open(filepath.Join(root, ".duolog"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterLead(session.Lead{ID: "task-lead", Target: config.Target{Agent: "claude", Model: "haiku"}, Mode: mode.Full, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTaskStartOpensTask(t *testing.T) {
	root := learningRepo(t, true)
	head, _ := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	out, err := executeBusCommand(t, "task", "start", "fix", "login")
	if err != nil {
		t.Fatal(out, err)
	}
	if !regexp.MustCompile(`task t-\d{8}-\d{6}-[0-9a-f]{4} started at ` + strings.TrimSpace(string(head))[:7]).MatchString(out) {
		t.Fatal(out)
	}
}

func TestTaskStartWhenLearningOff(t *testing.T) {
	root := learningRepo(t, false)
	out, err := executeBusCommand(t, "task", "start")
	if err != nil || strings.TrimSpace(out) != "learning mode is off in .duolog/config.yaml; nothing recorded" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".duolog", "journal")); !os.IsNotExist(err) {
		t.Fatal("journal created", err)
	}
}

func TestTaskCommandRefusesWorker(t *testing.T) {
	learningRepo(t, true)
	t.Setenv("DUOLOG_DEPTH", "1")
	_, err := executeBusCommand(t, "task", "start")
	if err == nil || !strings.Contains(err.Error(), "workers cannot change the journal") {
		t.Fatal(err)
	}
}
