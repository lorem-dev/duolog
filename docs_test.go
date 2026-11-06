package duolog_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

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

// A free run drops every name that can bill per token, listed or not, and pins
// Claude fast mode off; a paid run needs the agent's paid_runs opt-in with a
// daily cap. The docs say so, quote the migration line and the doctor example
// as duolog prints them, and nowhere tell the person to hand a key to workers
// through env_passthrough, which no longer reaches them.
func TestDocsPaidKeyMigration(t *testing.T) {
	page := filepath.Join("docs", "configuration.md")
	inherit := docSection(t, page, "## What workers inherit")
	for _, want := range []string{
		"The default environment includes PATH, HOME and USER, plus `CODEX_HOME` and `CLAUDE_CONFIG_DIR`.",
		"It leaves out `ANTHROPIC_API_KEY` and `OPENAI_API_KEY`",
		"billed per token",
		"A free run never gets a name that can bill per token, even when `env_passthrough` lists it.",
		"`paid_runs.<agent>.allow`",
		// Fast mode is pinned off in the free variant only; the lead is
		// reported, never changed.
		"Free Claude workers and peers always start with fast mode off.",
		`"fastMode": false`,
		"duolog never changes the lead's settings",
		"it only reports how the lead is billed",
		// The warning wording and the doctor example, as duolog prints them.
		workerenv.MigrationLine("claude", "ANTHROPIC_API_KEY"),
		"`duolog doctor` warns",
		"Example config: paid_runs: {claude: {allow: true, daily_cap_usd: 5, env: [ANTHROPIC_API_KEY]}}",
		"set in duolog's environment or listed in `env_passthrough`",
		"claude: {allow: true, daily_cap_usd: 5}",
		"`paid_runs.<agent>.calibrate: true`",
	} {
		if !strings.Contains(inherit, want) {
			t.Errorf("What workers inherit lacks %q", want)
		}
	}
	for _, stale := range []string{
		"list it in an explicit `env_passthrough`",
		"keep the default names and add the key",
		"duolog does not cap that spend",
	} {
		if strings.Contains(inherit, stale) {
			t.Errorf("What workers inherit still says %q", stale)
		}
	}

	paid := docSection(t, page, "## How paid runs are allowed and capped")
	for _, want := range []string{
		"| `paid_runs.<agent>.allow` | `false` |",
		"| `paid_runs.<agent>.daily_cap_usd` | `0` |",
		"Must be above 0 with `allow`",
		"| `paid_runs.<agent>.env` |",
		"Optional",
		"| `paid_runs.<agent>.calibrate` | `false` |",
		"Needs `allow` too",
		"the last 24 hours, not a calendar day",
		"runs in flight",
		"the larger of",
		"lower bound",
		"never spends paid overage, workspace credits or reset credits",
		"drive admission",
		"may only make these stricter",
		"managed policy outside files is not checked",
	} {
		if !strings.Contains(paid, want) {
			t.Errorf("How paid runs are allowed and capped lacks %q", want)
		}
	}

	// No page tells the person to restore a key through env_passthrough.
	for _, path := range docFiles(t) {
		for i, line := range docLines(t, path) {
			if strings.Contains(line, "env_passthrough") && strings.Contains(line, "_API_KEY") {
				t.Errorf("%s:%d: hands an API key to workers through env_passthrough", path, i+1)
			}
		}
	}

	// Prices now drive admission of paid runs, not only estimates.
	for _, path := range []string{filepath.Join("internal", "core", "config", "config.default.yaml"), filepath.Join("internal", "core", "config", "config.go"), "duolog-config.schema.json"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, stale := range []string{"never routing", "never for routing"} {
			if strings.Contains(string(b), stale) {
				t.Errorf("%s says prices are %q; they drive paid-run admission", path, stale)
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
		// Development is everything before the first version section.
		dev, _, _ := strings.Cut(string(b), "\n## 1.")
		for _, want := range []string{"`paid_runs.<agent>.allow`", "`daily_cap_usd`"} {
			if !strings.Contains(dev, want) {
				t.Errorf("CHANGES.md Development lacks %s", want)
			}
		}
	}
}

// Live sessions are limited per account: by the agent's measured tier and for
// all agents together. The docs give the defaults, what counts, how each
// command waits, and that the shared project config may only tighten them.
func TestDocsPaidSessionLimits(t *testing.T) {
	limits := docSection(t, filepath.Join("docs", "configuration.md"), "## How many sessions run at once")
	for _, want := range []string{
		"| `limits.max_total_sessions` | `10` |",
		"| `limits.sessions_by_tier.small` | `3` |",
		"| `limits.sessions_by_tier.medium` | `5` |",
		"| `limits.sessions_by_tier.large` | `10` |",
		"| `limits.session_wait_seconds` | `60` |",
		"An agent with no measured rate yet is `small`",
		"A paid run or a workspace account runs one task at a time",
		"An open peer pane counts, idle or busy.",
		"An idle resumable thread runs no process and does not count.",
		"`limits.max_live_sessions` still applies",
		"the lower limit wins",
		"first come first served",
		"`duolog peer open` is refused at once",
		"Calibration cells wait for a slot without a time limit",
		"may only lower",
		"Only your user config may raise `max_total_sessions` or a tier",
		"raising one tier does not raise the next",
	} {
		if !strings.Contains(limits, want) {
			t.Errorf("How many sessions run at once lacks %q", want)
		}
	}
	// Examples name model families, never a version, and hold no personal data.
	for _, bad := range []*regexp.Regexp{
		regexp.MustCompile(`gpt-\d`),
		regexp.MustCompile(`claude-(fable|opus|sonnet|haiku)-\d`),
		regexp.MustCompile(`/Users/|/home/|@[a-z0-9-]+\.[a-z]`),
	} {
		if m := bad.FindString(limits); m != "" {
			t.Errorf("How many sessions run at once holds %q", m)
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
