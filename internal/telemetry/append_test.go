package telemetry

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

const (
	appendTestEnv  = "DUOLOG_APPEND_TEST_DIR"
	appendChildEnv = "DUOLOG_APPEND_TEST_CHILD"
	testStateRoot  = "/work/example"
	testDay        = "2026-11-05"
)

// appendErrors is the closed set of errors Append returns.
var appendErrors = []error{errRefused, errUnsafeStore, errDenied, errWrite, errClosed}

// storeOptions gives every process of a test the same store under base.
func storeOptions(base string) Options {
	return Options{
		Location: Location{GOOS: runtime.GOOS, Home: base,
			XDGStateHome: filepath.Join(base, "state"), LocalAppData: filepath.Join(base, "local")},
		Names:              testNames,
		CanonicalStateRoot: testStateRoot,
		Enabled:            true,
		ProjectEnabled:     true,
		RetentionDays:      30,
		PruneEveryHours:    6,
		MaxMegabytes:       16,
		Now:                func() time.Time { return testNow },
	}
}

func storeRoot(t *testing.T, base string) string {
	t.Helper()
	root, err := ResolveRoot(storeOptions(base).Location)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func mustOpenStore(t *testing.T, o Options) *Store {
	t.Helper()
	s, err := Open(o)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustAppend(t *testing.T, s *Store, e Event) {
	t.Helper()
	if err := s.Append(e); err != nil {
		t.Fatalf("Append(%T): %v", e, err)
	}
}

// projectSegment is the path of the project partition's segment for day,
// with the id computed from the salt on disk.
func projectSegment(t *testing.T, root, day string) string {
	t.Helper()
	salt, err := os.ReadFile(filepath.Join(root, "salt"))
	if err != nil || len(salt) != 32 {
		t.Fatalf("salt: %d bytes, %v", len(salt), err)
	}
	return filepath.Join(root, "v1", referenceID([32]byte(salt), testStateRoot), day+".ndjson")
}

func accountSegment(root, day string) string {
	return filepath.Join(root, "v1", "_account", day+".ndjson")
}

// segmentLines returns the lines of a segment, failing on a line that is
// not terminated or not JSON.
func segmentLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		t.Fatalf("%s ends in a partial line: %q", filepath.Base(path), tail(data))
	}
	var out []map[string]any
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		if len(line) > 0 {
			out = append(out, decodeLine(t, line))
		}
	}
	return out
}

func tail(data []byte) []byte {
	if len(data) > 80 {
		return data[len(data)-80:]
	}
	return data
}

func types(lines []map[string]any) []string {
	var out []string
	for _, m := range lines {
		out = append(out, m["type"].(string))
	}
	return out
}

func healthDelta(before, after Health) (dropped, failures uint64) {
	return after.Dropped - before.Dropped, after.Failures - before.Failures
}

func setWriteFile(t *testing.T, fn func(f *os.File, b []byte) (int, error)) {
	t.Helper()
	old := writeFile
	writeFile = fn
	t.Cleanup(func() { writeFile = old })
}

func setCloseFile(t *testing.T, fn func(f *os.File) error) {
	t.Helper()
	old := closeFile
	closeFile = fn
	t.Cleanup(func() { closeFile = old })
}

