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
	for _, scenario := range []string{"good", "tampered", "missing", "checksum-download", "pinned", "pinned-bare", "bad-version", "unsupported", "windows", "via-go", "update", "env", "dir", "tampered-existing", "wget", "wget-no-sed", "wget-no-head", "no-fetcher", "release", "release-flag", "release-env"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "install.sh")
			rendered := defaultData()
			if strings.HasPrefix(scenario, "release") {
				// A release copy defaults to its own release, not the newest one.
				rendered.Version = "v1.2.3"
				if scenario != "release" {
					rendered.Version = "v0.0.1"
				}
			}
			if err := renderTemplate("../install-template/install.sh.tmpl", script, rendered); err != nil {
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
			// The wget path reads the latest tag with sed and head, so a
			// system without one of them must be told which.
			noCurl := strings.HasPrefix(scenario, "wget") || scenario == "no-fetcher"
			if noCurl {
				if err := os.Remove(filepath.Join(bin, "curl")); err != nil {
					t.Fatal(err)
				}
				tools := filepath.Join(dir, "tools")
				if err := os.Mkdir(tools, 0755); err != nil {
					t.Fatal(err)
				}
				for _, tool := range []string{"tr", "tar", "mktemp", "awk", "chmod", "mv", "cp", "rm", "mkdir", "sed", "head", "cat", "sha256sum", "shasum", "gzip", "dirname", "basename"} {
					if scenario == "wget-no-"+tool {
						continue
					}
					if found, err := exec.LookPath(tool); err == nil {
						if err := os.Symlink(found, filepath.Join(tools, tool)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if strings.HasPrefix(scenario, "wget") {
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
			if scenario == "pinned" || scenario == "release-flag" {
				args = append(args, "--version", "v1.2.3")
			}
			if scenario == "release" {
				envVersion = ""
			}
			if scenario == "release-env" {
				envVersion = "v1.2.3"
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
			success := scenario == "good" || strings.HasPrefix(scenario, "pinned") || strings.HasPrefix(scenario, "release") || scenario == "update" || scenario == "env" || scenario == "dir" || scenario == "wget"
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
			if tool, ok := strings.CutPrefix(scenario, "wget-no-"); ok && !strings.Contains(out, "missing required tool: "+tool) {
				t.Fatal(out)
			}
			if scenario == "wget" {
				urls, err := os.ReadFile(filepath.Join(dir, "urls"))
				if err != nil || !bytes.Contains(urls, []byte("api.github.com/repos/lorem-dev/duolog/releases/latest")) {
					t.Fatalf("wget urls %q: %v", urls, err)
				}
			}
			if scenario == "pinned" || strings.HasPrefix(scenario, "release") {
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

func TestCheckVersion(t *testing.T) {
	for _, ok := range []string{"latest", "v1.4.0", "v1.5.0-rc.1"} {
		if err := checkVersion(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1.4.0", "v1.4.0;x", "v", "$(x)", "latest "} {
		if err := checkVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// The copy rendered without a version says latest; a release copy names its tag.
func TestRenderedDefaults(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"install.sh", "install.ps1"} {
		for _, version := range []string{"latest", "v1.4.0"} {
			data := defaultData()
			data.Version = version
			out := filepath.Join(dir, version+"-"+name)
			if err := renderTemplate("../install-template/"+name+".tmpl", out, data); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			want := "DUOLOG_VERSION:-" + version + "}"
			if name == "install.ps1" {
				want = "$version = '" + version + "'"
			}
			if !strings.Contains(string(body), want) {
				t.Errorf("%s (%s) lacks %q", name, version, want)
			}
		}
	}
}

func TestPowerShellInstallerAddsUserPath(t *testing.T) {
	data, err := os.ReadFile("../install-template/install.ps1.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"HKCU:\\Environment", "DUOLOG_NO_PATH", "ExpandString"} {
		if !strings.Contains(body, want) {
			t.Errorf("install.ps1.tmpl lacks %q", want)
		}
	}
	if strings.Contains(body, "HKLM") || strings.Contains(body, "'Machine'") {
		t.Error("install.ps1.tmpl must not touch the machine PATH")
	}
	// New-Item -Force recreates an existing key and drops every other user
	// variable; New-ItemProperty and Set-ItemProperty can change the kind.
	for _, banned := range []string{"New-Item -Path $envKey", "New-Item -Force -Path $envKey", "New-ItemProperty", "Set-ItemProperty", "Remove-Item -LiteralPath $envKey"} {
		if strings.Contains(body, banned) {
			t.Errorf("install.ps1.tmpl must not use %q on the environment key", banned)
		}
	}
	for _, want := range []string{"CreateSubKey", "GetValueKind('Path')", "SetValue('Path', $newPath, $kind)"} {
		if !strings.Contains(body, want) {
			t.Errorf("install.ps1.tmpl lacks %q", want)
		}
	}
}

// With -o alone the extension picks the template, so an install.ps1 is never
// the POSIX installer under a PowerShell name.
func TestRenderPairsByExtension(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		out, tmpl, want string
	}{
		{"x/install.ps1", ps1Template, "$version = 'v1.4.1' }\r\n"},
		{"x/Install.PS1", ps1Template, "$version = 'v1.4.1' }\r\n"},
		{"x/install.sh", shTemplate, "#!/bin/sh\n"},
	} {
		pairs, err := renderPairs("", tc.out)
		if err != nil {
			t.Fatalf("%s: %v", tc.out, err)
		}
		if len(pairs) != 1 || pairs[0] != [2]string{tc.tmpl, tc.out} {
			t.Fatalf("%s: pairs = %v, want %s", tc.out, pairs, tc.tmpl)
		}
		data := defaultData()
		data.Version = "v1.4.1"
		out := filepath.Join(dir, filepath.Base(tc.out))
		// The pairs name templates from the repository root.
		if err := renderTemplate(filepath.Join("..", "..", pairs[0][0]), out, data); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("%s lacks %q", tc.out, tc.want)
		}
		if tc.tmpl == ps1Template && bytes.Contains(body, []byte("#!/bin/sh")) {
			t.Errorf("%s is the POSIX installer", tc.out)
		}
	}
	for _, out := range []string{"x/install", "x/install.txt", "x/install.ps1.bak"} {
		if pairs, err := renderPairs("", out); err == nil {
			t.Errorf("%s: pairs = %v, want an error asking for -template", out, pairs)
		}
	}
	// -template still renders any output name, and the defaults stay.
	if pairs, err := renderPairs("custom.tmpl", "x/install.txt"); err != nil || pairs[0] != [2]string{"custom.tmpl", "x/install.txt"} {
		t.Errorf("custom template: %v %v", pairs, err)
	}
	if pairs, err := renderPairs("", ""); err != nil || len(pairs) != 2 {
		t.Errorf("defaults: %v %v", pairs, err)
	}
}

func TestRenderErrors(t *testing.T) {
	dir := t.TempDir()
	if err := renderTemplate(filepath.Join(dir, "missing"), filepath.Join(dir, "out"), defaultData()); err == nil {
		t.Fatal("expected missing-template error")
	}
}
