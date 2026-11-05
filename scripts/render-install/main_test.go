package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstaller(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX installer")
	}
	for _, scenario := range []string{"good", "tampered", "missing", "checksum-download", "pinned", "pinned-bare", "bad-version", "unsupported", "windows", "via-go", "update", "env", "dir", "tampered-existing", "wget", "no-fetcher"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "install.sh")
			if err := renderTemplate("../install-template/install.sh.tmpl", script, defaultData()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(script)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(data, []byte("#!/bin/sh\n")) || bytes.Contains(data, []byte("{{")) {
				t.Fatal("bad rendering")
			}
			check := exec.Command("sh", "-n", script)
			if out, err := check.CombinedOutput(); err != nil {
				t.Fatalf("syntax: %v %s", err, out)
			}
			bin := filepath.Join(dir, "fake-bin")
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			write := func(path, content string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), 0755); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bin, "uname"), `#!/bin/sh
case "$1" in
 -s) printf '%s\n' "$FAKE_OS" ;;
 -m) printf '%s\n' "$FAKE_ARCH" ;;
esac
`)
			write(filepath.Join(bin, "curl"), `#!/bin/sh
printf '%s\n' "$*" >> "$FIXTURE/urls"
case "$1" in
 -fsSLI) printf 'https://github.com/lorem-dev/duolog/releases/tag/v1.2.3'; exit 0 ;;
esac
[ "$1" = -fsSL ] && [ "$2" = -o ] || exit 9
case "$4" in
 */checksums.txt) [ "$FAIL_CHECKSUM" = 0 ] || exit 22; cp "$FIXTURE/checksums.txt" "$3" ;;
 */duolog_1.2.3_linux_amd64.tar.gz) cp "$FIXTURE/archive.tar.gz" "$3" ;;
 *) exit 22 ;;
esac
`)
			// Alpine has busybox wget and often no curl. Give the script a PATH
			// with the tools it needs and no curl.
			noCurl := scenario == "wget" || scenario == "no-fetcher"
			if noCurl {
				if err := os.Remove(filepath.Join(bin, "curl")); err != nil {
					t.Fatal(err)
				}
				tools := filepath.Join(dir, "tools")
				if err := os.Mkdir(tools, 0755); err != nil {
					t.Fatal(err)
				}
				for _, tool := range []string{"tr", "tar", "mktemp", "awk", "chmod", "mv", "cp", "rm", "mkdir", "sed", "head", "cat", "sha256sum", "shasum", "gzip", "dirname", "basename"} {
					if found, err := exec.LookPath(tool); err == nil {
						if err := os.Symlink(found, filepath.Join(tools, tool)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if scenario == "wget" {
				write(filepath.Join(bin, "wget"), `#!/bin/sh
printf '%s\n' "$*" >> "$FIXTURE/urls"
[ "$1" = -q ] && [ "$2" = -O ] || exit 9
case "$4" in
 https://api.github.com/repos/lorem-dev/duolog/releases/latest) printf '{\n  "tag_name": "v1.2.3",\n  "name": "v1.2.3"\n}\n' ;;
 */checksums.txt) cp "$FIXTURE/checksums.txt" "$3" ;;
 */duolog_1.2.3_linux_amd64.tar.gz) cp "$FIXTURE/archive.tar.gz" "$3" ;;
 *) exit 1 ;;
esac
`)
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			payload := []byte("#!/bin/sh\nprintf '1.2.3\\n'\n")
			if err := tw.WriteHeader(&tar.Header{Name: "duolog", Mode: 0755, Size: int64(len(payload))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(payload); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(archive.Bytes())
			checksums := fmt.Sprintf("%x  duolog_1.2.3_linux_amd64.tar.gz\n", digest)
			if scenario == "missing" {
				checksums = fmt.Sprintf("%x  other.tar.gz\n", digest)
			}
			if strings.HasPrefix(scenario, "tampered") {
				archive.WriteString("tampered")
			}
			if err := os.WriteFile(filepath.Join(dir, "archive.tar.gz"), archive.Bytes(), 0644); err != nil {
				t.Fatal(err)
			}
			write(filepath.Join(dir, "checksums.txt"), checksums)
			arch, osName, failChecksum := "x86_64", "Linux", "0"
			if scenario == "unsupported" {
				arch = "riscv64"
			}
			if scenario == "windows" {
				osName = "MINGW64_NT"
			}
			if scenario == "checksum-download" {
				failChecksum = "1"
			}
			home := filepath.Join(dir, "home")
			target := filepath.Join(home, ".local", "bin", "duolog")
			args := []string{script}
			installDir, envVersion := "", "latest"
			if scenario == "env" {
				installDir = filepath.Join(dir, "custom bin")
				envVersion = "v1.2.3"
			}
			if scenario == "dir" {
				args = append(args, "--dir", filepath.Join(dir, "flag bin"))
				installDir = filepath.Join(dir, "ignored")
			}
			if scenario == "env" {
				target = filepath.Join(installDir, "duolog")
			}
			if scenario == "dir" {
				target = filepath.Join(dir, "flag bin", "duolog")
			}
			if scenario == "pinned" {
				args = append(args, "--version", "v1.2.3")
			}
			if scenario == "pinned-bare" {
				args = append(args, "--version", "1.2.3")
			}
			if scenario == "bad-version" {
				args = append(args, "--version", "1.2.3;x")
			}
			if scenario == "via-go" {
				args = append(args, "--via-go")
			}
			if scenario == "update" || scenario == "tampered-existing" {
				if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
					t.Fatal(err)
				}
				write(target, "#!/bin/sh\necho 0.9.0\n")
			}
			run := func() (string, error) {
				cmd := exec.Command("sh", args...)
				path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
				if noCurl {
					path = bin + string(os.PathListSeparator) + filepath.Join(dir, "tools")
				}
				cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+path, "FIXTURE="+dir, "FAKE_ARCH="+arch, "FAKE_OS="+osName, "FAIL_CHECKSUM="+failChecksum, "DUOLOG_VERSION="+envVersion, "DUOLOG_INSTALL_DIR="+installDir)
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			out, err := run()
			success := scenario == "good" || strings.HasPrefix(scenario, "pinned") || scenario == "update" || scenario == "env" || scenario == "dir" || scenario == "wget"
			if success {
				if err != nil {
					t.Fatalf("install: %v\n%s", err, out)
				}
				installed, err := os.ReadFile(target)
				if err != nil || !bytes.Equal(installed, payload) {
					t.Fatalf("installed %q: %v", installed, err)
				}
				if !strings.Contains(out, "Next: duolog install --scope project") || !strings.Contains(out, "not on PATH") {
					t.Fatal(out)
				}
				if scenario == "update" && !strings.Contains(out, "Updating") {
					t.Fatal(out)
				}
				out, err = run()
				if err != nil || !strings.Contains(out, "already current") {
					t.Fatalf("rerun %v: %s", err, out)
				}
			} else {
				if err == nil {
					t.Fatalf("unexpected success: %s", out)
				}
				if scenario == "tampered-existing" {
					data, err := os.ReadFile(target)
					if err != nil || string(data) != "#!/bin/sh\necho 0.9.0\n" {
						t.Fatalf("existing binary changed: %q %v", data, err)
					}
				} else if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("binary installed on failure: %v", err)
				}
				if scenario == "unsupported" || scenario == "windows" || scenario == "via-go" {
					if !strings.Contains(out, "go install github.com/lorem-dev/duolog/cmd/duolog@latest") {
						t.Fatal(out)
					}
				}
			}
			if scenario == "no-fetcher" && !strings.Contains(out, "curl or wget") {
				t.Fatal(out)
			}
			if scenario == "wget" {
				urls, err := os.ReadFile(filepath.Join(dir, "urls"))
				if err != nil || !bytes.Contains(urls, []byte("api.github.com/repos/lorem-dev/duolog/releases/latest")) {
					t.Fatalf("wget urls %q: %v", urls, err)
				}
			}
			if scenario == "pinned" {
				urls, err := os.ReadFile(filepath.Join(dir, "urls"))
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Contains(urls, []byte("releases/latest")) || !bytes.Contains(urls, []byte("releases/download/v1.2.3/")) {
					t.Fatal(string(urls))
				}
			}
		})
	}
}

func TestRenderErrors(t *testing.T) {
	dir := t.TempDir()
	if err := renderTemplate(filepath.Join(dir, "missing"), filepath.Join(dir, "out"), defaultData()); err == nil {
		t.Fatal("expected missing-template error")
	}
}
