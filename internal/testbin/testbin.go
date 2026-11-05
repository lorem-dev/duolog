// Package testbin installs compiled fake agents without shell wrappers.
package testbin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// OutputChunk writes one piece to the selected stream, in scenario order.
type OutputChunk struct {
	Text   string
	Stderr bool
}

// Scenario describes one fake invocation. Exit -1 terminates by a kill signal.
// Writes happen after SleepMS, and relative paths are relative to the child cwd.
// HookCommand runs an actual generated hook inside the fake worker environment.
type HookCommand struct {
	Command, Input string
	Args           []string
}

type Scenario struct {
	// RPCResponses answers each JSON-RPC method before stdin closes.
	RPCResponses map[string]string
	SpawnChild   bool
	Hooks        []HookCommand
	// Answer writes the Codex -o destination, discovered from real runner argv.
	Answer string
	// Responses selects a scenario by the longest matching argument prefix.
	Responses map[string]Scenario
	// Sequence replaces the scenario by call order: the nth call that selects
	// this scenario uses Sequence[n], and the last entry repeats.
	Sequence       []Scenario
	Output         []OutputChunk
	Stdout, Stderr string
	Exit           int
	SleepMS        int
	Writes         map[string]string
}

var build struct {
	sync.Once
	binary []byte
	// shared is one copy of binary that every Install links to, or empty.
	shared string
	err    error
}

// share writes binary once to the temporary folder, named by its hash so that
// every test process and run of the same fake finds the same file, and
// returns its path, or "" when it cannot. A new executable is scanned before
// its first start: 0.3 to 0.5 s on macOS for each fresh copy, which most tests
// paid once per fake they installed. Links to one file pay it once.
func share(binary []byte) string {
	sum := sha256.Sum256(binary)
	path := filepath.Join(os.TempDir(), "duolog-testbin-"+hex.EncodeToString(sum[:8]))
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if data, err := os.ReadFile(path); err == nil && bytes.Equal(data, binary) {
		return path
	}
	// Another test process may write the same file at the same time: each
	// writes its own and renames it into place.
	tmp, err := os.CreateTemp(filepath.Dir(path), "duolog-testbin-*.tmp")
	if err != nil {
		return ""
	}
	_, err = tmp.Write(binary)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0755)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		// Windows refuses to replace a file that runs; the other process
		// wrote the same bytes.
		if data, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(data, binary) {
			return ""
		}
	}
	return path
}

// startEnv is the environment the test process started with. Tests move HOME,
// XDG_CACHE_HOME and LOCALAPPDATA to temporary folders before they install a
// fake; building with those would miss the Go build cache and compile the
// standard library again, which took seconds per package.
var startEnv = os.Environ()

// Install builds fake once per test process and links it, or copies it where
// a link fails, into an isolated PATH entry. It returns the executable path;
// recordings use .args.json, .stdin, .env.json and .cwd suffixes. The scenario
// lives at <executable>.json. A test must not change the executable itself:
// the link shares it with every other test.
func Install(t testing.TB, name string, s Scenario) string {
	t.Helper()
	build.Do(func() {
		dir, err := os.MkdirTemp("", "duolog-testbin-build-")
		if err != nil {
			build.err = err
			return
		}
		defer os.RemoveAll(dir)
		_, file, _, _ := runtime.Caller(0)
		dest := filepath.Join(dir, "fake")
		if runtime.GOOS == "windows" {
			dest += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", dest, "./fake")
		cmd.Dir = filepath.Dir(file)
		cmd.Env = startEnv
		if output, err := cmd.CombinedOutput(); err != nil {
			build.err = &buildError{err, string(output)}
			return
		}
		build.binary, build.err = os.ReadFile(dest)
		if build.err == nil {
			build.shared = share(build.binary)
		}
	})
	if build.err != nil {
		t.Fatal(build.err)
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(dir, name)
	if build.shared == "" || os.Link(build.shared, dest) != nil {
		if err := os.WriteFile(dest, build.binary, 0755); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".json", data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dest
}

type buildError struct {
	err    error
	output string
}

func (e *buildError) Error() string { return "build fake agent: " + e.err.Error() + "\n" + e.output }
