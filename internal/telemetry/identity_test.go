package telemetry

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// testSalt is the salt of the fixed vectors: the bytes 0x00 to 0x1f.
var testSalt = func() (s [32]byte) {
	for i := range s {
		s[i] = byte(i)
	}
	return s
}()

var projectIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// referenceHMAC computes HMAC-SHA256 from its definition (RFC 2104), apart
// from crypto/hmac, for keys of at most one block.
func referenceHMAC(key, msg []byte) []byte {
	const block = 64
	ipad, opad := make([]byte, block), make([]byte, block)
	copy(ipad, key)
	copy(opad, key)
	for i := range block {
		ipad[i] ^= 0x36
		opad[i] ^= 0x5c
	}
	inner := sha256.Sum256(append(ipad, msg...))
	outer := sha256.Sum256(append(opad, inner[:]...))
	return outer[:]
}

func referenceID(salt [32]byte, root string) string {
	return hex.EncodeToString(referenceHMAC(salt[:], []byte("project-v1\x00"+root)))[:16]
}

func TestProjectIDVector(t *testing.T) {
	noOverride(t)

	// Calculated with openssl dgst -sha256 -mac HMAC over
	// "project-v1\0" + root, keyed with testSalt.
	vectors := []struct{ root, want string }{
		{"/work/example", "d58e08808d8cbaeb"},
		{"/work/moved", "aba24b085431fc22"},
	}
	for _, v := range vectors {
		if got := ProjectID(testSalt, v.root); got != v.want {
			t.Errorf("ProjectID(%q) = %s, want %s", v.root, got, v.want)
		}
		if ref := referenceID(testSalt, v.root); ref != v.want {
			t.Errorf("reference HMAC of %q = %s, want %s", v.root, ref, v.want)
		}
	}

	t.Run("16 lowercase hex", func(t *testing.T) {
		for _, root := range []string{"/work/example", "/work/Example", `C:\work\example`, "/work/ünï cødé", "/"} {
			id := ProjectID(testSalt, root)
			if !projectIDPattern.MatchString(id) {
				t.Errorf("ProjectID(%q) = %q, want 16 lowercase hex", root, id)
			}
			if id != referenceID(testSalt, root) {
				t.Errorf("ProjectID(%q) = %s, want %s", root, id, referenceID(testSalt, root))
			}
			if !fileName.MatchString("v1/" + id + "/2026-11-05.ndjson") {
				t.Errorf("id %s does not name a partition", id)
			}
		}
	})

	t.Run("same checkout and worktrees", func(t *testing.T) {
		// The adapter gives a checkout and each of its linked worktrees the
		// StateRoot of the main checkout.
		stateRoot := map[string]string{
			"/work/example":                    "/work/example",
			"/work/example/sub/dir":            "/work/example",
			"/work/example/.worktrees/feature": "/work/example",
			"/work/example-review":             "/work/example",
		}
		want := ProjectID(testSalt, "/work/example")
		for cwd, root := range stateRoot {
			if got := ProjectID(testSalt, root); got != want {
				t.Errorf("%s: id %s, want %s", cwd, got, want)
			}
		}

		// The salt read back from the store gives the same id every time.
		r := mustOpenProtected(t, filepath.Join(tempBase(t), "telemetry"))
		first, err := loadSalt(r)
		if err != nil {
			t.Fatal(err)
		}
		again, err := loadSalt(r)
		if err != nil {
			t.Fatal(err)
		}
		if first != again || ProjectID(first, "/work/example") != ProjectID(again, "/work/example") {
			t.Error("the stored salt changed between loads")
		}
	})

	t.Run("moved root or new salt", func(t *testing.T) {
		base := ProjectID(testSalt, "/work/example")
		if moved := ProjectID(testSalt, "/work/moved"); moved == base {
			t.Errorf("a moved root kept id %s", base)
		}
		other := testSalt
		other[31] ^= 1
		if ProjectID(other, "/work/example") == base {
			t.Errorf("a new salt kept id %s", base)
		}
		a, err := loadSalt(mustOpenProtected(t, filepath.Join(tempBase(t), "telemetry")))
		if err != nil {
			t.Fatal(err)
		}
		b, err := loadSalt(mustOpenProtected(t, filepath.Join(tempBase(t), "telemetry")))
		if err != nil {
			t.Fatal(err)
		}
		if a == b || ProjectID(a, "/work/example") == ProjectID(b, "/work/example") {
			t.Error("two stores share a salt or an id")
		}
	})

	t.Run("no id-to-path map", func(t *testing.T) {
		root := filepath.Join(tempBase(t), "telemetry")
		r := mustOpenProtected(t, root)
		project := filepath.Join(tempBase(t), "work", "example")
		salt, err := loadSalt(r)
		if err != nil {
			t.Fatal(err)
		}
		id := ProjectID(salt, project)
		var names []string
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || p == root {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			names = append(names, filepath.ToSlash(rel))
			if d.IsDir() {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte(project)) || bytes.Contains(data, []byte(id)) {
				t.Errorf("%s holds the project path or id", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			if name != "salt" && name != "CACHEDIR.TAG" {
				t.Errorf("unexpected entry %s", name)
			}
		}
		assertTight(t, filepath.Join(root, "salt"), false)
	})
}

const saltTestEnv = "DUOLOG_SALT_TEST_DIR"

// saltChild runs in a test-binary process: it loads the salt of
// <dir>/telemetry and prints the project id or the error. The O_EXCL winner
// marks itself and waits for <dir>/control/release before it writes; a loser
// marks itself when it first sees a short salt.
func saltChild(t *testing.T, dir string) {
	wait, err := time.ParseDuration(os.Getenv("DUOLOG_SALT_TEST_WAIT"))
	if err != nil {
		t.Fatal(err)
	}
	saltWait = wait
	control := filepath.Join(dir, "control")
	mark := func(name string) {
		if err := os.WriteFile(filepath.Join(control, fmt.Sprintf("%s-%d", name, os.Getpid())), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setRaceHook(t, func(stage string) {
		switch stage {
		case "salt":
			mark("won")
			deadline := time.Now().Add(time.Minute)
			for {
				if _, err := os.Lstat(filepath.Join(control, "release")); err == nil {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("never released")
				}
				time.Sleep(5 * time.Millisecond)
			}
		case "salt short":
			mark("waiting")
		}
	})
	r, _, err := openProtected(filepath.Join(dir, "telemetry"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	salt, err := loadSalt(r)
	switch {
	case errors.Is(err, errSaltLength):
		fmt.Println("result length")
	case err != nil:
		fmt.Println("result error", err)
	default:
		fmt.Println("result id", ProjectID(salt, "/work/example"), hex.EncodeToString(salt[:]))
	}
}

type saltProcess struct {
	cmd *exec.Cmd
	out *bytes.Buffer
}

func startSaltProcesses(t *testing.T, dir string, n int, wait time.Duration) []saltProcess {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	procs := make([]saltProcess, n)
	for i := range procs {
		cmd := exec.Command(exe, "-test.run=^TestSaltConcurrentCreation$", "-test.timeout=120s")
		cmd.Env = append(os.Environ(), saltTestEnv+"="+dir, "DUOLOG_SALT_TEST_WAIT="+wait.String())
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		procs[i] = saltProcess{cmd: cmd, out: out}
	}
	return procs
}

// result waits for the process and returns the words of its result line.
func (p saltProcess) result(t *testing.T) []string {
	t.Helper()
	if err := p.cmd.Wait(); err != nil {
		t.Fatalf("child: %v\n%s", err, p.out)
	}
	sc := bufio.NewScanner(bytes.NewReader(p.out.Bytes()))
	for sc.Scan() {
		if words := strings.Fields(sc.Text()); len(words) > 1 && words[0] == "result" {
			return words[1:]
		}
	}
	t.Fatalf("no result:\n%s", p.out)
	return nil
}

// markers counts the marker files with the given prefix.
func markers(t *testing.T, control, prefix string) int {
	t.Helper()
	entries, err := os.ReadDir(control)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix+"-") {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// saltSetup makes the telemetry root and the control folder of one run.
func saltSetup(t *testing.T) (dir, salt, control string) {
	t.Helper()
	dir = tempBase(t)
	mustOpenProtected(t, filepath.Join(dir, "telemetry"))
	control = filepath.Join(dir, "control")
	if err := os.Mkdir(control, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "telemetry", "salt"), control
}

// pausedSalt waits until the winner holds an empty salt and returns it.
func pausedSalt(t *testing.T, salt, control string) os.FileInfo {
	t.Helper()
	waitFor(t, "the winner", func() bool { return markers(t, control, "won") > 0 })
	info, err := os.Lstat(salt)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("salt has %d bytes before the winner wrote", info.Size())
	}
	return info
}

func release(t *testing.T, control string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(control, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// sameSalt checks that the salt is still the file the winner created, with
// 32 bytes and mode 0600, and returns its bytes.
func sameSalt(t *testing.T, salt string, created os.FileInfo) []byte {
	t.Helper()
	info, err := os.Lstat(salt)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, created) {
		t.Fatal("the winner's salt was replaced")
	}
	data, err := os.ReadFile(salt)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 32 {
		t.Fatalf("salt has %d bytes, want 32", len(data))
	}
	assertTight(t, salt, false)
	return data
}

func TestSaltConcurrentCreation(t *testing.T) {
	if dir := os.Getenv(saltTestEnv); dir != "" {
		saltChild(t, dir)
		return
	}
	noOverride(t)
	const n = 6

	t.Run("losers wait for the winner", func(t *testing.T) {
		dir, salt, control := saltSetup(t)
		procs := startSaltProcesses(t, dir, n, time.Minute)
		created := pausedSalt(t, salt, control)
		waitFor(t, "the losers", func() bool { return markers(t, control, "waiting") == n-1 })
		if markers(t, control, "won") != 1 {
			t.Fatalf("%d winners", markers(t, control, "won"))
		}
		// The losers retry the short salt; none uses, rewrites or deletes it.
		if info, err := os.Lstat(salt); err != nil || info.Size() != 0 || !os.SameFile(info, created) {
			t.Fatalf("losers touched the winner's salt: %v", err)
		}
		release(t, control)
		var ids []string
		for _, p := range procs {
			words := p.result(t)
			if words[0] != "id" {
				t.Fatalf("child result %v, want an id", words)
			}
			ids = append(ids, words[1])
			data := sameSalt(t, salt, created)
			if words[2] != hex.EncodeToString(data) {
				t.Errorf("child used salt %s, the file holds %x", words[2], data)
			}
		}
		data := sameSalt(t, salt, created)
		want := referenceID([32]byte(data), "/work/example")
		for _, id := range ids {
			if id != want {
				t.Errorf("ids %v, want all %s", ids, want)
				break
			}
		}
	})

	t.Run("losers give up on a stalled winner", func(t *testing.T) {
		dir, salt, control := saltSetup(t)
		procs := startSaltProcesses(t, dir, n, 200*time.Millisecond)
		created := pausedSalt(t, salt, control)
		var winner saltProcess
		gaveUp := 0
		for _, p := range procs {
			if _, err := os.Lstat(filepath.Join(control, fmt.Sprintf("won-%d", p.cmd.Process.Pid))); err == nil {
				winner = p
				continue
			}
			if words := p.result(t); words[0] != "length" {
				t.Errorf("loser result %v, want a length failure", words)
			}
			gaveUp++
		}
		if gaveUp != n-1 || winner.cmd == nil {
			t.Fatalf("%d losers gave up, winner found %v", gaveUp, winner.cmd != nil)
		}
		if info, err := os.Lstat(salt); err != nil || info.Size() != 0 || !os.SameFile(info, created) {
			t.Fatalf("failed losers touched the winner's salt: %v", err)
		}
		release(t, control)
		words := winner.result(t)
		if words[0] != "id" {
			t.Fatalf("winner result %v, want an id", words)
		}
		data := sameSalt(t, salt, created)
		r := mustOpenProtected(t, filepath.Join(dir, "telemetry"))
		got, err := loadSalt(r)
		if err != nil || got != [32]byte(data) || ProjectID(got, "/work/example") != words[1] {
			t.Fatalf("after the winner: %x, %v; want the winner's salt", got, err)
		}
	})

	t.Run("wrong length fails without rotating", func(t *testing.T) {
		old := saltWait
		saltWait = 50 * time.Millisecond
		t.Cleanup(func() { saltWait = old })
		for _, size := range []int{0, 1, 31, 33, 64} {
			root := filepath.Join(tempBase(t), "telemetry")
			r := mustOpenProtected(t, root)
			salt := filepath.Join(root, "salt")
			content := bytes.Repeat([]byte{0xa5}, size)
			if err := os.WriteFile(salt, content, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(salt)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if got, err := loadSalt(r); !errors.Is(err, errSaltLength) || got != [32]byte{} {
					t.Fatalf("%d bytes: got %x, %v; want errSaltLength", size, got, err)
				} else {
					noPath(t, err, root)
				}
			}
			after, err := os.Lstat(salt)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("%d bytes: the salt was replaced: %v", size, err)
			}
			if data, _ := os.ReadFile(salt); !bytes.Equal(data, content) {
				t.Errorf("%d bytes: the salt was rewritten to %x", size, data)
			}
		}
	})
}
