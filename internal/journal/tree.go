package journal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// GitCommand runs git in dir with line-ending conversion off, so that trees
// hash the bytes on disk and patches apply byte for byte whatever the
// person's core.autocrlf (true on Windows by default).
func GitCommand(ctx context.Context, dir string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir, "-c", "core.autocrlf=false"}, args...)...)
}

func git(root string, env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := GitCommand(context.Background(), root, args...)
	cmd.Env = append(os.Environ(), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// InGit reports whether root is inside a Git work tree.
func InGit(root string) bool {
	out, err := git(root, nil, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Head returns the HEAD commit; outside Git, or before the first commit, it is empty.
func Head(root string) (string, error) {
	out, err := git(root, nil, nil, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// Capture hashes the working tree (untracked files included, .duolog excluded)
// through a temporary index, so the real index stays untouched.
// Each git call is a process, slow to start on Windows, so Capture uses few.
func Capture(root string) (string, error) {
	out, err := git(root, nil, nil, "rev-parse", "--is-inside-work-tree", "--git-path", "index")
	if err != nil {
		return "", nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || lines[0] != "true" {
		return "", nil
	}
	index := strings.TrimSpace(lines[1])
	if !filepath.IsAbs(index) {
		index = filepath.Join(root, index)
	}
	tmp, err := os.CreateTemp("", "duolog-index-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(name)
	if data, err := os.ReadFile(index); err == nil {
		if err := os.WriteFile(name, data, 0600); err != nil {
			return "", err
		}
	} else if os.IsNotExist(err) {
		_ = os.Remove(name)
	} else {
		return "", err
	}
	env := []string{"GIT_INDEX_FILE=" + name}
	// git add refuses a pathspec naming an ignored path, even to exclude it,
	// and projects ignore .duolog, so it is added with the rest. Then the
	// .duolog paths the project does not track (duolog's own state) that add
	// picked up, the untracked ones it does not ignore, are dropped; tracked
	// ones, such as a committed config.yaml, stay with their working-tree content.
	drop, err := git(root, env, nil, "ls-files", "-z", "--others", "--exclude-standard", "--", ".duolog")
	if err != nil {
		return "", err
	}
	if _, err := git(root, env, nil, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	if len(drop) > 0 {
		if _, err := git(root, env, drop, "update-index", "--force-remove", "-z", "--stdin"); err != nil {
			return "", err
		}
	}
	tree, err := git(root, env, nil, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

func Diff(root, from, to string) ([]byte, error) {
	return git(root, nil, nil, "diff", "--no-ext-diff", "--no-color", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/", "--binary", "--full-index", from, to)
}

func NumStat(root, from, to string) ([]FileChange, error) {
	out, err := git(root, nil, nil, "diff", "--no-ext-diff", "--no-color", "--no-textconv", "--numstat", "--no-renames", "-z", from, to)
	if err != nil {
		return nil, err
	}
	var files []FileChange
	for _, entry := range strings.Split(string(out), "\x00") {
		parts := strings.SplitN(entry, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0]) // "-" for binary files counts as 0
		removed, _ := strconv.Atoi(parts[1])
		files = append(files, FileChange{Path: parts[2], Added: added, Removed: removed})
	}
	return files, nil
}

func Apply(dir string, patch []byte) error {
	_, err := git(dir, nil, patch, "apply", "--binary", "--whitespace=nowarn", "-")
	return err
}
