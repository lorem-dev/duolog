// Package testbin installs compiled fake agents without shell wrappers.
package testbin

import (
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
type Scenario struct {
	// Responses selects a scenario by the longest matching argument prefix.
	Responses      map[string]Scenario
	Output         []OutputChunk
	Stdout, Stderr string
	Exit           int
	SleepMS        int
	Writes         map[string]string
}

var build struct {
	sync.Once
	binary []byte
	err    error
}

// Install builds fake once per test process and copies it into an isolated PATH
// entry. It returns the executable path; recordings use .args.json, .stdin,
// .env.json and .cwd suffixes. The scenario lives at <executable>.json.
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
		if output, err := cmd.CombinedOutput(); err != nil {
			build.err = &buildError{err, string(output)}
			return
		}
		build.binary, build.err = os.ReadFile(dest)
	})
	if build.err != nil {
		t.Fatal(build.err)
	}
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(dir, name)
	if err := os.WriteFile(dest, build.binary, 0755); err != nil {
		t.Fatal(err)
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
