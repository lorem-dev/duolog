package calibration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/install"
	"github.com/lorem-dev/duolog/internal/journal"
	"github.com/lorem-dev/duolog/internal/pathutil"
	projectroot "github.com/lorem-dev/duolog/internal/project"
	"github.com/lorem-dev/duolog/internal/trust"
)

// The single commit of an exported tree carries no person and no real date,
// so it says nothing about who wrote the change or when.
const (
	exportName    = "duolog calibration"
	exportEmail   = "calibration@duolog.invalid"
	exportDate    = "2000-01-01T00:00:00Z"
	exportMessage = "calibration base"
	stateFolder   = ".duolog/calibration"
)

// NewTree exports commit into a fresh repository under os.TempDir() named
// duolog-cal-*; it refuses a destination inside repo.
func NewTree(ctx context.Context, repo, commit string) (dir string, cleanup func() error, err error) {
	dir, err = os.MkdirTemp("", "duolog-cal-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() error { return os.RemoveAll(dir) }
	if err := Export(ctx, repo, commit, dir); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// codexHooks is where Codex reads a project's hooks; claudeLocal is the
// personal Claude settings file, where a tree gets duolog's gate when
// nothing else gives Claude one there.
const (
	codexHooks  = ".codex/hooks.json"
	claudeLocal = ".claude/settings.local.json"
)

// treeOwn are the files of a tree slot that duolog writes itself; an export
// into a slot leaves the commit's copies out, and the tree's commit does not
// track them.
var treeOwn = []string{codexHooks, claudeLocal}

// gateTimeout is the gate's hook timeout in seconds, as duolog install
// writes it.
const gateTimeout = 150

// TreeSlot is the folder cell slot n of root's project works in:
// <user cache dir>/duolog/calibration/<project id>/tree-<n>. It stays the
// same from run to run, so the person trusts it in Codex once.
func TreeSlot(root string, n int) (string, error) {
	if n < 0 {
		return "", fmt.Errorf("tree slot %d: not a slot number", n)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "duolog", "calibration", projectID(root), fmt.Sprintf("tree-%d", n)), nil
}

// projectID is a short hash of the project's resolved main checkout, so
// its linked worktrees share it. duolog keeps no other project id: its
// per-project state lives in the project's own .duolog folder.
func projectID(root string) string {
	sum := sha256.Sum256([]byte(pathutil.Resolved(absolute(projectroot.StateRoot(absolute(root))))))
	return hex.EncodeToString(sum[:])[:16]
}

// NewTreeAt exports commit into dir as NewTree does, into the slot
// PrepareTree leaves, so a Codex worker runs with the project's gate
// whichever commit the tree holds. When Claude would run there without the
// duolog gate, from the person's settings, the plugin or the commit's
// project settings, the tree gets the gate in .claude/settings.local.json.
// dir must neither be inside repo nor hold it. The cleanup empties dir but
// for the Codex hooks, which the person approved in that place.
func NewTreeAt(ctx context.Context, repo, commit, dir string) (cleanup func() error, err error) {
	if err := PrepareTree(ctx, repo, dir); err != nil {
		return nil, err
	}
	cleanup = func() error { return clearTree(dir) }
	if err := export(ctx, repo, commit, dir, treeOwn); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	if err := addClaudeGate(dir); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return cleanup, nil
}

// PrepareTree empties dir, the tree slot of repo, but for the project's
// .codex/hooks.json as its working copy has it, or as HEAD has it when the
// working copy has none. Codex approves hooks by their path, so the person
// approves these once in the slot, before or after the first refused cell;
// a project without the file leaves the slot empty.
func PrepareTree(ctx context.Context, repo, dir string) error {
	if inside(repo, dir) || inside(dir, repo) {
		return fmt.Errorf("tree %s must be outside the project %s", dir, repo)
	}
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return fmt.Errorf("tree %s is not a folder", dir)
	}
	hooks, err := projectHooks(ctx, repo)
	if err != nil {
		return err
	}
	if err := clearTree(dir); err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(codexHooks))
	// Written afresh, so a candidate's edit or a link in its place is gone.
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	if hooks == nil {
		// Only an empty folder goes.
		_ = os.Remove(filepath.Dir(target))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, hooks, 0o644)
}

