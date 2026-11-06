package calibration

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func collect(t *testing.T, root string, gh GH) ([]Candidate, []string) {
	t.Helper()
	candidates, notes, err := Collect(context.Background(), root, testConfig(t), gh, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return candidates, notes
}

// containing returns the candidates whose commits include c.
func containing(candidates []Candidate, c string) []Candidate {
	var out []Candidate
	for _, k := range candidates {
		if slices.Contains(k.Commits, c) {
			out = append(out, k)
		}
	}
	return out
}

func only(t *testing.T, candidates []Candidate, c string) Candidate {
	t.Helper()
	found := containing(candidates, c)
	if len(found) != 1 {
		t.Fatalf("%d candidates contain %s, want 1: %+v", len(found), c, found)
	}
	return found[0]
}

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

func TestCollectGroupsLedgerRange(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md",
		"# Plan\n\n### Task 2: Add parser\n\nWrite a parser for lines. It reads one line at a time, splits it into a key and a value at the first "+
			"equals sign, trims both, and reports the line number of any line it cannot read.\n\n```go\nfunc Parse() {}\n```\n\n"+
			"Keep it small.\n\n### Task 3: Other\n\nSomething else.\n")
	c1 := commit(t, root, "start")
	write(t, root, "parse.go", lines("one", 10))
	c2 := commit(t, root, "parser part one")
	write(t, root, "parse.go", lines("two", 10))
	c3 := commit(t, root, "parser part two")
	write(t, root, "b.txt", lines("b", 12))
	c4 := commit(t, root, "unrelated")
	write(t, root, ".superpowers/sdd/2026-01-01-x/progress.md",
		fmt.Sprintf("Task 1: complete (commits %s..%s)\nTask 2: complete (commits %s..%s)\n", c1[:7], c1[:7], c2[:7], c3[:7]))

	candidates, _ := collect(t, root, nil)
	k := only(t, candidates, c2)
	if !slices.Equal(k.Commits, []string{c2, c3}) || k.Parent != c1 || k.Ref != c3 {
		t.Fatalf("commits %v parent %s ref %s, want [%s %s] %s %s", k.Commits, k.Parent, k.Ref, c2, c3, c1, c3)
	}
	if len(containing(candidates, c3)) != 1 {
		t.Fatal("c3 is in more than one candidate")
	}
	if k.StatementSource != "plan" || !strings.Contains(k.Statement, "Add parser") || strings.Contains(k.Statement, "```") || k.NeedsRewrite {
		t.Fatalf("source %q NeedsRewrite %v statement %q", k.StatementSource, k.NeedsRewrite, k.Statement)
	}
	if !strings.Contains(k.Statement, "Keep it small.") || strings.Contains(k.Statement, "Something else") {
		t.Fatalf("statement is not the Task 2 section: %q", k.Statement)
	}
	if !strings.HasPrefix(k.ID, "k-") || len(k.ID) != 14 {
		t.Fatalf("ID %q", k.ID)
	}
	if u := only(t, candidates, c4); u.StatementSource == "plan" || len(u.Commits) != 1 {
		t.Fatalf("unrelated commit: %+v", u)
	}
	if first := only(t, candidates, c1); first.Excluded != "no parent" {
		t.Fatalf("root commit Excluded %q", first.Excluded)
	}
}

func TestCollectCommitStatementNeedsRewrite(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "start")
	write(t, root, "widget.go", lines("w", 12))
	subject := "add widget renderer"
	c2 := commit(t, root, subject+"\n\nThe widget draws a box; follows "+c1[:9]+".\nSee add widget renderer notes.\n\nSigned-off-by: t <t@t>")

	candidates, _ := collect(t, root, nil)
	k := only(t, candidates, c2)
	if k.StatementSource != "commit" || !k.NeedsRewrite {
		t.Fatalf("source %q NeedsRewrite %v", k.StatementSource, k.NeedsRewrite)
	}
	if k.Subject != subject {
		t.Fatalf("Subject %q", k.Subject)
	}
	for _, leak := range []string{c1[:7], c2[:7], subject, "t@t"} {
		if strings.Contains(k.Statement, leak) {
			t.Fatalf("statement %q contains %q", k.Statement, leak)
		}
	}
	if !strings.Contains(k.Statement, "The widget draws a box") {
		t.Fatalf("statement %q lost the message body", k.Statement)
	}
}

