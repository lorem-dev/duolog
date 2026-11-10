package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Kills mutations that drop, rename or misroute any default release asset.
func TestRenderPairsDefaultAssets(t *testing.T) {
	pairs, err := renderPairs("", "")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{shTemplate, ".release/install.sh"},
		{ps1Template, ".release/install.ps1"},
		{"scripts/install-template/install-prompt.md.tmpl", ".release/install-prompt.md"},
	}
	if !slices.Equal(pairs, want) {
		t.Fatalf("pairs = %v, want %v", pairs, want)
	}
}

// Kills mutations that pin latest, lose the release tag, or change prompt line endings.
func TestRenderedPromptURLs(t *testing.T) {
	for _, tc := range []struct{ version, from string }{
		{"latest", "latest/download"},
		{"v1.4.0", "download/v1.4.0"},
		{"v1.5.0-rc.1", "download/v1.5.0-rc.1"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			body := renderPrompt(t, tc.version)
			prefix := "https://github.com/lorem-dev/duolog/releases/"
			for _, name := range []string{"install.sh", "install.ps1"} {
				if want := prefix + tc.from + "/" + name; !bytes.Contains(body, []byte(want)) {
					t.Errorf("prompt lacks %s", want)
				}
			}
			if bytes.Count(body, []byte(prefix)) != 2 || bytes.Contains(body, []byte("{{")) {
				t.Error("expected exactly two fully rendered installer URLs")
			}
			if bytes.ContainsRune(body, '\r') || !bytes.ContainsRune(body, '\n') || !bytes.HasSuffix(body, []byte("\n")) {
				t.Error("prompt must use LF line endings and end with LF")
			}
		})
	}
}

// Kills mutations that add shell expansion or PowerShell 5.1 argument-mangling characters.
func TestRenderedPromptArgumentSafety(t *testing.T) {
	for _, version := range []string{"latest", "v1.4.0"} {
		body := renderPrompt(t, version)
		if bytes.ContainsAny(body, "\"`$") {
			t.Errorf("prompt for %s contains a double quote, backtick or dollar sign", version)
		}
		for _, char := range body {
			if char > 127 {
				t.Fatalf("prompt for %s contains non-ASCII byte %x", version, char)
			}
		}
	}
}

func renderPrompt(t *testing.T, version string) []byte {
	t.Helper()
	data := defaultData()
	data.Version = version
	out := filepath.Join(t.TempDir(), "install-prompt.md")
	if err := renderTemplate("../install-template/install-prompt.md.tmpl", out, data); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// Kills mutations that omit the prompt from checksums or release attachments.
func TestPromptReleaseAssets(t *testing.T) {
	body, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"checksum:", "release:"} {
		_, block, ok := strings.Cut(string(body), "\n"+section+"\n")
		if !ok {
			t.Fatalf("missing %s section", section)
		}
		block, _, _ = strings.Cut(block, "\n\n")
		if !slices.Contains(strings.Split(block, "\n"), "    - glob: .release/install-prompt.md") {
			t.Errorf("%s lacks the prompt asset", section)
		}
	}
}