// addClaudeGate writes duolog's Claude hooks into dir's local settings
// unless Claude already runs the gate in dir.
func addClaudeGate(dir string) error {
	state, err := trust.Hooks("claude", dir)
	if err != nil || state.Installed {
		return err
	}
	hooks, err := install.HooksJSON("claude", gateTimeout)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, filepath.FromSlash(claudeLocal))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, hooks, 0o644)
}

// clearTree empties dir but for its Codex hooks, kept only as a regular
// file; a missing dir is already empty. A dir that is a link or a file is
// removed itself, never followed.
func clearTree(dir string) error {
	if info, err := os.Lstat(dir); err == nil && !info.IsDir() {
		return os.Remove(dir)
	}
	return emptyDirKeeping(dir, []string{codexHooks})
}

// projectHooks returns the project's .codex/hooks.json from its working
// copy, where an installed gate may not be committed yet, or else from HEAD.
func projectHooks(ctx context.Context, repo string) ([]byte, error) {
	hooks, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(codexHooks)))
	if errors.Is(err, os.ErrNotExist) {
		return headHooks(ctx, repo)
	}
	return hooks, err
}

// headHooks returns the project's .codex/hooks.json at HEAD, or nil when
// HEAD has none.
func headHooks(ctx context.Context, repo string) ([]byte, error) {
	if _, err := run(ctx, repo, "cat-file", "-e", "HEAD:"+codexHooks); err != nil {
		return nil, nil
	}
	return run(ctx, repo, "cat-file", "blob", "HEAD:"+codexHooks)
}

// emptyDirKeeping removes everything in dir and keeps dir, the regular
// files keep names, as slash paths below dir, and the folders that lead to
// them. A link is never kept or followed. A missing dir is already empty.
func emptyDirKeeping(dir string, keep []string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := filepath.Join(dir, e.Name())
		var below []string
		kept := false
		for _, k := range keep {
			first, rest, nested := strings.Cut(k, "/")
			switch {
			case first != e.Name():
			case nested:
				below = append(below, rest)
			default:
				kept = true
			}
		}
		switch {
		case kept && e.Type().IsRegular():
		case len(below) > 0 && e.IsDir():
			errs = append(errs, emptyDirKeeping(name, below))
		default:
			errs = append(errs, os.RemoveAll(name))
		}
	}
	return errors.Join(errs...)
}

// CheckTreeTrust says what an agent needs before a worker runs in dir. Codex
// needs the folder trusted and its hooks approved; duolog only reads the
// Codex config, and the person grants both in Codex. Claude needs no trust,
// but the duolog gate must be active for it in dir: installed, and no
// settings layer Claude loads there sets disableAllHooks.
func CheckTreeTrust(agent, dir string) error {
	folder, err := trust.Folder(agent, dir)
	if err != nil {
		return err
	}
	hooks, err := trust.Hooks(agent, dir)
	if err != nil {
		return err
	}
	if agent == "claude" {
		if hooks.HooksOff != "" {
			return fmt.Errorf("Claude would run without the duolog gate in the calibration tree %s, so no cell runs there: %s sets disableAllHooks; remove it from that file, or from the commit's .claude settings with a branch that lacks it", dir, hooks.HooksOff)
		}
		if hooks.Installed {
			return nil
		}
		return fmt.Errorf("Claude has no duolog gate in the calibration tree %s, so no cell runs there: duolog writes it to %s when no other settings run it; check that the folder is writable", dir, claudeLocal)
	}
	if folder && hooks.Trusted {
		return nil
	}
	return fmt.Errorf("Codex must trust the calibration tree %s and its hooks need approval: open Codex in %s once, trust the folder and approve its hooks in /hooks", dir, dir)
}