// appendChild runs in a test-binary process. "writer <n>" appends 50 run and
// 50 quota events from each of 4 goroutines; "killed" writes half a line,
// marks itself and waits to be killed.
func appendChild(t *testing.T, base string) {
	words := strings.Fields(os.Getenv(appendChildEnv))
	s, err := Open(storeOptions(base))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	switch words[0] {
	case "writer":
		child, err := strconv.Atoi(words[1])
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for g := range 4 {
			wg.Go(func() {
				for i := range 50 {
					n := g*50 + i
					r := sampleRun()
					r.Attempt, r.Asks = n+1, int64(child)
					q := sampleQuota()
					q.Epoch = int64(child*1000 + n)
					if err := s.Append(r); err != nil {
						t.Error(err)
					}
					if err := s.Append(q); err != nil {
						t.Error(err)
					}
				}
			})
		}
		wg.Wait()
		h := s.Health()
		fmt.Println("result health", h.Dropped, h.Failures)
	case "killed":
		writeFile = func(f *os.File, b []byte) (int, error) {
			n, err := f.Write(b[:len(b)/2])
			if err != nil {
				return n, err
			}
			if err := os.WriteFile(filepath.Join(base, "half-written"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Minute)
			return n, nil
		}
		s.Append(sampleRun())
		t.Fatal("not killed")
	}
}

func startAppendChild(t *testing.T, test, base, mode string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^"+test+"$", "-test.timeout=120s")
	cmd.Env = append(os.Environ(), appendTestEnv+"="+base, appendChildEnv+"="+mode)
	out := &bytes.Buffer{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd, out
}

func TestAppendWholeLines(t *testing.T) {
	if base := os.Getenv(appendTestEnv); base != "" {
		appendChild(t, base)
		return
	}
	noOverride(t)
	const children, perChild = 6, 200
	base := tempBase(t)
	// The parent makes the salt so every child shares the project id.
	mustOpenStore(t, storeOptions(base)).Close()

	type child struct {
		cmd *exec.Cmd
		out *bytes.Buffer
	}
	var procs []child
	for i := range children {
		cmd, out := startAppendChild(t, "TestAppendWholeLines", base, fmt.Sprintf("writer %d", i))
		procs = append(procs, child{cmd, out})
	}
	for _, p := range procs {
		if err := p.cmd.Wait(); err != nil {
			t.Fatalf("child: %v\n%s", err, p.out)
		}
		sc := bufio.NewScanner(bytes.NewReader(p.out.Bytes()))
		found := false
		for sc.Scan() {
			if words := strings.Fields(sc.Text()); len(words) == 4 && words[0] == "result" {
				found = true
				if words[2] != "0" || words[3] != "0" {
					t.Errorf("child health dropped %s failures %s, want 0 0", words[2], words[3])
				}
			}
		}
		if !found {
			t.Fatalf("no result:\n%s", p.out)
		}
	}

	root := storeRoot(t, base)
	runs := segmentLines(t, projectSegment(t, root, testDay))
	quotas := segmentLines(t, accountSegment(root, testDay))
	if len(runs) != children*perChild || len(quotas) != children*perChild {
		t.Fatalf("%d run and %d quota lines, want %d each", len(runs), len(quotas), children*perChild)
	}
	want := decodeLine(t, mustEncode(t, sampleRun(), testNames))
	seen := map[string]bool{}
	for _, m := range runs {
		key := fmt.Sprintf("%v/%v", m["asks"], m["attempt"])
		if seen[key] {
			t.Errorf("run %s written twice", key)
		}
		seen[key] = true
		m["asks"], m["attempt"] = want["asks"], want["attempt"]
		if !reflect.DeepEqual(m, want) {
			t.Fatalf("run line %v, want %v", m, want)
		}
	}
	epochs := map[float64]bool{}
	for _, m := range quotas {
		if m["type"] != "quota" || epochs[m["epoch"].(float64)] {
			t.Fatalf("bad or repeated quota line %v", m)
		}
		epochs[m["epoch"].(float64)] = true
	}
}

func TestAppendRepairsTail(t *testing.T) {
	if base := os.Getenv(appendTestEnv); base != "" {
		appendChild(t, base)
		return
	}
	noOverride(t)
	old := mustEncode(t, sampleRun(), testNames)
	partial := []byte(`{"v":1,"t":1793880000,"type":"run","role":"rev`)
	complete := mustEncode(t, samplePeer(), testNames)
	complete = complete[:len(complete)-1]

	cases := []struct {
		name    string
		content []byte
		want    []byte
	}{
		{"truncated JSON", slices.Concat(old, partial), old},
		{"complete JSON without newline", slices.Concat(old, complete), old},
		{"only a partial line", partial, nil},
		{"whole lines", slices.Concat(old, old), slices.Concat(old, old)},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := tempBase(t)
			s := mustOpenStore(t, storeOptions(base))
			root := storeRoot(t, base)
			seg := projectSegment(t, root, testDay)
			if err := os.MkdirAll(filepath.Dir(seg), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(seg, c.content, 0o600); err != nil {
				t.Fatal(err)
			}
			mustAppend(t, s, sampleRun())
			got, err := os.ReadFile(seg)
			if err != nil {
				t.Fatal(err)
			}
			if want := slices.Concat(c.want, old); !bytes.Equal(got, want) {
				t.Errorf("segment\n%q\nwant\n%q", got, want)
			}
			segmentLines(t, seg)
		})
	}

	t.Run("killed writer", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleRun())
		mustAppend(t, s, sampleGate())
		root := storeRoot(t, base)
		seg := projectSegment(t, root, testDay)
		before, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}

		cmd, out := startAppendChild(t, "TestAppendRepairsTail", base, "killed")
		waitFor(t, "the half-written line", func() bool {
			_, err := os.Lstat(filepath.Join(base, "half-written"))
			return err == nil
		})
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		cmd.Wait()
		killed, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		if len(killed) <= len(before) || killed[len(killed)-1] == '\n' {
			t.Fatalf("the child left no partial line:\n%q\n%s", killed, out)
		}

		// The dead writer's lock is gone; the next append repairs its tail.
		mustAppend(t, s, samplePeer())
		got, err := os.ReadFile(seg)
		if err != nil {
			t.Fatal(err)
		}
		if want := slices.Concat(before, mustEncode(t, samplePeer(), testNames)); !bytes.Equal(got, want) {
			t.Errorf("segment\n%q\nwant\n%q", got, want)
		}
		if got := types(segmentLines(t, seg)); !slices.Equal(got, []string{"run", "gate", "peer"}) {
			t.Errorf("types %v", got)
		}
	})
}

