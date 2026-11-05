//go:build linux

package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// fakeNFS makes every file system look like NFS.
func fakeNFS(t *testing.T) {
	t.Helper()
	real := statfs
	statfs = func(path string, buf *unix.Statfs_t) error {
		if err := real(path, buf); err != nil {
			return err
		}
		buf.Type = unix.NFS_SUPER_MAGIC
		return nil
	}
	t.Cleanup(func() { statfs = real })
}

func TestRootCacheDirTag(t *testing.T) {
	noOverride(t)
	root := filepath.Join(tempBase(t), "telemetry")
	r, p, err := openProtected(root)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if !p.BackupVerified || p.IndexVerified {
		t.Errorf("protection %+v, want backup verified only", p)
	}
	tag := filepath.Join(root, "CACHEDIR.TAG")
	got, err := os.ReadFile(tag)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "Signature: 8a477f597d28d172789f06886806bc55") {
		t.Errorf("tag %q has no signature", got)
	}
	assertTight(t, tag, false)

	// A broken tag is rewritten.
	if err := os.WriteFile(tag, []byte("Signature: wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := excludeBackup(root); err != nil || !ok {
		t.Fatalf("excludeBackup: %v, %v", ok, err)
	}
	if again, _ := os.ReadFile(tag); string(again) != string(got) {
		t.Errorf("tag not repaired: %q", again)
	}
}

// nfsDirEnv names a folder on a real NFS mount. CI mounts one on Linux; a
// local run without it skips the test.
const nfsDirEnv = "DUOLOG_TEST_NFS_DIR"

// A root on a real NFS mount is refused through the real statfs, with no
// fake: this is the native evidence for the NFS probe.
func TestRootRefusalRealNFS(t *testing.T) {
	base := os.Getenv(nfsDirEnv)
	if base == "" {
		t.Skip(nfsDirEnv + " names no NFS mount")
	}
	noOverride(t)
	var buf unix.Statfs_t
	if err := unix.Statfs(base, &buf); err != nil || buf.Type != unix.NFS_SUPER_MAGIC {
		t.Fatalf("%s is not on NFS: type %#x, %v", nfsDirEnv, buf.Type, err)
	}
	if err := refuseMount(base); err == nil {
		t.Fatal("a real NFS mount was accepted")
	}
	root := filepath.Join(base, t.Name(), "duolog", "telemetry")
	r, p, err := tryOpenProtected(t, root)
	if err == nil || r != nil || p.Refusal != refusalNFS {
		t.Fatalf("got %+v, %v; want nfs", p, err)
	}
	if _, err := os.Lstat(root); err == nil {
		t.Error("a root on NFS was created")
	}
}

func TestRootRefusalNFS(t *testing.T) {
	noOverride(t)
	base := tempBase(t)
	if err := refuseMount(base); err != nil {
		t.Fatalf("local file system refused: %v", err)
	}

	fakeNFS(t)
	if err := refuseMount(base); err == nil {
		t.Fatal("NFS accepted")
	}
	root := filepath.Join(base, "duolog", "telemetry")
	r, p, err := tryOpenProtected(t, root)
	if err == nil || r != nil || p.Refusal != refusalNFS {
		t.Fatalf("got %+v, %v; want nfs", p, err)
	}
	noPath(t, err, base)
	if _, err := os.Lstat(root); err == nil {
		t.Error("a root on NFS was created")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if p, err := checkRoot(root); err == nil || p.Refusal != refusalNFS {
		t.Fatalf("checkRoot: got %+v, %v; want nfs", p, err)
	}
}