// Export writes the tree of commit into dst as a new repository with one
// commit, so a candidate sees neither the history nor the descendants of
// commit. The files are the blobs as Git stores them: export-ignore and
// export-subst do not apply. Files under .duolog/calibration/ are left out:
// they hold the manifest and evidence of calibration itself.
func Export(ctx context.Context, repo, commit, dst string) error {
	return export(ctx, repo, commit, dst, nil)
}

// export is Export for a dst that may already hold the own files, slash
// paths below dst: the commit's copies of them are left out, and the new
// commit does not track them.
func export(ctx context.Context, repo, commit, dst string, own []string) error {
	if inside(repo, dst) {
		return fmt.Errorf("export destination %s must be outside the project %s", dst, repo)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := onlyOwn(dst, own); err != nil {
		return err
	}
	if err := extract(ctx, repo, commit, dst, own); err != nil {
		return err
	}
	// Each link was checked on its own while extracting; a chain can still
	// leave dst once every link is in place.
	if err := checkLinks(dst); err != nil {
		return err
	}
	if _, err := run(ctx, dst, "init", "-q"); err != nil {
		return err
	}
	// Force: the person's global excludes or the project's own ignore rules
	// must not drop files the commit tracks.
	add := []string{"add", "-A", "--force", "--", "."}
	for _, name := range own {
		add = append(add, ":(exclude,literal)"+name)
	}
	if _, err := run(ctx, dst, add...); err != nil {
		return err
	}
	// No signature and no hook: the person's global config must not sign
	// with their key or run their post-commit scripts on a scratch tree.
	noHooks := filepath.Join(dst, ".git", "no-hooks")
	if _, err := run(ctx, dst, "-c", "commit.gpgsign=false", "-c", "core.hooksPath="+noHooks,
		"commit", "-q", "--no-verify", "--allow-empty", "-m", exportMessage); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(dst, ".git", "logs"))
}

// onlyOwn refuses a dst that holds anything but the regular files own names
// and the folders that lead to them.
func onlyOwn(dst string, own []string) error {
	return filepath.WalkDir(dst, func(name string, d os.DirEntry, err error) error {
		if err != nil || name == dst {
			return err
		}
		rel, err := filepath.Rel(dst, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, o := range own {
			if d.IsDir() && strings.HasPrefix(o, rel+"/") || d.Type().IsRegular() && o == rel {
				return nil
			}
		}
		return fmt.Errorf("export destination %s is not empty", dst)
	})
}

// isOwn reports whether name, a slash path of an archive entry, is one of
// own, without case, as case-insensitive file systems would.
func isOwn(name string, own []string) bool {
	return slices.ContainsFunc(own, func(o string) bool { return strings.EqualFold(o, name) })
}

// VerifyIsolated checks that dir is an exported tree a candidate may work
// in: one commit, no remote, no alternates, no reflog, no calibration state,
// no link that leads out of dir, and outside repo.
func VerifyIsolated(dir, repo string) error {
	if inside(repo, dir) {
		return fmt.Errorf("tree %s is not outside the project %s", dir, repo)
	}
	gitDir := filepath.Join(dir, ".git")
	if info, err := os.Lstat(gitDir); err != nil || !info.IsDir() {
		return fmt.Errorf("tree %s has no own .git folder", dir)
	}
	ctx := context.Background()
	out, err := run(ctx, dir, "rev-list", "--all", "--count")
	if err != nil {
		return err
	}
	if n := strings.TrimSpace(string(out)); n != "1" {
		return fmt.Errorf("tree %s has %s commits, want exactly one", dir, n)
	}
	out, err = run(ctx, dir, "remote")
	if err != nil {
		return err
	}
	if remotes := strings.Fields(string(out)); len(remotes) > 0 {
		return fmt.Errorf("tree %s has a remote: %s", dir, strings.Join(remotes, ", "))
	}
	if exists(filepath.Join(gitDir, "objects", "info", "alternates")) {
		return fmt.Errorf("tree %s borrows objects through .git/objects/info/alternates", dir)
	}
	if exists(filepath.Join(gitDir, "logs")) {
		return fmt.Errorf("tree %s keeps a reflog in .git/logs", dir)
	}
	if exists(filepath.Join(dir, filepath.FromSlash(stateFolder))) {
		return fmt.Errorf("tree %s contains %s", dir, stateFolder)
	}
	return checkLinks(dir)
}

// maxLinkHops bounds link resolution; operating systems give up well
// before it, so a longer chain cannot be followed anyway.
const maxLinkHops = 255

// checkLinks walks root and refuses any symbolic link that, followed
// through every link it meets, leads out of root.
func checkLinks(root string) error {
	return filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if err := resolveInside(root, filepath.ToSlash(rel)); err != nil {
			return fmt.Errorf("link %s points outside the export %s: %w", filepath.ToSlash(rel), root, err)
		}
		return nil
	})
}