func TestAppendPartition(t *testing.T) {
	noOverride(t)

	t.Run("project and account events", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		before := s.Health()
		for _, e := range sampleEvents() {
			mustAppend(t, s, e)
		}
		// Pointers go to the same partitions.
		mustAppend(t, s, new(sampleGate()))
		mustAppend(t, s, new(sampleReset()))
		root := storeRoot(t, base)
		if got := types(segmentLines(t, projectSegment(t, root, testDay))); !slices.Equal(got, []string{"run", "gate", "peer", "gate"}) {
			t.Errorf("project partition %v, want run, gate, peer, gate", got)
		}
		if got := types(segmentLines(t, accountSegment(root, testDay))); !slices.Equal(got, []string{"quota", "route", "reset", "reset"}) {
			t.Errorf("account partition %v, want quota, route, reset, reset", got)
		}
		for _, dir := range []string{filepath.Dir(projectSegment(t, root, testDay)), filepath.Dir(accountSegment(root, testDay))} {
			if info, err := os.Lstat(filepath.Join(dir, "telemetry.lock")); err != nil || !info.Mode().IsRegular() {
				t.Errorf("%s: no lock file: %v", filepath.Base(dir), err)
			}
			assertTight(t, dir, true)
		}
		assertTight(t, projectSegment(t, root, testDay), false)
		assertTight(t, accountSegment(root, testDay), false)
		if d, f := healthDelta(before, s.Health()); d != 0 || f != 0 {
			t.Errorf("health moved by %d dropped, %d failures", d, f)
		}
	})

	t.Run("UTC write day", func(t *testing.T) {
		london, err := time.LoadLocation("Europe/London")
		if err != nil {
			t.Skipf("no zone data: %v", err)
		}
		base := tempBase(t)
		o := storeOptions(base)
		// 00:30 in London in summer is still the previous day in UTC.
		now := time.Date(2026, 7, 1, 0, 30, 0, 0, london)
		o.Now = func() time.Time { return now }
		s := mustOpenStore(t, o)
		mustAppend(t, s, sampleRun())
		mustAppend(t, s, sampleQuota())
		now = now.Add(time.Hour)
		mustAppend(t, s, sampleRun())
		root := storeRoot(t, base)
		first := segmentLines(t, projectSegment(t, root, "2026-06-30"))
		if len(first) != 1 || first[0]["t"] != float64(time.Date(2026, 6, 30, 23, 30, 0, 0, time.UTC).Unix()) {
			t.Errorf("2026-06-30 holds %v", first)
		}
		if got := segmentLines(t, accountSegment(root, "2026-06-30")); len(got) != 1 {
			t.Errorf("account 2026-06-30 holds %d lines", len(got))
		}
		if got := segmentLines(t, projectSegment(t, root, "2026-07-01")); len(got) != 1 {
			t.Errorf("2026-07-01 holds %d lines", len(got))
		}
		if _, err := os.Lstat(accountSegment(root, "2026-07-01")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("an account segment for 2026-07-01: %v", err)
		}
	})

	t.Run("per-partition locks", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleRun())
		mustAppend(t, s, sampleQuota())
		root := storeRoot(t, base)
		projectLock := filepath.Join(filepath.Dir(projectSegment(t, root, testDay)), "telemetry.lock")
		accountLock := filepath.Join(root, "v1", "_account", "telemetry.lock")
		try := func(path string) bool {
			l := flock.New(path, flock.SetFlag(os.O_RDWR))
			defer l.Close()
			ok, err := l.TryLock()
			if err != nil {
				t.Fatal(err)
			}
			return ok
		}
		calls := 0
		setRaceHook(t, func(stage string) {
			if stage != "append" {
				return
			}
			calls++
			if try(projectLock) {
				t.Error("the project lock was free during a project append")
			}
			if !try(accountLock) {
				t.Error("a project append held the account lock")
			}
		})
		mustAppend(t, s, sampleRun())
		if calls != 1 {
			t.Fatalf("append hook ran %d times", calls)
		}
		setRaceHook(t, nil)
		// Between appends no lock is held.
		if !try(projectLock) || !try(accountLock) {
			t.Error("a lock outlived its append")
		}
	})

	t.Run("project off permits account", func(t *testing.T) {
		base := tempBase(t)
		o := storeOptions(base)
		o.ProjectEnabled = false
		s := mustOpenStore(t, o)
		before := s.Health()
		for _, e := range sampleEvents() {
			if err := s.Append(e); err != nil {
				t.Fatalf("Append(%T): %v", e, err)
			}
		}
		root := storeRoot(t, base)
		if got := types(segmentLines(t, accountSegment(root, testDay))); !slices.Equal(got, []string{"quota", "route", "reset"}) {
			t.Errorf("account partition %v", got)
		}
		entries, err := os.ReadDir(filepath.Join(root, "v1"))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Name() != "_account" {
				t.Errorf("project partition %s written with the project off", e.Name())
			}
		}
		if d, f := healthDelta(before, s.Health()); d != 0 || f != 0 {
			t.Errorf("an off project counted %d dropped, %d failures", d, f)
		}
	})

	off := func(t *testing.T, base string, o Options) {
		t.Helper()
		s := mustOpenStore(t, o)
		before := s.Health()
		for _, e := range sampleEvents() {
			if err := s.Append(e); err != nil {
				t.Fatalf("Append(%T): %v", e, err)
			}
		}
		if d, f := healthDelta(before, s.Health()); d != 0 || f != 0 {
			t.Errorf("off counted %d dropped, %d failures", d, f)
		}
		// Neither the root nor any directory above it was created.
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("off created %v", entries[0].Name())
		}
	}

	t.Run("user off permits neither", func(t *testing.T) {
		base := tempBase(t)
		o := storeOptions(base)
		o.Enabled, o.ProjectEnabled = false, false
		off(t, base, o)
		// A project setting cannot turn it back on.
		o.ProjectEnabled = true
		off(t, base, o)
	})

	t.Run("env off permits neither", func(t *testing.T) {
		for _, value := range []string{"off", "OFF", " off "} {
			t.Setenv("DUOLOG_TELEMETRY", value)
			base := tempBase(t)
			off(t, base, storeOptions(base))
		}
		// No value turns it on.
		t.Setenv("DUOLOG_TELEMETRY", "on")
		base := tempBase(t)
		o := storeOptions(base)
		o.Enabled = false
		off(t, base, o)
	})

	t.Run("off keeps existing data for maintenance", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleRun())
		s.Close()
		root := storeRoot(t, base)
		seg := projectSegment(t, root, testDay)
		saltBefore, _ := os.ReadFile(filepath.Join(root, "salt"))
		segBefore, _ := os.ReadFile(seg)

		o := storeOptions(base)
		o.Enabled = false
		disabled := mustOpenStore(t, o)
		mustAppend(t, disabled, sampleRun())
		mustAppend(t, disabled, sampleQuota())
		if got, _ := os.ReadFile(filepath.Join(root, "salt")); !bytes.Equal(got, saltBefore) {
			t.Error("off changed the salt")
		}
		if got, _ := os.ReadFile(seg); !bytes.Equal(got, segBefore) {
			t.Error("off wrote a project event")
		}
		if _, err := os.Lstat(accountSegment(root, testDay)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("off wrote an account event: %v", err)
		}
	})
}