func TestCollectUsesPRTextFromGH(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	write(t, root, "cache.go", lines("c", 12))
	c2 := commit(t, root, "add cache layer")
	body := "Cache the parsed config between calls."
	var calls [][]string
	gh := func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(fmt.Sprintf(`[{"number":7,"body":%q,"mergeCommit":{"oid":%q},"closingIssuesReferences":[]}]`, body, c2)), nil
	}

	candidates, _ := collect(t, root, gh)
	k := only(t, candidates, c2)
	if k.StatementSource != "pr" || k.Statement != body || k.NeedsRewrite {
		t.Fatalf("source %q NeedsRewrite %v statement %q", k.StatementSource, k.NeedsRewrite, k.Statement)
	}
	want := []string{"pr", "list", "--state", "merged", "--limit", "200", "--json", "number,body,mergeCommit,closingIssuesReferences"}
	if len(calls) != 1 || !slices.Equal(calls[0], want) {
		t.Fatalf("gh calls %v", calls)
	}
}

func TestCollectWithoutGHAddsNote(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	write(t, root, "cache.go", lines("c", 12))
	c2 := commit(t, root, "add cache layer")
	gh := func(ctx context.Context, args ...string) ([]byte, error) {
		return nil, errors.New("not logged in")
	}

	candidates, notes, err := Collect(context.Background(), root, testConfig(t), gh, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, "gh: skipped") }) {
		t.Fatalf("notes %v lack gh: skipped", notes)
	}
	if k := only(t, candidates, c2); k.StatementSource == "pr" {
		t.Fatalf("source %q after a gh error", k.StatementSource)
	}
}

func TestCollectADRNamedByCommit(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	adr := "# Cache parsed config\n\nThe loader keeps the parsed config in memory.\n"
	write(t, root, "docs/adr/0003-cache.md", adr)
	c2 := commit(t, root, "add decision record")
	write(t, root, "cache.go", lines("c", 12))
	c3 := commit(t, root, "implement 0003-cache.md")

	candidates, _ := collect(t, root, nil)
	k := only(t, candidates, c3)
	if k.StatementSource != "spec" || !strings.Contains(k.Statement, "keeps the parsed config in memory") {
		t.Fatalf("source %q statement %q", k.StatementSource, k.Statement)
	}
	if d := only(t, candidates, c2); d.StatementSource == "spec" {
		t.Fatal("the commit that adds the record got it as statement")
	}
}

func TestCollectMarksFormattingAndGenerated(t *testing.T) {
	root := project(t)
	write(t, root, "a.go", lines("a", 12))
	commit(t, root, "start")
	write(t, root, "a.go", strings.ReplaceAll(lines("a", 12), " ", "   ")+"\n\n")
	c2 := commit(t, root, "reindent")
	write(t, root, "api/x.go", "// Code generated by tool. DO NOT EDIT.\n\n"+lines("x", 30))
	c3 := commit(t, root, "regenerate api")

	candidates, _ := collect(t, root, nil)
	if k := only(t, candidates, c2); k.Excluded != "formatting only" {
		t.Fatalf("whitespace commit Excluded %q", k.Excluded)
	}
	if k := only(t, candidates, c3); k.Excluded != "generated or vendored only" {
		t.Fatalf("generated commit Excluded %q", k.Excluded)
	}
}

func TestHiddenTestsAndNames(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	write(t, root, "parse.go", "package p\n\nfunc ParseLine(s string) string { return s }\n\nfunc helper() {}\n")
	write(t, root, "parse_test.go", "package p\n\nimport \"testing\"\n\nfunc TestParseLine(t *testing.T) {\n\tif ParseLine(\"x\") != \"x\" {\n\t\tt.Fatal()\n\t}\n}\n")
	c2 := commit(t, root, "parse lines")

	candidates, _ := collect(t, root, nil)
	k := only(t, candidates, c2)
	if !slices.Equal(k.Hidden, []string{"parse_test.go"}) || !slices.Equal(k.HiddenNames, []string{"ParseLine"}) {
		t.Fatalf("Hidden %v HiddenNames %v", k.Hidden, k.HiddenNames)
	}
}