// resolveInside follows name, a slash path below root, one component at a
// time as the file system would, and fails once it would leave root. A
// component that does not exist resolves lexically: the system could not
// follow it either.
func resolveInside(root, name string) error {
	pending := strings.Split(name, "/")
	var resolved []string
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return errors.New("it climbs above the export")
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}
		current := filepath.Join(root, filepath.Join(resolved...), part)
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			resolved = append(resolved, part)
			continue
		}
		if hops++; hops > maxLinkHops {
			return errors.New("too many links")
		}
		target, err := os.Readlink(current)
		if err != nil {
			return err
		}
		rel, ok := relativeTarget(target)
		if !ok {
			return fmt.Errorf("absolute target %s", target)
		}
		pending = append(strings.Split(rel, "/"), pending...)
	}
	return nil
}

// relativeTarget returns target, a link target, as a slash path, and false
// when target does not lead from its link's folder. On Windows, which
// writes link targets with backslashes, a backslash separates as a slash
// does, and an absolute, rooted, drive-relative or UNC target is refused.
// Elsewhere a backslash is part of a name, but a target written the
// Windows way is refused as well: it would leave the export on Windows.
func relativeTarget(target string) (string, bool) {
	if runtime.GOOS == "windows" {
		target = filepath.ToSlash(target)
		if filepath.IsAbs(target) || filepath.VolumeName(target) != "" || strings.HasPrefix(target, "/") {
			return "", false
		}
		return target, true
	}
	if pathutil.IsAbs(target) || pathutil.Windows(target) {
		return "", false
	}
	return target, true
}

// treeEntry is one entry of `git ls-tree -r`: a blob, or a submodule's
// commit.
type treeEntry struct{ mode, kind, oid, name string }

// extract writes the tree of commit under dst: the entries `git ls-tree`
// lists, with their blobs as `git cat-file --batch` stores them, with no
// shell and no pipe between processes. No attribute applies, as one would
// to git archive: export-ignore leaves nothing out and export-subst
// rewrites nothing, whether the tree, .git/info/attributes or the person's
// own attributes set them.
func extract(ctx context.Context, repo, commit, dst string, own []string) error {
	listing, err := run(ctx, repo, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return err
	}
	var entries []treeEntry
	var oids strings.Builder
	for _, record := range strings.Split(string(listing), "\x00") {
		if record == "" {
			continue
		}
		meta, name, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return fmt.Errorf("git ls-tree %s: cannot read %q", commit, record)
		}
		e := treeEntry{mode: fields[0], kind: fields[1], oid: fields[2], name: path.Clean(name)}
		if e.name == "." || calibrationState(e.name) || isOwn(e.name, own) {
			continue
		}
		if pathutil.Outside(e.name) || pathutil.Windows(e.name) {
			return fmt.Errorf("tree entry %q leaves the export destination", name)
		}
		entries = append(entries, e)
		if e.kind == "blob" {
			oids.WriteString(e.oid + "\n")
		}
	}
	cmd := journal.GitCommand(ctx, repo, "cat-file", "--batch")
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(oids.String())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stream, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	werr := writeEntries(bufio.NewReader(stream), dst, entries)
	if werr != nil {
		// Drain so git can exit instead of blocking on a full pipe.
		_, _ = io.Copy(io.Discard, stream)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git cat-file %s: %w: %s", commit, err, strings.TrimSpace(stderr.String()))
	}
	return werr
}