func TestAppendFailures(t *testing.T) {
	noOverride(t)

	// check runs one append that must fail: the error is one closed
	// category without a path, both counters move by one, the caller's event
	// is unchanged and the segment keeps only whole lines. A good append
	// afterwards moves no counter.
	check := func(t *testing.T, s *Store, base string, e Event, want error) {
		t.Helper()
		before := s.Health()
		var copied any
		if e != nil {
			v := reflect.New(reflect.TypeOf(e)).Elem()
			v.Set(reflect.ValueOf(e))
			copied = v.Interface()
		}
		err := s.Append(e)
		if !slices.Contains(appendErrors, err) {
			t.Fatalf("Append returned %v, want one of the closed errors", err)
		}
		if err != want {
			t.Errorf("Append returned %v, want %v", err, want)
		}
		noPath(t, err, base)
		if d, f := healthDelta(before, s.Health()); d != 1 || f != 1 {
			t.Errorf("health moved by %d dropped, %d failures; want 1 and 1", d, f)
		}
		if e != nil && !reflect.DeepEqual(e, copied) {
			t.Error("Append changed the caller's event")
		}
	}
	recovers := func(t *testing.T, s *Store, root string) {
		t.Helper()
		before := s.Health()
		mustAppend(t, s, sampleRun())
		if d, f := healthDelta(before, s.Health()); d != 0 || f != 0 {
			t.Errorf("a good append moved health by %d, %d", d, f)
		}
		segmentLines(t, projectSegment(t, root, testDay))
	}

	t.Run("short write", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleRun())
		root := storeRoot(t, base)
		setWriteFile(t, func(f *os.File, b []byte) (int, error) { return f.Write(b[:len(b)/2]) })
		check(t, s, base, sampleRun(), errWrite)
		// The half line was cut back at once.
		if got := segmentLines(t, projectSegment(t, root, testDay)); len(got) != 1 {
			t.Errorf("%d lines after a short write, want 1", len(got))
		}
		setWriteFile(t, (*os.File).Write)
		recovers(t, s, root)
	})

	t.Run("write error", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		root := storeRoot(t, base)
		setWriteFile(t, func(f *os.File, b []byte) (int, error) {
			n, _ := f.Write(b[:10])
			return n, errors.New("disk full at " + base)
		})
		check(t, s, base, sampleQuota(), errWrite)
		if data, err := os.ReadFile(accountSegment(root, testDay)); err != nil || len(data) != 0 {
			t.Errorf("account segment %q, %v; want empty", data, err)
		}
		setWriteFile(t, (*os.File).Write)
		recovers(t, s, root)
	})

	t.Run("close", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		root := storeRoot(t, base)
		setCloseFile(t, func(f *os.File) error {
			f.Close()
			return &fs.PathError{Op: "close", Path: filepath.Join(base, "x"), Err: errors.New("io error")}
		})
		check(t, s, base, sampleRun(), errWrite)
		setCloseFile(t, (*os.File).Close)
		recovers(t, s, root)
	})

	t.Run("permission", func(t *testing.T) {
		if runtime.GOOS != "windows" && os.Geteuid() == 0 {
			t.Skip("root ignores file modes")
		}
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleRun())
		root := storeRoot(t, base)
		seg := projectSegment(t, root, testDay)
		if err := os.Chmod(seg, 0o400); err != nil {
			t.Fatal(err)
		}
		check(t, s, base, sampleRun(), errDenied)
		if err := os.Chmod(seg, 0o600); err != nil {
			t.Fatal(err)
		}
		recovers(t, s, root)
	})

	t.Run("validation", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		root := storeRoot(t, base)
		bad := sampleRun()
		bad.Attempt = 0
		check(t, s, base, bad, errRefused)
		cached := sampleRun()
		cached.Cached = cached.In + 1
		check(t, s, base, &cached, errRefused)
		check(t, s, base, nil, errRefused)
		check(t, s, base, (*Quota)(nil), errRefused)
		q := sampleQuota()
		q.Windows[0].UsedBP = maxBasisPoints + 1
		check(t, s, base, q, errRefused)
		late := storeOptions(base)
		late.Now = func() time.Time { return time.Unix(-1, 0) }
		check(t, mustOpenStore(t, late), base, sampleReset(), errRefused)
		for _, name := range []string{projectSegment(t, root, testDay), accountSegment(root, testDay)} {
			if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s written for refused events: %v", filepath.Base(name), err)
			}
		}
		recovers(t, s, root)
	})

	t.Run("closed store", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		check(t, s, base, sampleRun(), errClosed)
		if err := s.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})

	t.Run("planted hard link", func(t *testing.T) {
		for _, name := range []string{testDay + ".ndjson", "telemetry.lock"} {
			base := tempBase(t)
			s := mustOpenStore(t, storeOptions(base))
			root := storeRoot(t, base)
			dir := filepath.Dir(projectSegment(t, root, testDay))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(base, "outside")
			content := []byte("outside bytes\n")
			if err := os.WriteFile(outside, content, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(outside, filepath.Join(dir, name)); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			check(t, s, base, sampleRun(), errUnsafeStore)
			info, err := os.Stat(outside)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(outside); !bytes.Equal(got, content) || info.Mode().Perm() != 0o640 && runtime.GOOS != "windows" {
				t.Errorf("%s: the outside file changed to %q, %v", name, got, info.Mode())
			}
		}
	})

	t.Run("swapped root", func(t *testing.T) {
		base := tempBase(t)
		s := mustOpenStore(t, storeOptions(base))
		mustAppend(t, s, sampleQuota())
		root := storeRoot(t, base)
		if err := os.Rename(root, root+".old"); err != nil {
			// Windows refuses to move a directory the store holds open.
			if runtime.GOOS == "windows" {
				t.Skipf("cannot move the open root: %v", err)
			}
			t.Fatal(err)
		}
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		check(t, s, base, sampleQuota(), errUnsafeStore)
		if got := segmentLines(t, accountSegment(root+".old", testDay)); len(got) != 1 {
			t.Errorf("the moved root got %d lines, want the first only", len(got))
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Errorf("the new root holds %d entries, %v", len(entries), err)
		}

		// A symlink in place of the root is refused too.
		if err := os.Remove(root); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, root+".old", root)
		check(t, s, base, sampleQuota(), errUnsafeStore)
		if got := segmentLines(t, accountSegment(root+".old", testDay)); len(got) != 1 {
			t.Errorf("the link target got %d lines, want 1", len(got))
		}
	})
}