// TestCollectSpecStatementNeedsHiddenNames gives a pull request text that
// does not name the function the hidden test calls, and one that does: only
// the first must be rewritten, since a candidate cannot guess the name.
func TestCollectSpecStatementNeedsHiddenNames(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	var commits []string
	for _, name := range []string{"Widget", "Gadget"} {
		write(t, root, strings.ToLower(name)+".go", "package p\n\nfunc Parse"+name+"(s string) string { return s }\n\n"+lines("// line", 10))
		write(t, root, strings.ToLower(name)+"_test.go", "package p\n\nimport \"testing\"\n\nfunc TestParse"+name+"(t *testing.T) {\n\tif Parse"+name+"(\"x\") != \"x\" {\n\t\tt.Fatal()\n\t}\n}\n")
		commits = append(commits, commit(t, root, "parse "+strings.ToLower(name)+"s"))
	}
	bodies := []string{"Add a parser for this format.", "Add ParseGadget, a parser for this format."}

	candidates, _ := collect(t, root, prList(commits, bodies))
	for i, want := range []bool{true, false} {
		k := only(t, candidates, commits[i])
		if k.StatementSource != "pr" || k.Statement != bodies[i] || k.NeedsRewrite != want {
			t.Errorf("source %q statement %q hidden names %v NeedsRewrite %v, want %v", k.StatementSource, k.Statement, k.HiddenNames, k.NeedsRewrite, want)
		}
	}
}

// TestCollectStripsCodeFromEverySource writes the solution as fenced code
// into a plan, a design document, a pull request and an issue: no statement
// keeps it.
func TestCollectStripsCodeFromEverySource(t *testing.T) {
	const prose, code = "Cache the parsed config between calls.", "var cache sync.Map"
	text := prose + "\n\n```go\n" + code + "\n```\n\n~~~\n" + code + "\n~~~\n\nKeep it small.\n"
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md", "# Plan\n\n### Task 1: Cache\n\n"+text)
	write(t, root, "docs/adr/0001-cache.md", "# Cache\n\n"+text)
	commit(t, root, "start")
	write(t, root, "plan.go", lines("p", 12))
	cPlan := commit(t, root, "cache plan")
	write(t, root, "spec.go", lines("s", 12))
	cSpec := commit(t, root, "implement 0001-cache.md")
	write(t, root, "pr.go", lines("r", 12))
	cPR := commit(t, root, "pull request change")
	write(t, root, "issue.go", lines("i", 12))
	cIssue := commit(t, root, "issue change")
	write(t, root, ".superpowers/sdd/2026-01-01-x/progress.md", fmt.Sprintf("Task 1: complete (commits %s..%s)\n", cPlan[:7], cPlan[:7]))
	gh := func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"number":7,"body":%q,"mergeCommit":{"oid":%q},"closingIssuesReferences":[]},`+
			`{"number":8,"body":"Change the issue.","mergeCommit":{"oid":%q},"closingIssuesReferences":[{"number":3,"body":%q}]}]`,
			text, cPR, cIssue, text)), nil
	}

	candidates, _ := collect(t, root, gh)
	for _, source := range []struct{ name, commit string }{
		{"plan", cPlan}, {"spec", cSpec}, {"pr", cPR}, {"issue", cIssue},
	} {
		k := only(t, candidates, source.commit)
		if k.StatementSource != source.name {
			t.Errorf("source %q, want %q", k.StatementSource, source.name)
			continue
		}
		if strings.Contains(k.Statement, code) || strings.Contains(k.Statement, "```") || strings.Contains(k.Statement, "~~~") {
			t.Errorf("%s statement %q keeps the code", source.name, k.Statement)
		}
		if !strings.Contains(k.Statement, prose) || !strings.Contains(k.Statement, "Keep it small.") {
			t.Errorf("%s statement %q lost its text", source.name, k.Statement)
		}
	}
}

