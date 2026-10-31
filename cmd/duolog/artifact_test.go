package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/artifact"
)

func TestArtifactCommandsInLinkedWorktree(t *testing.T) {
	// Construct Git's on-disk worktree layout without state-changing Git commands.
	base := t.TempDir()
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	mainRoot := filepath.Join(base, "main")
	linkedName := "linked "
	if runtime.GOOS == "windows" {
		linkedName = "linked worktree" // Windows disallows trailing spaces.
	}
	linkedRoot := filepath.Join(base, linkedName)
	common := filepath.Join(mainRoot, ".git")
	worktreeGit := filepath.Join(common, "worktrees", "linked")
	for _, dir := range []string{filepath.Join(common, "objects"), filepath.Join(common, "refs"), worktreeGit, filepath.Join(linkedRoot, "sub")} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for path, content := range map[string]string{
		filepath.Join(common, "HEAD"):                "ref: refs/heads/main\n",
		filepath.Join(worktreeGit, "HEAD"):           "ref: refs/heads/linked\n",
		filepath.Join(worktreeGit, "commondir"):      "../..\n",
		filepath.Join(worktreeGit, "gitdir"):         filepath.Join(linkedRoot, ".git") + "\n",
		filepath.Join(linkedRoot, ".git"):            "gitdir: " + worktreeGit + "\n",
		filepath.Join(mainRoot, "source.txt"):        "main content\n",
		filepath.Join(linkedRoot, "source.txt"):      "linked content\n",
		filepath.Join(linkedRoot, "only-linked.txt"): "only linked\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(filepath.Join(linkedRoot, "sub"))
	if root, err := projectRoot(); err != nil || root != mainRoot {
		t.Fatalf("shared project root = %q, %v; want %q", root, err, mainRoot)
	}
	run := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		cmd := newRootCommand()
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out.String(), stderr.String()
	}
	for _, name := range []string{"source.txt", "only-linked.txt"} {
		t.Run(name, func(t *testing.T) {
			want, err := artifact.RepoRef(linkedRoot, name)
			if err != nil {
				t.Fatal(err)
			}
			ref, _ := run(t, "ref", name)
			if ref != want+"\n" {
				t.Errorf("ref = %q; want %q", ref, want+"\n")
			}
			content, stderr := run(t, "read", want)
			wantContent, err := os.ReadFile(filepath.Join(linkedRoot, name))
			if err != nil || content != string(wantContent) || stderr != "" {
				t.Fatalf("read = %q, %q; want %q (error %v)", content, stderr, wantContent, err)
			}
		})
	}
	snapshot, _ := run(t, "put", "../source.txt")
	if _, err := os.Stat(filepath.Join(mainRoot, ".duolog", "artifacts", strings.TrimPrefix(strings.TrimSpace(snapshot), "a/"))); err != nil {
		t.Fatalf("snapshot missing from shared store: %v", err)
	}
	content, stderr := run(t, "read", strings.TrimSpace(snapshot))
	if content != "linked content\n" || stderr != "" {
		t.Fatalf("snapshot read = %q, %q", content, stderr)
	}
}
