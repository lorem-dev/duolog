package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configfile "github.com/lorem-dev/duolog/internal/adapter/config"
	"github.com/lorem-dev/duolog/internal/coauthor"
	"github.com/lorem-dev/duolog/internal/store"
)

func TestCoauthorsCommand(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("DUOLOG_DEPTH", "0")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Example", "GIT_AUTHOR_EMAIL=example@example.com", "GIT_COMMITTER_NAME=Example", "GIT_COMMITTER_EMAIL=example@example.com", "GIT_AUTHOR_DATE=2020-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2020-01-01T00:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-q")
	cmd := newRootCommand(testDeps(t))
	cmd.SetArgs([]string{"coauthors"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("no records: %v", err)
	}
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configfile.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := coauthor.Append(s.Dir, cfg.Coauthors, coauthor.Record{Time: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), Agent: "codex", Model: "old", Files: 1}); err != nil {
		t.Fatal(err)
	}
	if err := coauthor.Append(s.Dir, cfg.Coauthors, coauthor.Record{Agent: "codex", Model: "sol", Files: 1}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := newRootCommand(testDeps(t))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"coauthors"}, args...))
		err := cmd.Execute()
		return out.String(), err
	}
	if got, err := run(); err != nil || strings.Count(got, "Co-Authored-By:") != 2 {
		t.Fatalf("unborn: %q %v", got, err)
	}
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "example")
	line := "Co-Authored-By: OpenAI Codex sol <noreply@openai.com>\n"
	if got, err := run(); err != nil || got != line {
		t.Fatalf("HEAD: %q %v", got, err)
	}
	if _, err := run("--since", "missing"); err == nil {
		t.Fatal("invalid revision accepted")
	}
	path := filepath.Join(root, "message")
	if err := os.WriteFile(path, []byte("subject\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("--hook", path); err != nil {
		t.Fatal(err)
	}
	if _, err := run("--hook", path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "subject\n\n"+line {
		t.Fatalf("hook: %q", data)
	}
	if _, err := run("--hook", path, "--since", "missing"); err != nil {
		t.Fatalf("hook blocked: %v", err)
	}
	if _, err := run("--hook", filepath.Join(root, "missing", "message")); err != nil {
		t.Fatalf("hook blocked: %v", err)
	}
	t.Setenv("DUOLOG_DEPTH", "1")
	if err := os.WriteFile(path, []byte("worker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("--hook", path); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "worker\n" {
		t.Fatal("worker hook changed message")
	}
}