// TestCollectThinStatementAfterCodeNeedsRewrite has pull requests whose
// fenced code is removed: a statement left under 30 words, or under half of
// its words, is marked for a rewrite; a long one that loses a small block,
// and a short one that loses nothing, are not. Indented code is not a fence
// and stays.
func TestCollectThinStatementAfterCodeNeedsRewrite(t *testing.T) {
	const prose = "Cache the parsed config between calls so that a second load of the same file reads it from memory, " +
		"drops the entry when the file changes on disk, and never shares one entry between two projects."
	code := "```go\n" + strings.Repeat("cache.Store(path, parsed) // keep it\n", 12) + "```"
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	bodies := []string{
		"Cache the config.\n\n" + code,
		prose + "\n\n" + code,
		prose + "\n\n```go\nvar cache sync.Map\n```",
		"Cache the parsed config between calls.",
		prose + "\n\n    var cache sync.Map",
	}
	var commits []string
	for i := range bodies {
		write(t, root, fmt.Sprintf("f%d.go", i), lines("f", 12))
		commits = append(commits, commit(t, root, fmt.Sprintf("change %d", i)))
	}

	candidates, _ := collect(t, root, prList(commits, bodies))
	for i, want := range []bool{true, true, false, false, false} {
		k := only(t, candidates, commits[i])
		if k.StatementSource != "pr" || strings.Contains(k.Statement, "```") || k.NeedsRewrite != want {
			t.Errorf("body %d: source %q statement %q NeedsRewrite %v, want %v", i, k.StatementSource, k.Statement, k.NeedsRewrite, want)
		}
	}
	if k := only(t, candidates, commits[4]); !strings.Contains(k.Statement, "    var cache sync.Map") {
		t.Errorf("indented code: statement %q, want it kept", k.Statement)
	}
}

// commitAs commits every file in root under an author and a committer name
// of its own, so the history holds names a statement must lose.
func commitAs(t *testing.T, root, author, committer, message string) string {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.name="+committer, "-c", "user.email=committer@example.invalid",
		"commit", "-q", "--author="+author+" <author@example.invalid>", "-m", message)
	return git(t, root, "rev-parse", "HEAD")
}

// prList is a fake `gh pr list` answer: one merged pull request per commit
// with the given body.
func prList(commits, bodies []string) GH {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		var prs []string
		for i, c := range commits {
			prs = append(prs, fmt.Sprintf(`{"number":%d,"body":%q,"mergeCommit":{"oid":%q},"closingIssuesReferences":[]}`, i+1, bodies[i], c))
		}
		return []byte("[" + strings.Join(prs, ",") + "]"), nil
	}
}

const privateText = "Ask Robin Sample or Casey Placeholder at robin.sample@example.invalid or <casey@example.invalid> before you change the cache."

func TestCollectSanitizesEverySource(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md", "# Plan\n\n### Task 1: Track lines\n\n"+privateText+"\n")
	write(t, root, "docs/adr/0001-cache.md", "# Cache\n\n"+privateText+"\n")
	commitAs(t, root, "Robin Sample", "Casey Placeholder", "start")
	write(t, root, "plan.go", lines("p", 12))
	cPlan := commit(t, root, "track lines")
	write(t, root, "spec.go", lines("s", 12))
	cSpec := commit(t, root, "implement 0001-cache.md")
	write(t, root, "pr.go", lines("r", 12))
	cPR := commit(t, root, "pull request change")
	write(t, root, "issue.go", lines("i", 12))
	cIssue := commit(t, root, "issue change")
	write(t, root, "message.go", lines("m", 12))
	cMessage := commit(t, root, "message change\n\n"+privateText+"\n\nCo-Authored-By: Casey Placeholder <casey@example.invalid>")
	write(t, root, ".superpowers/sdd/2026-01-01-x/progress.md", fmt.Sprintf("Task 1: complete (commits %s..%s)\n", cPlan[:7], cPlan[:7]))
	gh := func(ctx context.Context, args ...string) ([]byte, error) {
		return []byte(fmt.Sprintf(`[{"number":7,"body":%q,"mergeCommit":{"oid":%q},"closingIssuesReferences":[]},`+
			`{"number":8,"body":"Change the issue.","mergeCommit":{"oid":%q},"closingIssuesReferences":[{"number":3,"body":%q}]}]`,
			privateText, cPR, cIssue, privateText)), nil
	}

	candidates, _ := collect(t, root, gh)
	for _, source := range []struct{ name, commit string }{
		{"plan", cPlan}, {"spec", cSpec}, {"pr", cPR}, {"issue", cIssue}, {"commit", cMessage},
	} {
		k := only(t, candidates, source.commit)
		if k.StatementSource != source.name {
			t.Errorf("source %q, want %q", k.StatementSource, source.name)
			continue
		}
		for _, leak := range []string{"@", "example.invalid", "Robin Sample", "Casey Placeholder", "Co-Authored-By"} {
			if strings.Contains(k.Statement, leak) {
				t.Errorf("%s statement %q contains %q", source.name, k.Statement, leak)
			}
		}
		if !strings.Contains(k.Statement, "before you change the cache") || !k.NeedsRewrite {
			t.Errorf("%s statement %q NeedsRewrite %v, want the cleaned text marked for a rewrite", source.name, k.Statement, k.NeedsRewrite)
		}
	}
}