// writeEntries writes entries under dst, reading each blob in turn from
// blobs, the output of git cat-file --batch.
func writeEntries(blobs *bufio.Reader, dst string, entries []treeEntry) error {
	for _, e := range entries {
		target := filepath.Join(dst, filepath.FromSlash(e.name))
		if !inside(dst, filepath.Dir(target)) {
			return fmt.Errorf("tree entry %q leaves the export destination", e.name)
		}
		if e.kind == "commit" {
			// A submodule: an empty folder, as git archive writes it.
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if e.kind != "blob" {
			return fmt.Errorf("tree entry %q has unsupported type %s", e.name, e.kind)
		}
		size, err := blobHeader(blobs, e.oid)
		if err != nil {
			return err
		}
		content := io.LimitReader(blobs, size)
		switch e.mode {
		case "100644", "100755":
			perm := os.FileMode(0o644)
			if e.mode == "100755" {
				perm = 0o755
			}
			if err := writeFile(target, content, perm); err != nil {
				return err
			}
		case "120000":
			link, err := io.ReadAll(content)
			if err != nil {
				return err
			}
			if linkLeaves(e.name, string(link)) {
				return fmt.Errorf("tree entry %q links outside the export destination", e.name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("tree entry %q has unsupported mode %s", e.name, e.mode)
		}
		// Every blob was read whole and ends with a newline of the batch.
		if n, _ := io.Copy(io.Discard, content); n != 0 {
			return fmt.Errorf("git cat-file: blob %s was not read whole", e.oid)
		}
		if b, err := blobs.ReadByte(); err != nil || b != '\n' {
			return fmt.Errorf("git cat-file: blob %s does not end its record", e.oid)
		}
	}
	return nil
}

// blobHeader reads the header git cat-file --batch writes before an object
// and returns the object's size; the object must be the blob oid.
func blobHeader(r *bufio.Reader, oid string) (int64, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("git cat-file: blob %s: %w", oid, err)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != oid || fields[1] != "blob" {
		return 0, fmt.Errorf("git cat-file: want blob %s, got %q", oid, strings.TrimSpace(header))
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 {
		return 0, fmt.Errorf("git cat-file: blob %s: size %q", oid, fields[2])
	}
	return size, nil
}

// linkLeaves reports whether a link at name with target leaves the export
// on its own: an absolute target, or a relative one that climbs above the
// root. Chains through other links are checked once the tree is complete.
func linkLeaves(name, target string) bool {
	rel, ok := relativeTarget(target)
	if target == "" || !ok {
		return true
	}
	return pathutil.Outside(path.Join(path.Dir(name), rel))
}

func writeFile(target string, r io.Reader, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// calibrationState matches .duolog/calibration and everything below it,
// without case, as case-insensitive file systems would.
func calibrationState(name string) bool {
	return strings.EqualFold(name, stateFolder) || len(name) > len(stateFolder) &&
		strings.EqualFold(name[:len(stateFolder)+1], stateFolder+"/")
}

// inside reports whether target is root or below it, after resolving links
// such as the macOS /var to /private/var.
func inside(root, target string) bool {
	rel, err := pathutil.Relative(pathutil.Resolved(absolute(root)), pathutil.Resolved(absolute(target)))
	return err == nil && !pathutil.Outside(rel)
}

func absolute(name string) string {
	if abs, err := filepath.Abs(name); err == nil {
		return abs
	}
	return name
}

func exists(name string) bool {
	_, err := os.Lstat(name)
	return err == nil
}

// run calls git in dir with a fixed author and date and without the Git
// variables that would point it at another repository.
func run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := journal.GitCommand(ctx, dir, args...)
	cmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME="+exportName, "GIT_AUTHOR_EMAIL="+exportEmail, "GIT_AUTHOR_DATE="+exportDate,
		"GIT_COMMITTER_NAME="+exportName, "GIT_COMMITTER_EMAIL="+exportEmail, "GIT_COMMITTER_DATE="+exportDate,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func gitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_NAMESPACE":
			continue
		}
		env = append(env, kv)
	}
	return env
}
