package duolog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/workerenv"
)

func docFiles(t *testing.T) []string {
	t.Helper()
	files := []string{"README.md"}
	err := filepath.WalkDir("docs", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path == filepath.Join("docs", "superpowers") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func docLines(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "\n")
}

func TestDocsAreASCII(t *testing.T) {
	for _, path := range docFiles(t) {
		for i, line := range docLines(t, path) {
			for _, r := range line {
				if r > 127 {
					t.Errorf("%s:%d: non-ASCII character %q", path, i+1, r)
				}
			}
		}
	}
}

func TestDocsLineLength(t *testing.T) {
	for _, path := range docFiles(t) {
		fence := ""
		for i, line := range docLines(t, path) {
			trimmed := strings.TrimSpace(line)
			if fence != "" {
				if strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, string(fence[0])) == "" {
					fence = ""
				}
				continue
			}
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				marker := trimmed[0]
				n := 0
				for n < len(trimmed) && trimmed[n] == marker {
					n++
				}
				fence = trimmed[:n]
				continue
			}
			// A badge line holds one image link, which cannot wrap, like a table row.
			if strings.HasPrefix(trimmed, "|") || strings.HasPrefix(trimmed, "<a href=") {
				continue
			}
			if utf8.RuneCountInString(line) > 80 {
				t.Errorf("%s:%d: line exceeds 80 characters", path, i+1)
			}
		}
	}
}

// docSection returns the prose of the section under heading, with line breaks
// folded into spaces so that a phrase can wrap.
func docSection(t *testing.T, path, heading string) string {
	t.Helper()
	var section []string
	in := false
	for _, line := range docLines(t, path) {
		if strings.HasPrefix(line, "## ") {
			if in {
				break
			}
			in = line == heading
			continue
		}
		if in {
			section = append(section, strings.TrimSpace(line))
		}
	}
	if !in {
		t.Fatalf("%s: no section %q", path, heading)
	}
	return strings.Join(strings.Fields(strings.Join(section, " ")), " ")
}

// The interim patch drops API keys from the default inheritance and pins fast
// mode off; the docs say so, explain the migration line, and do not advertise
// paid_runs, which the config does not have yet.
func TestDocsPaidKeyMigration(t *testing.T) {
	inherit := docSection(t, filepath.Join("docs", "configuration.md"), "## What workers inherit")
	for _, want := range []string{
		"The default environment includes PATH, HOME and USER, plus `CODEX_HOME` and `CLAUDE_CONFIG_DIR`.",
		"It leaves out `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`",
		"billed per token",
		"fast mode off",
		`"fastMode": false`,
		// The page quotes the line as duolog prints it with the defaults.
		workerenv.MigrationLine("claude", "ANTHROPIC_API_KEY", config.DefaultEnvPassthrough("linux")),
		"`duolog doctor` warns",
		"set in duolog's environment and not listed in `env_passthrough`",
		"list it in an explicit `env_passthrough`",
		// An explicit list replaces the defaults, so the example keeps them all.
		"env_passthrough: [" + strings.Join(append(config.DefaultEnvPassthrough("linux"), "ANTHROPIC_API_KEY"), ", ") + "]",
		"On Windows, also keep the Windows names above.",
	} {
		if !strings.Contains(inherit, want) {
			t.Errorf("What workers inherit lacks %q", want)
		}
	}
	// A listed key passes to workers, so listing it ends the line.
	if strings.Contains(inherit, "or listed in `env_passthrough`") {
		t.Error("What workers inherit says a listed key still gets the line")
	}
	for _, path := range append(docFiles(t), "CHANGES.md") {
		for i, line := range docLines(t, path) {
			if strings.Contains(line, "paid_runs") {
				t.Errorf("%s:%d: advertises paid_runs, which the config does not have yet", path, i+1)
			}
		}
	}
	if b, err := os.ReadFile("CHANGES.md"); err != nil {
		t.Fatal(err)
	} else {
		// 1.4.0 dropped the keys, so its notes keep naming them however many
		// releases follow.
		_, rest, _ := strings.Cut(string(b), "\n## 1.4.0\n")
		section, _, _ := strings.Cut(rest, "\n## ")
		if !strings.Contains(section, "`ANTHROPIC_API_KEY` or `OPENAI_API_KEY`") {
			t.Error("CHANGES.md 1.4.0 does not name the dropped keys")
		}
	}
}

func TestDocsStartWithParagraph(t *testing.T) {
	for _, path := range docFiles(t) {
		lines := docLines(t, path)
		title := false
		inHTMLTitle := false
		prose := false
		for i, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if !title {
				// The README centres its icon, title and badges, which needs HTML.
				if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "<h1") {
					continue
				}
				if !strings.HasPrefix(line, "# ") && !strings.HasPrefix(line, "<h1") {
					t.Errorf("%s:%d: expected a page title", path, i+1)
					break
				}
				title = true
				inHTMLTitle = strings.HasPrefix(line, "<h1") && !strings.Contains(line, "</h1>")
				continue
			}
			if inHTMLTitle {
				inHTMLTitle = !strings.Contains(line, "</h1>")
				continue
			}
			if strings.HasPrefix(line, "<") {
				continue
			}
			if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, ">") || (line[0] >= '0' && line[0] <= '9' && strings.Contains(line, ". ")) {
				t.Errorf("%s:%d: the first non-empty line after the title must be prose", path, i+1)
			}
			prose = true
			break
		}
		if !title || !prose {
			t.Errorf("%s: missing title or opening paragraph", path)
		}
	}
}
