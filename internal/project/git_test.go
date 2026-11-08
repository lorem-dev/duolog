package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func TestGitRunsGit(t *testing.T) {
	needGit(t)
	out, err := Git(context.Background(), nil, "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("output = %q", out)
	}
}

// A Git hook that started the caller exports GIT_DIR, which would point git at
// its repository and not at dir.
func TestGitBatchNilEnvDropsGitVariables(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "one"},
	} {
		if out, err := Git(ctx, nil, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	b, err := GitBatch(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.In.Write([]byte("HEAD\n")); err != nil {
		t.Fatal(err)
	}
	header, err := b.Out.ReadString('\n')
	if err != nil {
		t.Fatalf("no answer from git: %v", err)
	}
	if !strings.Contains(header, " commit ") {
		t.Fatalf("header = %q, want the commit of dir", header)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGitBatchReadsBlob(t *testing.T) {
	needGit(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "a.txt"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "one"},
	} {
		if out, err := Git(ctx, nil, append([]string{"-C", dir}, args...)...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	b, err := GitBatch(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.In.Write([]byte("HEAD:a.txt\n")); err != nil {
		t.Fatal(err)
	}
	header, err := b.Out.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(header), " blob 2") {
		t.Fatalf("header = %q", header)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}
