package workerenv

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var workspaceWriteLaunch = []string{"exec", "-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=false",
	"-c", "sandbox_workspace_write.writable_roots=[]"}

// cacheInput is a Codex workspace-write launch of the project at lead whose
// temp folder is a fresh test folder, which it returns.
func cacheInput(t *testing.T, shape Shape, variant Variant, lead string) (ProcessInput, string) {
	t.Helper()
	tmp := t.TempDir()
	in := buildInput("codex", shape, variant)
	pa := in.Config.PaidRuns.Agent("codex")
	pa.Allow, pa.DailyCapUSD = true, 5
	in.Config.PaidRuns.Set("codex", pa)
	in.Launch = workspaceWriteLaunch
	in.StateDir = lead
	in.Environ = append(slices.Clone(in.Environ), "TMPDIR="+tmp)
	return in, tmp
}

// projectCacheDir is where the caches of the project at lead sit under tmp:
// a folder named by a short hash of the lead root.
func projectCacheDir(tmp, lead string) string {
	sum := sha256.Sum256([]byte(lead))
	return filepath.Join(tmp, "duolog-go-cache", hex.EncodeToString(sum[:])[:12])
}

// A Codex worker or peer in workspace-write cannot write Go's default cache, so
// it gets the build and lint caches under the temp folder, in a folder of its
// project, and TMPDIR, which Codex may not otherwise add to its writable roots.
func TestCodexWorkspaceWriteGetsGoCaches(t *testing.T) {
	for _, shape := range []Shape{Worker, Peer} {
		for _, variant := range []Variant{Free, Paid} {
			in, tmp := cacheInput(t, shape, variant, "/lead/one")
			base := projectCacheDir(tmp, "/lead/one")
			p := mustBuild(t, in)
			if v, _ := envValue(p.Env, "GOCACHE"); v != filepath.Join(base, "go-build") {
				t.Errorf("%s %s: GOCACHE=%q", shape, variant, v)
			}
			if v, _ := envValue(p.Env, "GOLANGCI_LINT_CACHE"); v != filepath.Join(base, "golangci-lint") {
				t.Errorf("%s %s: GOLANGCI_LINT_CACHE=%q", shape, variant, v)
			}
			// uv run takes a lock in its cache, which the sandbox refuses in ~/.cache/uv.
			if v, _ := envValue(p.Env, "UV_CACHE_DIR"); v != filepath.Join(base, "uv") {
				t.Errorf("%s %s: UV_CACHE_DIR=%q", shape, variant, v)
			}
			if v, _ := envValue(p.Env, "TMPDIR"); v != tmp || envCount(p.Env, "TMPDIR") != 1 {
				t.Errorf("%s %s: TMPDIR=%q, want %q once", shape, variant, v, tmp)
			}
			// Codex's own environment policy must keep them for its commands.
			for _, name := range []string{"GOCACHE", "UV_CACHE_DIR", "TMPDIR"} {
				if !slices.ContainsFunc(p.Prefix, func(a string) bool { return strings.HasPrefix(a, "shell_environment_policy.set."+name+"=") }) {
					t.Errorf("%s %s: no shell_environment_policy.set.%s in %v", shape, variant, name, p.Prefix)
				}
			}
			if runtime.GOOS != "windows" {
				for _, dir := range []string{base, filepath.Join(base, "go-build"), filepath.Join(base, "golangci-lint"), filepath.Join(base, "uv")} {
					if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
						t.Errorf("%s %s: %s: %v %v, want a folder with mode 0700", shape, variant, dir, st, err)
					}
				}
			}
		}
	}
}

// Two projects never share a cache folder; the worktrees of one project do,
// because they have the same lead root.
func TestGoCacheFolderIsPerProject(t *testing.T) {
	in, _ := cacheInput(t, Worker, Free, "/lead/one")
	one, _ := envValue(mustBuild(t, in).Env, "GOCACHE")
	again, _ := envValue(mustBuild(t, in).Env, "GOCACHE")
	in.StateDir = "/lead/two"
	two, _ := envValue(mustBuild(t, in).Env, "GOCACHE")
	if one == "" || one != again {
		t.Errorf("one project got %q and %q", one, again)
	}
	if one == two {
		t.Errorf("two projects share %q", one)
	}
}

// A folder that is open to the group or the world is not used: no variable is
// set and stderr says so.
func TestGoCacheFolderNotSafe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mode bits")
	}
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777} {
		in, tmp := cacheInput(t, Worker, Free, "/lead/one")
		base := projectCacheDir(tmp, "/lead/one")
		if err := os.MkdirAll(base, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(base, mode); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		old := cacheWarnings
		cacheWarnings = &stderr
		p := mustBuild(t, in)
		cacheWarnings = old
		for _, name := range []string{"GOCACHE", "GOLANGCI_LINT_CACHE"} {
			if _, ok := envValue(p.Env, name); ok {
				t.Errorf("mode %o: %s set", mode, name)
			}
		}
		if v, _ := envValue(p.Env, "TMPDIR"); v == tmp {
			t.Errorf("mode %o: TMPDIR set", mode)
		}
		if slices.ContainsFunc(p.Prefix, func(a string) bool { return strings.Contains(a, "GOCACHE") || strings.Contains(a, "TMPDIR") }) {
			t.Errorf("mode %o: the prefix names a cache variable: %v", mode, p.Prefix)
		}
		if !strings.Contains(stderr.String(), base) {
			t.Errorf("mode %o: stderr %q does not say which folder", mode, stderr.String())
		}
	}
}

// Read-only Codex, Claude and the lead get no cache variables, and a value the
// person set and passed on stays.
func TestGoCachesOnlyForCodexWorkspaceWrite(t *testing.T) {
	for _, c := range []struct {
		agent  string
		launch []string
	}{
		{"codex", []string{"exec", "-s", "read-only"}},
		{"codex", nil},
		{"claude", workspaceWriteLaunch},
	} {
		in := buildInput(c.agent, Worker, Free)
		in.Launch = c.launch
		p := mustBuild(t, in)
		if _, ok := envValue(p.Env, "GOCACHE"); ok {
			t.Errorf("%s %v: GOCACHE set", c.agent, c.launch)
		}
	}
	in, _ := cacheInput(t, Worker, Free, "/lead/one")
	in.Config.EnvPassthrough = append(in.Config.EnvPassthrough, "GOCACHE")
	in.Environ = append(in.Environ, "GOCACHE=/mine/cache")
	p := mustBuild(t, in)
	if v, _ := envValue(p.Env, "GOCACHE"); v != "/mine/cache" || envCount(p.Env, "GOCACHE") != 1 {
		t.Errorf("a set GOCACHE was replaced: %q", v)
	}
	if v, _ := envValue(p.Env, "GOLANGCI_LINT_CACHE"); v == "" {
		t.Error("the lint cache was not set")
	}
}
