package calibration

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/core/learn"
	"github.com/lorem-dev/duolog/internal/journal"
)

var releaseSubject = regexp.MustCompile(`(?i)^(release|bump|chore\(release\)|v?\d+\.\d+\.\d+\b)`)

var lockfiles = []string{"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
	"Cargo.lock", "poetry.lock", "uv.lock", "Gemfile.lock", "composer.lock"}

// specClasses are the classes a commit cannot test: only a plan or a spec
// written before the code describes their work.
var specClasses = []string{"skeleton", "plan_review", "debug_multi"}

// Filter drops generated and vendored files from c, measures what is left,
// and sets Excluded to the first reason the change is not a task, or "".
// With large, a change up to LargeMaxFiles and LargeMaxLines passes with
// Large set.
func Filter(c *Candidate, f config.CalibrationFilter, large bool) {
	c.Excluded, c.Large = "", false
	var kept []journal.FileChange
	for _, fc := range c.Files {
		if vendored(fc.Path) || generatedName(fc.Path) || c.generated[fc.Path] {
			c.dropped++
			continue
		}
		kept = append(kept, fc)
	}
	c.Files = kept
	c.Lines = 0
	for _, fc := range c.Files {
		c.Lines += fc.Added + fc.Removed
	}
	c.Excluded = exclusion(c)
	if c.Excluded != "" {
		return
	}
	files, lines := len(c.Files), c.Lines
	if within(files, f.MinFiles, f.MaxFiles) && within(lines, f.MinLines, f.MaxLines) {
		return
	}
	maxFiles, maxLines := f.MaxFiles, f.MaxLines
	if large {
		maxFiles, maxLines = f.LargeMaxFiles, f.LargeMaxLines
	}
	switch {
	case !within(files, f.MinFiles, maxFiles):
		c.Excluded = fmt.Sprintf("files outside %d-%d", f.MinFiles, maxFiles)
	case !within(lines, f.MinLines, maxLines):
		c.Excluded = fmt.Sprintf("lines outside %d-%d", f.MinLines, maxLines)
	default:
		c.Large = true
	}
}

// exclusion returns the first reason, in the documented order, that has
// nothing to do with size.
func exclusion(c *Candidate) string {
	switch {
	case c.Parent == "":
		return "no parent"
	case strings.HasPrefix(c.Subject, "Revert ") || strings.Contains(c.body, "This reverts commit"):
		return "revert"
	case releaseSubject.MatchString(c.Subject):
		return "release or version bump"
	case len(c.Files) > 0 && !slices.ContainsFunc(c.Files, func(fc journal.FileChange) bool {
		return !slices.Contains(lockfiles, path.Base(fc.Path))
	}):
		return "lockfile only"
	case len(c.Files) == 0 && c.dropped > 0:
		return "generated or vendored only"
	case c.formatting:
		return "formatting only"
	case strings.Contains(c.author, "[bot]"):
		return "bot author"
	}
	return ""
}

func within(n, lo, hi int) bool { return n >= lo && n <= hi }

// Classify sets the features, role and class of c from its files, and
// excludes a class only a spec task can test when c has no spec.
func Classify(c *Candidate, cfg *config.Config) {
	output := "code"
	switch {
	case len(c.Files) > 0 && all(c.Files, testFile):
		output = "tests"
	case len(c.Files) > 0 && all(c.Files, docFile):
		output = "docs"
	}
	scope := "1-3"
	switch n := len(c.Files); {
	case n > 20:
		scope = "20+"
	case n > 3:
		scope = "4-20"
	}
	// A file at the top of the repository counts as the component ".".
	c.Components = nil
	for _, fc := range c.Files {
		top, _, found := strings.Cut(fc.Path, "/")
		if !found {
			top = "."
		}
		if !slices.Contains(c.Components, top) {
			c.Components = append(c.Components, top)
		}
	}
	slices.Sort(c.Components)
	components := "1"
	if len(c.Components) > 1 {
		components = "2+"
	}
	c.Features = map[string]string{"output": output, "scope": scope, "components": components}
	c.Role = map[string]string{"code": "implement", "tests": "tests", "docs": "docs"}[output]
	c.Class = learn.Classify(cfg.Learning.Classify, c.Role, c.Features)
	if c.Excluded == "" && slices.Contains(specClasses, c.Class) && !specSource(c.StatementSource) {
		c.Excluded = "class needs a spec task"
	}
}

// specSource reports whether a statement was written as a spec before the
// code: a plan task, a design document, an issue or a pull request text.
func specSource(source string) bool {
	switch source {
	case "plan", "spec", "issue", "pr":
		return true
	}
	return false
}

func all(files []journal.FileChange, match func(string) bool) bool {
	for _, fc := range files {
		if !match(fc.Path) {
			return false
		}
	}
	return true
}

// underDir reports whether one of the folders above name is called dir.
func underDir(name string, dirs ...string) bool {
	parts := strings.Split(name, "/")
	for _, part := range parts[:len(parts)-1] {
		if slices.Contains(dirs, part) {
			return true
		}
	}
	return false
}

var testName = regexp.MustCompile(`(_test\.go|^test_.*\.py|_test\.py|\.(test|spec)\.(js|ts|tsx))$`)

func testFile(name string) bool {
	return testName.MatchString(path.Base(name)) || underDir(name, "tests", "test", "__tests__")
}

func docFile(name string) bool {
	switch path.Ext(name) {
	case ".md", ".rst", ".txt":
		return true
	}
	return underDir(name, "docs")
}

func vendored(name string) bool {
	return underDir(name, "vendor", "third_party", "node_modules")
}

func generatedName(name string) bool {
	for _, suffix := range []string{".pb.go", "_gen.go", ".min.js"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
