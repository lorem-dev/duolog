package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Execute the rendered release asset, with real HTTP and zip/checksum APIs.
func TestWindowsInstaller(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows PowerShell installer")
	}
	for _, shell := range []string{"powershell", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil && os.Getenv("CI") == "" {
				t.Skip(shell + " is not installed")
			}
			if err != nil {
				t.Fatal("CI must provide Windows PowerShell 5.1 and pwsh:", err)
			}
			if shell == "powershell" {
				pwsh, err := exec.LookPath("pwsh")
				if err != nil {
					// A developer's machine may have only 5.1. CI must run this.
					if os.Getenv("CI") == "" {
						t.Skip("pwsh is not installed")
					}
					t.Fatal(err)
				}
				// Reproduce pwsh -> Go -> powershell.exe module-path inheritance.
				// The installer must load 5.1 built-ins even with Core modules first.
				t.Setenv("PSModulePath", filepath.Join(filepath.Dir(pwsh), "Modules")+string(os.PathListSeparator)+os.Getenv("PSModulePath"))
			}
			for _, scenario := range []string{"good", "tampered", "pinned", "release", "update", "no-path", "string-path", "tampered-existing", "missing-checksum", "bad-zip", "missing-binary"} {
				t.Run(scenario, func(t *testing.T) {
					dir := t.TempDir()
					script := filepath.Join(dir, "install.ps1")
					rendered := defaultData()
					if scenario == "release" {
						// A release copy defaults to its own release.
						rendered.Version = "v1.2.3"
					}
					if err := renderTemplate("../install-template/install.ps1.tmpl", script, rendered); err != nil {
						t.Fatal(err)
					}
					var archive bytes.Buffer
					zw := zip.NewWriter(&archive)
					name := "duolog.exe"
					if scenario == "missing-binary" {
						name = "other.exe"
					}
					entry, err := zw.Create(name)
					if err != nil {
						t.Fatal(err)
					}
					payload := []byte("verified executable fixture")
					if _, err := entry.Write(payload); err != nil {
						t.Fatal(err)
					}
					if err := zw.Close(); err != nil {
						t.Fatal(err)
					}
					if scenario == "bad-zip" {
						archive.Reset()
						archive.WriteString("invalid zip")
					}
					digest := sha256.Sum256(archive.Bytes())
					if strings.HasPrefix(scenario, "tampered") {
						archive.WriteString("tampered")
					}
					arch := "amd64"
					if runtime.GOARCH == "arm64" {
						arch = "arm64"
					}
					asset := "duolog_1.2.3_windows_" + arch + ".zip"
					var mu sync.Mutex
					var requests []string
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mu.Lock()
						requests = append(requests, r.URL.Path)
						mu.Unlock()
						switch r.URL.Path {
						case "/releases/latest":
							http.Redirect(w, r, "/releases/tag/v1.2.3", http.StatusFound)
						case "/releases/tag/v1.2.3":
							w.WriteHeader(http.StatusOK)
						case "/releases/download/v1.2.3/" + asset:
							_, _ = w.Write(archive.Bytes())
						case "/releases/download/v1.2.3/checksums.txt":
							checksumName := asset
							if scenario == "missing-checksum" {
								checksumName = "other.zip"
							}
							fmt.Fprintf(w, "%x  %s\r\n", digest, checksumName)
						default:
							http.NotFound(w, r)
						}
					}))
					defer server.Close()
					installDir := filepath.Join(dir, "installed bin")
					target := filepath.Join(installDir, "duolog.exe")
					if scenario == "update" || scenario == "tampered-existing" {
						if err := os.MkdirAll(installDir, 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					t.Setenv("DUOLOG_INSTALL_DIR", installDir)
					t.Setenv("DUOLOG_VERSION", "")
					if scenario == "pinned" {
						t.Setenv("DUOLOG_VERSION", "v1.2.3")
					}
					// Exercise the default LOCALAPPDATA location as well.
					if scenario == "good" {
						t.Setenv("DUOLOG_INSTALL_DIR", "")
						t.Setenv("LOCALAPPDATA", dir)
						installDir = filepath.Join(dir, "Programs", "duolog")
						target = filepath.Join(installDir, "duolog.exe")
					}
					t.Setenv("PROCESSOR_ARCHITEW6432", strings.ToUpper(arch))
					// Never touch the real user environment: point the installer at a
					// scratch registry key that starts with one unexpanded entry and
					// other variables the installer must leave as they are.
					envKey := `HKCU:\Software\DuologInstallerTest\` + filepath.Base(dir)
					pathKind := "ExpandString"
					if scenario == "string-path" {
						pathKind = "String"
					}
					ps := func(command string) string {
						t.Helper()
						out, err := exec.Command(binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command).CombinedOutput()
						if err != nil {
							t.Fatalf("%v\n%s", err, out)
						}
						return strings.TrimSpace(string(out))
					}
					seed := func(name, value, kind string) string {
						return "$null = New-ItemProperty -LiteralPath '" + envKey + "' -Name " + name + " -Value '" + value + "' -PropertyType " + kind + "; "
					}
					ps("$null = New-Item -Force -Path '" + envKey + "'; " +
						seed("Path", `%SystemRoot%\keep`, pathKind) +
						seed("JAVA_HOME", `C:\jdk`, "String") +
						seed("TEMP", `%USERPROFILE%\Temp`, "ExpandString") +
						seed("Count", "7", "DWord"))
					t.Cleanup(func() { ps("Remove-Item -LiteralPath '" + envKey + "' -Recurse -Force -ErrorAction SilentlyContinue") })
					if scenario == "no-path" {
						t.Setenv("DUOLOG_NO_PATH", "1")
					} else {
						t.Setenv("DUOLOG_NO_PATH", "")
					}
					code := "$DuologInstallerBaseUrl = '" + server.URL + "'; $DuologInstallerEnvKey = '" + envKey + "'; & '" + strings.ReplaceAll(script, "'", "''") + "'"
					cmd := exec.Command(binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", code)
					output, runErr := cmd.CombinedOutput()
					storedPath := ps("(Get-Item -LiteralPath '" + envKey + "').GetValue('Path', '', 'DoNotExpandEnvironmentNames')")
					success := scenario == "good" || scenario == "pinned" || scenario == "release" || scenario == "update" || scenario == "no-path" || scenario == "string-path"
					if scenario == "no-path" {
						if storedPath != `%SystemRoot%\keep` {
							t.Fatal("PATH changed although DUOLOG_NO_PATH is set:", storedPath)
						}
					} else if success {
						if storedPath != `%SystemRoot%\keep;`+installDir {
							t.Fatal("user PATH not extended once:", storedPath)
						}
					} else if storedPath != `%SystemRoot%\keep` {
						t.Fatal("PATH changed by a failed install:", storedPath)
					}
					// The other variables survive, and Path keeps its kind.
					values := ps("$k = Get-Item -LiteralPath '" + envKey + "'; foreach ($n in 'Path', 'JAVA_HOME', 'TEMP', 'Count') { '{0}={1}={2}' -f $n, $k.GetValueKind($n), $k.GetValue($n, '', 'DoNotExpandEnvironmentNames') }")
					want := strings.Join([]string{"Path=" + pathKind + "=" + storedPath, `JAVA_HOME=String=C:\jdk`, `TEMP=ExpandString=%USERPROFILE%\Temp`, "Count=DWord=7"}, "\n")
					if got := strings.ReplaceAll(values, "\r\n", "\n"); got != want {
						t.Fatalf("user environment changed:\n%s\nwant:\n%s", got, want)
					}
					data, readErr := os.ReadFile(target)
					if success {
						if runErr != nil || readErr != nil || !bytes.Equal(data, payload) {
							t.Fatalf("%v %v %q\n%s", runErr, readErr, data, output)
						}
						if !strings.Contains(string(output), "PATH") {
							t.Fatal(string(output))
						}
					} else {
						if runErr == nil {
							t.Fatal("unexpected success:", string(output))
						}
						if scenario == "tampered-existing" {
							if readErr != nil || string(data) != "original" {
								t.Fatal("existing install changed", readErr, string(data))
							}
						} else if !os.IsNotExist(readErr) {
							t.Fatal("installed on failure", readErr)
						}
					}
					mu.Lock()
					defer mu.Unlock()
					if scenario == "pinned" || scenario == "release" {
						for _, request := range requests {
							if strings.Contains(request, "latest") {
								t.Fatal("pinned or release install resolved latest", requests)
							}
						}
					}
				})
			}
		})
	}
}
