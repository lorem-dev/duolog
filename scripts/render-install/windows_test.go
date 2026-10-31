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
			if err != nil {
				t.Fatal("CI must provide Windows PowerShell 5.1 and pwsh:", err)
			}
			for _, scenario := range []string{"good", "tampered", "pinned", "update", "tampered-existing", "missing-checksum", "bad-zip", "missing-binary"} {
				t.Run(scenario, func(t *testing.T) {
					dir := t.TempDir()
					script := filepath.Join(dir, "install.ps1")
					if err := renderTemplate("../install-template/install.ps1.tmpl", script, defaultData()); err != nil {
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
					code := "$DuologInstallerBaseUrl = '" + server.URL + "'; & '" + strings.ReplaceAll(script, "'", "''") + "'"
					cmd := exec.Command(binary, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", code)
					output, runErr := cmd.CombinedOutput()
					success := scenario == "good" || scenario == "pinned" || scenario == "update"
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
					if scenario == "pinned" {
						for _, request := range requests {
							if strings.Contains(request, "latest") {
								t.Fatal("pinned install resolved latest", requests)
							}
						}
					}
				})
			}
		})
	}
}