func TestCollectFlagsPrivateFigures(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	bodies := []string{
		"Keep the cache under a budget of $42 a month.",
		"Spend at most 3.50 USD on the cache.",
		"Stop the cache at quota 75% of the week.",
		"Stop when the cache reaches 90% of the weekly limit.",
		"The last cache run used 120K tokens.",
		"Cache the parsed config between calls.",
	}
	var commits []string
	for i := range bodies {
		write(t, root, fmt.Sprintf("f%d.go", i), lines("f", 12))
		commits = append(commits, commit(t, root, fmt.Sprintf("change %d", i)))
	}

	candidates, _ := collect(t, root, prList(commits, bodies))
	for i, c := range commits {
		k := only(t, candidates, c)
		want := i < len(bodies)-1
		if k.StatementSource != "pr" || k.Statement != bodies[i] || k.NeedsRewrite != want {
			t.Errorf("source %q statement %q NeedsRewrite %v, want %v", k.StatementSource, k.Statement, k.NeedsRewrite, want)
		}
	}
}

func TestRedactUsesHexBoundaries(t *testing.T) {
	sha := "3f2a9c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39"
	known := []string{sha}
	for _, text := range []string{
		"See commit_" + sha + "_details.",
		"See commit_" + sha[:7] + "_details.",
		"See https://example.invalid/x/commit/" + sha + ".",
		"Follows (" + sha[:9] + "), then " + sha[:12] + "; done.",
		"Follows " + strings.ToUpper(sha[:10]) + ".",
	} {
		got, leaked := redact(text, known, nil)
		if !leaked || strings.Contains(strings.ToLower(got), sha[:7]) {
			t.Errorf("redact(%q) = %q, %v", text, got, leaked)
		}
	}
	unrelated := "Keep deadbeef12, 0123456789abcdef, " + sha[:7] + "0 and " + sha[:6] + " as they are."
	if got, leaked := redact(unrelated, known, nil); got != unrelated || leaked {
		t.Errorf("redact(%q) = %q, %v", unrelated, got, leaked)
	}
}

func TestCollectFindsIDsBetweenWordCharacters(t *testing.T) {
	root := project(t)
	git(t, root, "remote", "add", "origin", "https://example.invalid/x.git")
	write(t, root, "a.txt", "a\n")
	c1 := commit(t, root, "start")
	write(t, root, "x.go", lines("x", 12))
	c2 := commit(t, root, "add x")
	write(t, root, "y.go", lines("y", 12))
	c3 := commit(t, root, "add y")
	bodies := []string{
		"Port the parser from commit_" + c1 + "_details to the reader.",
		"Keep the colour deadbeef12 and the id 0123456789abcdef as they are.",
	}

	candidates, _ := collect(t, root, prList([]string{c2, c3}, bodies))
	if k := only(t, candidates, c2); strings.Contains(k.Statement, c1[:7]) || !k.NeedsRewrite {
		t.Errorf("statement %q NeedsRewrite %v", k.Statement, k.NeedsRewrite)
	}
	if k := only(t, candidates, c3); k.Statement != bodies[1] || k.NeedsRewrite {
		t.Errorf("statement %q NeedsRewrite %v", k.Statement, k.NeedsRewrite)
	}
}

const cacheADR = "# Cache parsed config\n\nThe loader keeps the parsed config in memory.\n"

