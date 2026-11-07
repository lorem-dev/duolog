package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/agent"
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
			if strings.Count(got, agent.InstructionBegin) != 1 || strings.Count(got, agent.InstructionEnd) != 1 || !strings.Contains(got, Block) {
				t.Fatal(got)
			}
			if strings.Contains(original, "# Heading\r\n") && !strings.HasPrefix(got, "intro\r\n# Heading\r\n"+Block) {
				t.Fatal(got)
			}
			if original == "plain text\nwithout heading" && got != Block+original {
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

func TestInstructionBlockHoldsOfferThenUsage(t *testing.T) {
	home, root := setup(t)
	o := Options{Home: home, Root: root, Scope: "project"}
	path := filepath.Join(root, "AGENTS.md")
	write(t, path, "# Rules\nkeep\n")
	run(t, o)
	want := "# Rules\n" + agent.InstructionBegin + "\n" + InstallOffer + "\n" + UsageNote + "\n" + agent.InstructionEnd + "\nkeep\n"
	if got := read(t, path); got != want {
		t.Fatalf("want %q got %q", want, got)
	}
	run(t, o)
	if read(t, path) != want {
		t.Fatal("second install changed the file")
	}
}

func TestInstructionBlockIsReplacedInPlace(t *testing.T) {
	home, root := setup(t)
	o := Options{Home: home, Root: root, Scope: "project"}
	path := filepath.Join(root, "AGENTS.md")
	old := "mine above\n" + agent.InstructionBegin + "\nold offer\nold usage\n" + agent.InstructionEnd + "\nmine below\n"
	write(t, path, old)
	run(t, o)
	if want := "mine above\n" + Block + "mine below\n"; read(t, path) != want {
		t.Fatalf("want %q got %q", want, read(t, path))
	}
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != "mine above\nmine below\n" {
		t.Fatalf("uninstall left %q", read(t, path))
	}
}

func TestInstructionLegacyOfferBecomesBlock(t *testing.T) {
	home, root := setup(t)
	o := Options{Home: home, Root: root, Scope: "project"}
	path := filepath.Join(root, "AGENTS.md")
	write(t, path, "# Rules\n"+agent.LegacyOfferMarker+" "+InstallOffer+"\nkeep\n")
	run(t, o)
	if want := "# Rules\n" + Block + "keep\n"; read(t, path) != want {
		t.Fatalf("want %q got %q", want, read(t, path))
	}
	// Uninstall removes a legacy line that install never replaced, too.
	write(t, path, "keep\n"+agent.LegacyOfferMarker+" old\n")
	if err := Remove(o); err != nil {
		t.Fatal(err)
	}
	if read(t, path) != "keep\n" {
		t.Fatalf("uninstall left %q", read(t, path))
	}
}

func TestInstructionUnpairedMarkersLeaveFilesAlone(t *testing.T) {
	for name, text := range map[string]string{
		"begin only": "# Rules\n" + agent.InstructionBegin + "\nkeep\n",
		"end only":   "# Rules\n" + agent.InstructionEnd + "\nkeep\n",
		"reversed":   agent.InstructionEnd + "\n" + agent.InstructionBegin + "\n",
		"twice":      Block + Block,
	} {
		t.Run(name, func(t *testing.T) {
			home, root := setup(t)
			o := Options{Home: home, Root: root, Scope: "project"}
			path := filepath.Join(root, "AGENTS.md")
			write(t, path, text)
			other := filepath.Join(root, ".claude", "CLAUDE.md")
			write(t, other, "# Other\n"+Block)
			if _, err := testDeps().Run(o); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("install error %v does not name %s", err, path)
			}
			if read(t, path) != text {
				t.Fatal("install changed the file")
			}
			if err := Remove(o); err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("uninstall error %v does not name %s", err, path)
			}
			if read(t, path) != text || read(t, other) != "# Other\n"+Block {
				t.Fatal("uninstall changed a file")
			}
		})
	}
}

func TestInstructionBlockIsShort(t *testing.T) {
	if n := len(strings.Fields(UsageNote)); n > 60 || strings.Contains(UsageNote, "\n") {
		t.Fatalf("usage note has %d words or several lines", n)
	}
}

func TestCheckReportsInstructionBlock(t *testing.T) {
	home, root := setup(t)
	o := Options{Home: home, Root: root, Scope: "project"}
	run(t, o)
	stale := func() []string {
		var files []string
		statuses, err := testDeps().Check(o)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range statuses {
			for _, f := range s.Changed {
				if filepath.Base(f) == "AGENTS.md" || filepath.Base(f) == "CLAUDE.md" {
					files = append(files, filepath.Base(f))
				}
			}
		}
		return files
	}
	if got := stale(); len(got) != 0 {
		t.Fatalf("fresh install reported %v", got)
	}
	agents := filepath.Join(root, "AGENTS.md")
	write(t, agents, strings.Replace(read(t, agents), UsageNote, "old usage", 1))
	if got := stale(); len(got) != 1 || got[0] != "AGENTS.md" {
		t.Fatalf("outdated block reported %v", got)
	}
	write(t, agents, "no block\n")
	if got := stale(); len(got) != 1 {
		t.Fatalf("missing block reported %v", got)
	}
	run(t, o)
	if got := stale(); len(got) != 0 {
		t.Fatalf("reinstall reported %v", got)
	}
	// --hooks-only compares hooks alone.
	write(t, agents, "no block\n")
	o.HooksOnly = true
	if got := stale(); len(got) != 0 {
		t.Fatalf("hooks-only reported %v", got)
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
