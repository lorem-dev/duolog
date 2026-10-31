package duolog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
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
