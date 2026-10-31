package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionLifecycle(t *testing.T) {
	for _, original := range []string{"", "plain text\nwithout heading", "intro\r\n# Heading\r\n\r\nkeep  \r\n", "# Heading", "Heading\n=======\nbody", "#", "# Heading\n<!-- duolog --> old offer\nbody\n", "```md\n# example\n```\n# Real\nbody\n"} {
		t.Run(original, func(t *testing.T) {
			home, root := setup(t)
			o := Options{Home: home, Root: root, Scope: "project"}
			path := filepath.Join(root, "AGENTS.md")
			write(t, path, original)
			run(t, o)
			got := read(t, path)
			if strings.Count(got, offerMarker) != 1 || !strings.Contains(got, InstallOffer+"\n") {
				t.Fatal(got)
			}
			if strings.Contains(original, "# Heading\r\n") && !strings.HasPrefix(got, "intro\r\n# Heading\r\n"+InstallOffer+"\n") {
				t.Fatal(got)
			}
			if original == "plain text\nwithout heading" && got != InstallOffer+"\n"+original {
				t.Fatal(got)
			}
			run(t, o)
			if read(t, path) != got {
				t.Fatal("not idempotent")
			}
			if err := Remove(o); err != nil {
				t.Fatal(err)
			}
			want := strings.ReplaceAll(original, "<!-- duolog --> old offer\n", "")
			if read(t, path) != want {
				t.Fatalf("want %q got %q", want, read(t, path))
			}
			if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
				t.Fatal("created file survived")
			}
		})
	}
}

func TestInstructionClaudeLocationAndUserScope(t *testing.T) {
	home, root := setup(t)
	nested := filepath.Join(root, ".claude", "CLAUDE.md")
	write(t, nested, "# Existing\nkeep\n")
	run(t, Options{Home: home, Root: root, Scope: "user"})
	if read(t, nested) != "# Existing\nkeep\n" {
		t.Fatal("user install wrote offer")
	}
	for _, path := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(home, "AGENTS.md"), filepath.Join(home, "CLAUDE.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s", path)
		}
	}
	o := Options{Home: home, Root: root}
	run(t, o)
	if !strings.Contains(read(t, nested), InstallOffer) {
		t.Fatal("missing nested offer")
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("created wrong Claude file")
	}
	write(t, filepath.Join(root, "AGENTS.md"), read(t, filepath.Join(root, "AGENTS.md"))+"user addition\n")
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "AGENTS.md")) != "user addition\n" {
		t.Fatal("user content lost")
	}
	if read(t, nested) != "# Existing\nkeep\n" {
		t.Fatal("Claude content lost")
	}
}
