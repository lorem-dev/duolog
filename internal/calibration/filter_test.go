package calibration

import (
	"fmt"
	"testing"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

var testFilter = config.CalibrationFilter{MinFiles: 1, MaxFiles: 20, MinLines: 10, MaxLines: 400, LargeMaxFiles: 40, LargeMaxLines: 1500}

// testConfig loads the default configuration with a temporary home, so no
// layer of the person's own configuration is read.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// files returns n code files under dir with lines changed lines in all.
func files(dir string, n, lines int) []journal.FileChange {
	out := make([]journal.FileChange, n)
	for i := range out {
		out[i] = journal.FileChange{Path: fmt.Sprintf("%s/f%d.go", dir, i), Added: lines / n}
	}
	out[0].Added += lines % n
	return out
}

func TestFilterReasons(t *testing.T) {
	base := func() Candidate {
		return Candidate{Parent: "p", Ref: "r", Subject: "add parser", Files: files("a", 3, 60), author: "t"}
	}
	cases := []struct {
		name      string
		setup     func(c *Candidate)
		large     bool
		want      string
		wantLarge bool
	}{
		{"no parent", func(c *Candidate) { c.Parent = "" }, false, "no parent", false},
		{"revert subject", func(c *Candidate) { c.Subject = `Revert "add x"` }, false, "revert", false},
		{"revert body", func(c *Candidate) { c.body = "This reverts commit abc." }, false, "revert", false},
		{"release", func(c *Candidate) { c.Subject = "release: 1.2.0" }, false, "release or version bump", false},
		{"version", func(c *Candidate) { c.Subject = "v1.2.0" }, false, "release or version bump", false},
		{"lockfile", func(c *Candidate) { c.Files = []journal.FileChange{{Path: "go.sum", Added: 40}} }, false, "lockfile only", false},
		{"vendored", func(c *Candidate) { c.Files = []journal.FileChange{{Path: "vendor/x/y.go", Added: 40}} }, false, "generated or vendored only", false},
		{"formatting", func(c *Candidate) { c.formatting = true }, false, "formatting only", false},
		{"bot", func(c *Candidate) { c.author = "dependabot[bot]" }, false, "bot author", false},
		{"too many files", func(c *Candidate) { c.Files = files("a", 25, 100) }, false, "files outside 1-20", false},
		{"too few lines", func(c *Candidate) { c.Files = files("a", 3, 5) }, false, "lines outside 10-400", false},
		{"too many lines", func(c *Candidate) { c.Files = files("a", 3, 600) }, false, "lines outside 10-400", false},
		{"kept", func(c *Candidate) {}, false, "", false},
		{"large", func(c *Candidate) { c.Files = files("a", 30, 900) }, true, "", true},
	}
	for _, tc := range cases {
		c := base()
		tc.setup(&c)
		Filter(&c, testFilter, tc.large)
		if c.Excluded != tc.want || c.Large != tc.wantLarge {
			t.Errorf("%s: Excluded %q Large %v, want %q %v", tc.name, c.Excluded, c.Large, tc.want, tc.wantLarge)
		}
	}

	// Generated and vendored files leave the change before it is measured.
	c := base()
	c.Files = append(c.Files, journal.FileChange{Path: "vendor/x/y.go", Added: 900}, journal.FileChange{Path: "api/x.pb.go", Added: 900})
	Filter(&c, testFilter, false)
	if c.Excluded != "" || len(c.Files) != 3 || c.Lines != 60 {
		t.Errorf("generated files kept: Excluded %q, %d files, %d lines", c.Excluded, len(c.Files), c.Lines)
	}
}

func TestFeaturesAndClass(t *testing.T) {
	cfg := testConfig(t)
	cases := []struct {
		files                                  []journal.FileChange
		output, role, scope, components, class string
	}{
		{[]journal.FileChange{{Path: "x_test.go", Added: 20}}, "tests", "tests", "1-3", "1", "tests"},
		{[]journal.FileChange{{Path: "docs/a.md", Added: 20}}, "docs", "docs", "1-3", "1", "docs"},
		{append(files("a", 3, 30), files("b", 2, 30)...), "code", "implement", "4-20", "2+", "implement"},
	}
	for _, tc := range cases {
		c := Candidate{StatementSource: "commit", Files: tc.files}
		Classify(&c, cfg)
		f := c.Features
		if f["output"] != tc.output || c.Role != tc.role || f["scope"] != tc.scope || f["components"] != tc.components || c.Class != tc.class {
			t.Errorf("%v: features %v role %q class %q, want output %s role %s scope %s components %s class %s",
				tc.files, f, c.Role, c.Class, tc.output, tc.role, tc.scope, tc.components, tc.class)
		}
		if c.Excluded != "" {
			t.Errorf("%v: Excluded %q", tc.files, c.Excluded)
		}
	}
}

func TestClassWithoutCommitTestsNeedsSpec(t *testing.T) {
	cfg := &config.Config{}
	cfg.Learning.Classify = []config.ClassRule{{When: map[string]string{"output": "code"}, Class: "skeleton"}}
	c := Candidate{StatementSource: "commit", Files: files("a", 2, 40)}
	Classify(&c, cfg)
	if c.Class != "skeleton" || c.Excluded != "class needs a spec task" {
		t.Fatalf("commit: class %q Excluded %q", c.Class, c.Excluded)
	}
	c = Candidate{StatementSource: "plan", Files: files("a", 2, 40)}
	Classify(&c, cfg)
	if c.Class != "skeleton" || c.Excluded != "" {
		t.Fatalf("plan: class %q Excluded %q", c.Class, c.Excluded)
	}
}