func TestCollectADRIgnoresLaterSolution(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	write(t, root, "docs/adr/0003-cache.md", cacheADR)
	commit(t, root, "add decision record")
	write(t, root, "cache.go", lines("c", 12))
	c3 := commit(t, root, "implement 0003-cache.md")
	write(t, root, "docs/adr/0003-cache.md", cacheADR+"\n## Solution\n\nA sync.Map keyed by path holds the config.\n")
	commit(t, root, "record the cache solution")

	k := only(t, candidates(t, root), c3)
	if k.StatementSource != "spec" || !strings.Contains(k.Statement, "keeps the parsed config") || strings.Contains(k.Statement, "sync.Map") {
		t.Fatalf("source %q statement %q", k.StatementSource, k.Statement)
	}
}

func TestCollectADRIgnoresWorkingTreeEdit(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	write(t, root, "docs/adr/0003-cache.md", cacheADR)
	commit(t, root, "start")
	write(t, root, "cache.go", lines("c", 12))
	c2 := commit(t, root, "implement 0003-cache.md")
	write(t, root, "docs/adr/0003-cache.md", cacheADR+"\nUse an LRU of 64 entries.\n")

	k := only(t, candidates(t, root), c2)
	if k.StatementSource != "spec" || !strings.Contains(k.Statement, "keeps the parsed config") || strings.Contains(k.Statement, "LRU") {
		t.Fatalf("source %q statement %q", k.StatementSource, k.Statement)
	}
}

func TestCollectPlanReadsVersionBeforeTask(t *testing.T) {
	root := project(t)
	plan := "# Plan\n\n### Task 1: Add parser\n\nWrite a parser for lines.\n"
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md", plan)
	commit(t, root, "start")
	write(t, root, "parse.go", lines("p", 12))
	c2 := commit(t, root, "add parser")
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md", plan+"\nSolution: split on tabs.\n")
	commit(t, root, "note the solution")
	write(t, root, "docs/superpowers/plans/2026-01-01-x.md", plan+"\nSolution: use bufio.\n")
	write(t, root, ".superpowers/sdd/2026-01-01-x/progress.md", fmt.Sprintf("Task 1: complete (commits %s..%s)\n", c2[:7], c2[:7]))

	k := only(t, candidates(t, root), c2)
	if k.StatementSource != "plan" || !strings.Contains(k.Statement, "Write a parser") || strings.Contains(k.Statement, "Solution") {
		t.Fatalf("source %q statement %q", k.StatementSource, k.Statement)
	}
}

func TestCollectGeneratedMarkAtEachRevision(t *testing.T) {
	root := project(t)
	write(t, root, "a.txt", "a\n")
	commit(t, root, "start")
	gen := "// Code generated by tool. DO NOT EDIT.\n\n"
	write(t, root, "manual.go", lines("m", 12))
	cManual := commit(t, root, "add manual")
	write(t, root, "manual.go", gen+lines("g", 12))
	cGenerated := commit(t, root, "generate manual")
	write(t, root, "tool.go", gen+lines("t", 12))
	cTool := commit(t, root, "add generated tool")
	write(t, root, "tool.go", lines("h", 12))
	cHand := commit(t, root, "rewrite tool by hand")
	write(t, root, "big.go", lines("b", 12))
	cBig := commit(t, root, "add big")
	write(t, root, ".gitattributes", "big.go linguist-generated\n")
	commit(t, root, "mark big generated")
	write(t, root, "big.go", lines("c", 12))
	cBigGenerated := commit(t, root, "change big")
	git(t, root, "rm", "-q", "manual.go")
	cDelete := commit(t, root, "drop manual")

	all := candidates(t, root)
	for _, c := range []struct{ name, commit, want string }{
		{"handwritten", cManual, ""},
		{"generated over handwritten", cGenerated, "generated or vendored only"},
		{"generated", cTool, "generated or vendored only"},
		{"handwritten over generated", cHand, ""},
		{"before the attribute", cBig, ""},
		{"after the attribute", cBigGenerated, "generated or vendored only"},
		{"deleting a generated file", cDelete, "generated or vendored only"},
	} {
		if k := only(t, all, c.commit); k.Excluded != c.want {
			t.Errorf("%s: Excluded %q, want %q", c.name, k.Excluded, c.want)
		}
	}
}

func candidates(t *testing.T, root string) []Candidate {
	t.Helper()
	found, _ := collect(t, root, nil)
	return found
}
