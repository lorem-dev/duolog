package calibration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorem-dev/duolog/internal/config"
	"github.com/lorem-dev/duolog/internal/journal"
)

// Candidate is one past change that may become a calibration task.
type Candidate struct {
	ID              string // "k-" + first 12 hex of sha256(sources)
	Parent, Ref     string // parent of the first commit; last commit
	Commits         []string
	Time            time.Time
	StatementSource string // "plan", "spec", "pr", "issue", "commit", "reconstructed"
	Statement       string
	NeedsRewrite    bool // "commit" and "reconstructed"
	Sources         []string
	Subject         string               // first message line; never shown to a candidate
	Files           []journal.FileChange // generated and vendored dropped
	Lines           int
	Components      []string
	Hidden          []string // test files the change adds or changes
	HiddenNames     []string
	Role            string
	Features        map[string]string
	Class           string
	Large           bool // inside the large stratum
	Excluded        string

	// Facts Filter reads; they never leave the package, so no author name
	// or message reaches a manifest.
	body       string
	author     string
	formatting bool
	generated  map[string]bool
	dropped    int
}

// GH runs the gh CLI with args in the project; nil skips pull requests.
type GH func(ctx context.Context, args ...string) ([]byte, error)

var (
	ledgerLine = regexp.MustCompile(`(?m)^Task (\d+): complete \(commits ([0-9a-f]{7,40})\.\.([0-9a-f]{7,40})\)`)
	// hexRun is a whole run of hex characters: an underscore or a letter
	// next to a commit id does not hide it, another hex digit does.
	hexRun       = regexp.MustCompile(`[0-9A-Fa-f]{7,}`)
	identifier   = regexp.MustCompile(`[A-Za-z_$][A-Za-z0-9_$]*`)
	declarations = []*regexp.Regexp{
		regexp.MustCompile(`^\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`),
		regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`),
		regexp.MustCompile(`^\s*class\s+([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^\s*(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`),
		regexp.MustCompile(`^\s*export\s+(?:default\s+)?(?:async\s+)?(?:const|function|class)\s*\*?\s+([A-Za-z_$][\w$]*)`),
	}
)

// specDirs hold ADRs, RFCs and design documents.
var specDirs = []string{"docs/adr", "docs/adrs", "docs/rfc", "docs/rfcs", "docs/design", "adr", "rfcs"}

// gitCommit is one commit of the first-parent history.
type gitCommit struct {
	sha, parent, author, subject, body string
	time                               time.Time
	files                              []journal.FileChange
}

// Collect lists every first-parent commit of root as part of one candidate:
// a plan task's ledger range is one candidate, every other commit its own.
// Each candidate carries the best statement found, is filtered with the
// default bounds and classified. Notes say what was skipped and why.
func Collect(ctx context.Context, root string, cfg *config.Config, gh GH, now time.Time) ([]Candidate, []string, error) {
	commits, err := firstParentLog(ctx, root)
	if err != nil || len(commits) == 0 {
		return nil, nil, err
	}
	index := map[string]int{}
	for i, c := range commits {
		index[c.sha] = i
	}
	known, names, err := history(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	blobs, err := openBlobs(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	defer blobs.close()

	var notes []string
	candidates, claimed, ledgerNotes, err := ledgerCandidates(ctx, root, blobs, commits, index)
	if err != nil {
		return nil, nil, err
	}
	notes = append(notes, ledgerNotes...)
	for i := range commits {
		if !claimed[commits[i].sha] {
			candidates = append(candidates, single(commits[i]))
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool { return index[candidates[i].Ref] < index[candidates[j].Ref] })

	byCommit := map[string]*Candidate{}
	for i := range candidates {
		for _, c := range candidates[i].Commits {
			byCommit[c] = &candidates[i]
		}
	}
	if err := applySpecs(root, blobs, commits, byCommit); err != nil {
		return nil, nil, err
	}
	if gh != nil {
		remotes, err := run(ctx, root, "remote")
		if err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(string(remotes)) != "" {
			notes = append(notes, applyPRs(ctx, gh, byCommit)...)
		}
	}

	subjects := map[string]string{}
	bodies := map[string]string{}
	for _, c := range commits {
		subjects[c.sha], bodies[c.sha] = c.subject, c.body
	}
	clean := newPrivacy(names)
	for i := range candidates {
		k := &candidates[i]
		if k.StatementSource == "" {
			if len(k.Commits) == 1 {
				k.Statement = bodies[k.Commits[0]]
			}
			k.StatementSource = "commit"
		}
		var subs []string
		for _, c := range k.Commits {
			subs = append(subs, subjects[c])
		}
		// Every source passes the same code removal, redaction and
		// privacy policy before the statement leaves Collect.
		var leaked, private bool
		before := len(strings.Fields(k.Statement))
		k.Statement = stripCode(k.Statement)
		thin := thinAfterCode(before, len(strings.Fields(k.Statement)))
		k.Statement, leaked = redact(k.Statement, known, subs)
		k.Statement, private = clean.sanitize(k.Statement)
		if k.Statement == "" {
			k.StatementSource = "reconstructed"
		}
		k.NeedsRewrite = thin || leaked || private || k.StatementSource == "commit" || k.StatementSource == "reconstructed"
		k.Sources = append(k.Sources, prefixed("commit ", k.Commits)...)
		sum := sha256.Sum256([]byte(strings.Join(k.Sources, "\n")))
		k.ID = "k-" + hex.EncodeToString(sum[:])[:12]
	}

	if err := markFormatting(ctx, root, candidates); err != nil {
		return nil, nil, err
	}
	if err := markGenerated(ctx, root, blobs, candidates); err != nil {
		return nil, nil, err
	}
	for i := range candidates {
		k := &candidates[i]
		Filter(k, cfg.Calibration.Filter, false)
		if err := hiddenTests(ctx, root, blobs, k); err != nil {
			return nil, nil, err
		}
		// A candidate cannot guess a name the hidden tests call, so a
		// statement from any source must name them all.
		if missingName(k.Statement, k.HiddenNames) != "" {
			k.NeedsRewrite = true
		}
		Classify(k, cfg)
	}
	return candidates, notes, nil
}

// missingName returns the first of names that statement does not hold as a
// whole identifier, or "".
func missingName(statement string, names []string) string {
	words := map[string]bool{}
	for _, w := range identifier.FindAllString(statement, -1) {
		words[w] = true
	}
	for _, name := range names {
		if !words[name] {
			return name
		}
	}
	return ""
}

// firstParentLog reads the first-parent history without merges, newest
// first, with the changed lines of each file.
func firstParentLog(ctx context.Context, root string) ([]gitCommit, error) {
	out, err := run(ctx, root, "-c", "core.quotePath=false", "log", "--first-parent", "--no-merges",
		"--numstat", "--no-renames", "--format=%x1e%H%x1f%P%x1f%ct%x1f%an%x1f%B%x1f")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var commits []gitCommit
	for _, record := range strings.Split(string(out), "\x1e") {
		fields := strings.SplitN(record, "\x1f", 5)
		if len(fields) < 5 {
			continue
		}
		cut := strings.LastIndex(fields[4], "\x1f")
		message, stat := fields[4][:cut], fields[4][cut+1:]
		seconds, _ := strconv.ParseInt(fields[2], 10, 64)
		c := gitCommit{sha: fields[0], author: fields[3], time: time.Unix(seconds, 0).UTC(), files: numstat(stat)}
		if parents := strings.Fields(fields[1]); len(parents) > 0 {
			c.parent = parents[0]
		}
		subject, body, _ := strings.Cut(strings.TrimSpace(message), "\n")
		c.subject, c.body = strings.TrimSpace(subject), strings.TrimSpace(body)
		commits = append(commits, c)
	}
	return commits, nil
}

func numstat(text string) []journal.FileChange {
	var files []journal.FileChange
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, _ := strconv.Atoi(parts[0])
		removed, _ := strconv.Atoi(parts[1])
		files = append(files, journal.FileChange{Path: unquote(parts[2]), Added: added, Removed: removed})
	}
	return files
}

// unquote reads a path git wrote in C quotes because of special characters.
func unquote(name string) string {
	if strings.HasPrefix(name, `"`) {
		if s, err := strconv.Unquote(name); err == nil {
			return s
		}
	}
	return name
}

// history returns every commit reachable from a ref, sorted, so a
// statement can be checked for a commit id, and the author and committer
// names of those commits, so a statement can lose them. A bot or service
// identity is not a person and gives no name: a task about GitHub need not
// lose the word because the web interface merged a commit.
func history(ctx context.Context, root string) (shas, names []string, err error) {
	out, err := run(ctx, root, "log", "--all", "--format=%H%x1f%an%x1f%ae%x1f%cn%x1f%ce")
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, "\x1f")
		if len(fields) != 5 {
			continue
		}
		shas = append(shas, fields[0])
		for _, who := range [][2]string{{fields[1], fields[2]}, {fields[3], fields[4]}} {
			if name := who[0]; !seen[name] && !serviceIdentity(name, who[1]) {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(shas)
	return shas, names, nil
}

// serviceIdentity reports whether a name and email belong to a bot or a
// service rather than a person: a name ending in [bot], or a noreply address
// of the service itself, such as noreply@github.com. A person's own noreply
// address, such as 12345+name@users.noreply.github.com, is still a person.
func serviceIdentity(name, email string) bool {
	if strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), "[bot]") {
		return true
	}
	local, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	return local == "noreply" || local == "no-reply" || local == "no_reply"
}

func single(c gitCommit) Candidate {
	return Candidate{Parent: c.parent, Ref: c.sha, Commits: []string{c.sha}, Time: c.time,
		Subject: c.subject, Files: c.files, body: c.body, author: c.author}
}

// ledgerCandidates turns each completed task of an SDD ledger into one
// candidate over its inclusive commit range, with the plan's task section
// as statement. The plan is read as it was before the range, from the
// parent tree, so a solution noted in it later, or an edit not yet
// committed, never reaches the statement.
func ledgerCandidates(ctx context.Context, root string, blobs *blobs, commits []gitCommit, index map[string]int) ([]Candidate, map[string]bool, []string, error) {
	ledgers, err := filepath.Glob(filepath.Join(root, ".superpowers", "sdd", "*", "progress.md"))
	if err != nil {
		return nil, nil, nil, err
	}
	sort.Strings(ledgers)
	claimed := map[string]bool{}
	var candidates []Candidate
	var notes []string
	for _, ledger := range ledgers {
		name := filepath.Base(filepath.Dir(ledger))
		rel := ".superpowers/sdd/" + name + "/progress.md"
		data, err := os.ReadFile(ledger)
		if err != nil {
			return nil, nil, nil, err
		}
		planRel := "docs/superpowers/plans/" + name + ".md"
		for _, m := range ledgerLine.FindAllStringSubmatch(string(data), -1) {
			task, _ := strconv.Atoi(m[1])
			first, last := resolve(ctx, root, m[2]), resolve(ctx, root, m[3])
			i, okFirst := index[first]
			j, okLast := index[last]
			if !okFirst || !okLast || j > i {
				notes = append(notes, fmt.Sprintf("%s: task %d: commits %s..%s are not a range of the first-parent history", rel, task, m[2], m[3]))
				continue
			}
			var shas []string
			for n := i; n >= j; n-- {
				shas = append(shas, commits[n].sha)
			}
			if slices.ContainsFunc(shas, func(s string) bool { return claimed[s] }) {
				notes = append(notes, fmt.Sprintf("%s: task %d: commits %s..%s overlap another task", rel, task, m[2], m[3]))
				continue
			}
			for _, s := range shas {
				claimed[s] = true
			}
			// The last commit speaks for the range; any message may revert.
			k := Candidate{Parent: commits[i].parent, Ref: last, Commits: shas, Time: commits[j].time,
				Subject: commits[j].subject, author: commits[j].author}
			var bodies []string
			for n := i; n >= j; n-- {
				bodies = append(bodies, commits[n].body)
			}
			k.body = strings.Join(bodies, "\n")
			if k.Files, err = rangeFiles(ctx, root, commits[j:i+1]); err != nil {
				return nil, nil, nil, err
			}
			var plan []byte
			planOK := false
			if k.Parent != "" {
				if plan, planOK, err = blobs.read(k.Parent, planRel, -1); err != nil {
					return nil, nil, nil, err
				}
			}
			if section, ok := planTask(string(plan), task); planOK && ok {
				k.StatementSource, k.Statement = "plan", section
				k.Sources = []string{fmt.Sprintf("plan %s task %d", planRel, task)}
			} else {
				notes = append(notes, fmt.Sprintf("%s: task %d: no section in %s before the task", rel, task, planRel))
				k.StatementSource = "reconstructed"
			}
			candidates = append(candidates, k)
		}
	}
	return candidates, claimed, notes, nil
}

// resolve returns the full id of an abbreviated commit, or "".
func resolve(ctx context.Context, root, short string) string {
	out, err := run(ctx, root, "rev-parse", "--verify", "--quiet", short+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rangeFiles returns the files changed by commits, given newest first: the
// diff from the oldest one's parent to the newest one.
func rangeFiles(ctx context.Context, root string, commits []gitCommit) ([]journal.FileChange, error) {
	oldest, newest := commits[len(commits)-1], commits[0]
	if len(commits) == 1 {
		return oldest.files, nil
	}
	if oldest.parent == "" {
		// Excluded with no parent anyway; the sum of the commits will do.
		sums := map[string]*journal.FileChange{}
		var files []journal.FileChange
		for _, c := range commits {
			for _, fc := range c.files {
				if s, ok := sums[fc.Path]; ok {
					s.Added, s.Removed = s.Added+fc.Added, s.Removed+fc.Removed
				} else {
					files = append(files, fc)
					sums[fc.Path] = &files[len(files)-1]
				}
			}
		}
		return slices.Clone(files), nil
	}
	out, err := run(ctx, root, "-c", "core.quotePath=false", "diff", "--numstat", "--no-renames",
		"--no-ext-diff", "--no-textconv", oldest.parent, newest.sha)
	if err != nil {
		return nil, err
	}
	return numstat(string(out)), nil
}

// planTask returns the "### Task n:" section of a plan. A heading inside a
// fenced code block does not end it; Collect removes the blocks.
func planTask(plan string, n int) (string, bool) {
	head := fmt.Sprintf("### Task %d:", n)
	lines := strings.Split(plan, "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, head) })
	if start < 0 {
		return "", false
	}
	var out []string
	fence := ""
	for i, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if closesFence(trimmed, fence) {
				fence = ""
			}
		} else if marker := fenceMarker(trimmed); marker != "" {
			fence = marker
		} else if i > 0 && headingLevel(line) > 0 && headingLevel(line) <= 3 {
			break
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n")), true
}

// stripCode removes every fenced code block from a statement, an unclosed
// one to the end, and folds the blank lines left behind: a plan, a design
// document, an issue or a pull request may hold the solution as code.
func stripCode(statement string) string {
	var out []string
	fence := ""
	for _, line := range strings.Split(statement, "\n") {
		trimmed := strings.TrimSpace(line)
		if fence != "" {
			if closesFence(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if marker := fenceMarker(trimmed); marker != "" {
			fence = marker
			continue
		}
		if trimmed == "" && (len(out) == 0 || out[len(out)-1] == "") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// minStatementWords is the fewest words a statement that lost code keeps
// before it needs a rewrite.
const minStatementWords = 30

// thinAfterCode reports whether removing code took a statement of before
// words down to after words that may not say what to build: fewer than
// minStatementWords, or under half of what it was. A statement that lost
// no code is never thin. Code indented by four spaces is not a fence; it
// stays and counts as words.
func thinAfterCode(before, after int) bool {
	return after < before && (after < minStatementWords || 2*after < before)
}

// closesFence reports whether line ends a block opened by fence: the same
// character, at least as many times, and nothing else.
func closesFence(line, fence string) bool {
	return strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == ""
}

func fenceMarker(line string) string {
	for _, c := range []string{"`", "~"} {
		n := len(line) - len(strings.TrimLeft(line, c))
		if n >= 3 {
			return line[:n]
		}
	}
	return ""
}

func headingLevel(line string) int {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}

// applySpecs gives a candidate without a plan statement the text of an
// ADR, RFC or design document that one of its commits names by file name.
// The text is the document as it was before the candidate's first commit,
// read from the parent tree, so a solution added to it later, or an edit
// not yet committed, never reaches the statement. A document the parent
// lacks, such as one the candidate adds or an untracked one, gives none.
func applySpecs(root string, blobs *blobs, commits []gitCommit, byCommit map[string]*Candidate) error {
	var docs []string
	for _, dir := range specDirs {
		base := filepath.Join(root, filepath.FromSlash(dir))
		err := filepath.WalkDir(base, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				if name == base && os.IsNotExist(err) {
					return filepath.SkipDir
				}
				return err
			}
			switch strings.ToLower(path.Ext(d.Name())) {
			case ".md", ".markdown", ".rst", ".txt", ".adoc":
				if d.Type().IsRegular() {
					docs = append(docs, name)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	for _, doc := range docs {
		rel, err := filepath.Rel(root, doc)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		name := path.Base(rel)
		for _, c := range commits {
			k := byCommit[c.sha]
			if k.StatementSource != "" || k.Parent == "" || !strings.Contains(c.subject+"\n"+c.body, name) {
				continue
			}
			data, ok, err := blobs.read(k.Parent, rel, -1)
			if err != nil {
				return err
			}
			text := strings.TrimSpace(string(data))
			if !ok || text == "" {
				continue
			}
			k.StatementSource, k.Statement = "spec", text
			k.Sources = []string{"spec " + rel}
		}
	}
	return nil
}

type pullRequest struct {
	Number      int    `json:"number"`
	Body        string `json:"body"`
	MergeCommit *struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
	ClosingIssuesReferences []struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	} `json:"closingIssuesReferences"`
}

// applyPRs gives a candidate without a plan or spec statement the text of
// the merged pull request whose merge commit it holds, or of the issue that
// pull request closes.
func applyPRs(ctx context.Context, gh GH, byCommit map[string]*Candidate) []string {
	out, err := gh(ctx, "pr", "list", "--state", "merged", "--limit", "200", "--json", "number,body,mergeCommit,closingIssuesReferences")
	var prs []pullRequest
	if err == nil {
		err = json.Unmarshal(out, &prs)
	}
	if err != nil {
		return []string{fmt.Sprintf("gh: skipped (%v)", err)}
	}
	var notes []string
	for _, pr := range prs {
		if pr.MergeCommit == nil {
			continue
		}
		k := byCommit[pr.MergeCommit.OID]
		if k == nil || k.StatementSource != "" {
			continue
		}
		for _, issue := range pr.ClosingIssuesReferences {
			body := strings.TrimSpace(issue.Body)
			if body == "" {
				data, err := gh(ctx, "issue", "view", strconv.Itoa(issue.Number), "--json", "body")
				var view struct {
					Body string `json:"body"`
				}
				if err == nil {
					err = json.Unmarshal(data, &view)
				}
				if err != nil {
					notes = append(notes, fmt.Sprintf("gh: issue #%d skipped (%v)", issue.Number, err))
					continue
				}
				body = strings.TrimSpace(view.Body)
			}
			if body != "" {
				k.StatementSource, k.Statement = "issue", body
				k.Sources = []string{fmt.Sprintf("pr #%d", pr.Number), fmt.Sprintf("issue #%d", issue.Number)}
				break
			}
		}
		if body := strings.TrimSpace(pr.Body); k.StatementSource == "" && body != "" {
			k.StatementSource, k.Statement = "pr", body
			k.Sources = []string{fmt.Sprintf("pr #%d", pr.Number)}
		}
	}
	return notes
}

// redact removes every commit id prefix and every subject line from a
// statement and reports whether it found one.
func redact(statement string, known []string, subjects []string) (string, bool) {
	leaked := false
	statement = hexRun.ReplaceAllStringFunc(statement, func(word string) string {
		lower := strings.ToLower(word)
		i := sort.SearchStrings(known, lower)
		if i < len(known) && strings.HasPrefix(known[i], lower) {
			leaked = true
			return ""
		}
		return word
	})
	for _, subject := range subjects {
		if subject == "" {
			continue
		}
		// Bounded by words, so a short subject such as "fix" does not
		// match inside "prefix".
		pattern := regexp.QuoteMeta(subject)
		if wordByte(subject[0]) {
			pattern = `\b` + pattern
		}
		if wordByte(subject[len(subject)-1]) {
			pattern += `\b`
		}
		re := regexp.MustCompile(pattern)
		if re.MatchString(statement) {
			leaked = true
			statement = re.ReplaceAllLiteralString(statement, "")
		}
	}
	return strings.TrimSpace(statement), leaked
}

func wordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func prefixed(prefix string, items []string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = prefix + item
	}
	return out
}

// markFormatting sets formatting on candidates whose change is empty when
// whitespace and blank lines are ignored: one log pass for single commits,
// one diff per range.
func markFormatting(ctx context.Context, root string, candidates []Candidate) error {
	substantive := map[string]bool{}
	current := ""
	err := stream(ctx, root, func(line []byte) {
		if sha, ok := bytes.CutPrefix(line, []byte("\x1e")); ok {
			current = string(sha)
		} else if len(bytes.TrimSpace(line)) > 0 {
			substantive[current] = true
		}
	}, "log", "--first-parent", "--no-merges", "-p", "-w", "--ignore-blank-lines", "--no-renames",
		"--no-ext-diff", "--no-textconv", "--no-color", "--format=%x1e%H")
	if err != nil {
		return err
	}
	for i := range candidates {
		k := &candidates[i]
		if k.Parent == "" {
			continue
		}
		if len(k.Commits) == 1 {
			k.formatting = !substantive[k.Ref]
			continue
		}
		out, err := run(ctx, root, "diff", "-w", "--ignore-blank-lines", "--no-renames", "--no-ext-diff",
			"--no-textconv", "--no-color", k.Parent, k.Ref)
		if err != nil {
			return err
		}
		k.formatting = len(bytes.TrimSpace(out)) == 0
	}
	return nil
}

// stream runs git and hands each output line to fn without holding the
// whole output; a line longer than the buffer is cut, which is enough to
// see whether it is blank.
func stream(ctx context.Context, root string, fn func([]byte), args ...string) error {
	cmd := journal.GitCommand(ctx, root, args...)
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r := bufio.NewReaderSize(out, 64*1024)
	for {
		line, prefix, err := r.ReadLine()
		if err != nil {
			break
		}
		fn(line)
		for prefix && err == nil {
			_, prefix, err = r.ReadLine()
		}
	}
	_, _ = io.Copy(io.Discard, out)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// spot is a file at a revision.
type spot struct{ rev, path string }

// markGenerated records the files of each candidate that .gitattributes
// marks linguist-generated or whose first five lines say "Code generated"
// and "DO NOT EDIT". Both are read at the candidate's own revision, its
// last commit, or its parent for a file the change deletes, so a later
// version of the file or of the attributes does not decide for it.
func markGenerated(ctx context.Context, root string, blobs *blobs, candidates []Candidate) error {
	type mark struct{ present, generated bool }
	marks := map[spot]mark{}
	look := func(s spot) (mark, error) {
		if m, ok := marks[s]; ok {
			return m, nil
		}
		head, ok, err := blobs.read(s.rev, s.path, 4096)
		if err != nil {
			return mark{}, err
		}
		m := mark{present: ok, generated: ok && generatedHead(head)}
		marks[s] = m
		return m, nil
	}
	at := make([]map[string]spot, len(candidates))
	var spots []spot
	asked := map[spot]bool{}
	for i, k := range candidates {
		for _, fc := range k.Files {
			if vendored(fc.Path) || generatedName(fc.Path) {
				continue
			}
			s := spot{k.Ref, fc.Path}
			m, err := look(s)
			if err != nil {
				return err
			}
			if !m.present && k.Parent != "" {
				s.rev = k.Parent
				if _, err := look(s); err != nil {
					return err
				}
			}
			if at[i] == nil {
				at[i] = map[string]spot{}
			}
			at[i][fc.Path] = s
			if !asked[s] {
				asked[s] = true
				spots = append(spots, s)
			}
		}
	}
	attributed, err := generatedAttributes(ctx, root, spots)
	if err != nil {
		return err
	}
	for i := range candidates {
		for name, s := range at[i] {
			if marks[s].generated || attributed[s] {
				if candidates[i].generated == nil {
					candidates[i].generated = map[string]bool{}
				}
				candidates[i].generated[name] = true
			}
		}
	}
	return nil
}

func generatedHead(head []byte) bool {
	lines := strings.SplitN(string(head), "\n", 6)
	first := strings.Join(lines[:min(5, len(lines))], "\n")
	return strings.Contains(first, "Code generated") && strings.Contains(first, "DO NOT EDIT")
}

// generatedAttributes returns the spots whose .gitattributes, at the spot's
// revision, set linguist-generated. A history without any .gitattributes
// file needs one check for all paths, since only attributes outside the
// tree apply; otherwise each revision is checked with its own tree.
func generatedAttributes(ctx context.Context, root string, spots []spot) (map[spot]bool, error) {
	attributed := map[spot]bool{}
	if len(spots) == 0 {
		return attributed, nil
	}
	out, err := run(ctx, root, "log", "--all", "-1", "--format=%H", "--", ":(glob)**/.gitattributes")
	if err != nil {
		return nil, err
	}
	shared := len(bytes.TrimSpace(out)) == 0
	checked := func(s spot) spot {
		if shared {
			s.rev = spots[0].rev
		}
		return s
	}
	byRev := map[string][]string{}
	var revs []string
	for _, s := range spots {
		c := checked(s)
		if _, ok := byRev[c.rev]; !ok {
			revs = append(revs, c.rev)
		}
		byRev[c.rev] = append(byRev[c.rev], c.path)
	}
	set := map[spot]bool{}
	for _, rev := range revs {
		cmd := journal.GitCommand(ctx, root, "check-attr", "-z", "--stdin", "--source="+rev, "linguist-generated")
		cmd.Env = gitEnv()
		cmd.Stdin = strings.NewReader(strings.Join(byRev[rev], "\x00") + "\x00")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git check-attr: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		fields := strings.Split(string(out), "\x00")
		for i := 0; i+2 < len(fields); i += 3 {
			if v := fields[i+2]; v == "set" || v == "true" {
				set[spot{rev, fields[i]}] = true
			}
		}
	}
	for _, s := range spots {
		attributed[s] = set[checked(s)]
	}
	return attributed, nil
}

// hiddenTests sets the test files k adds or changes and the names its other
// added lines declare that those tests use.
func hiddenTests(ctx context.Context, root string, blobs *blobs, k *Candidate) error {
	words := map[string]bool{}
	for _, fc := range k.Files {
		if !testFile(fc.Path) {
			continue
		}
		data, ok, err := blobs.read(k.Ref, fc.Path, -1)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		k.Hidden = append(k.Hidden, fc.Path)
		for _, w := range identifier.FindAllString(string(data), -1) {
			words[w] = true
		}
	}
	sort.Strings(k.Hidden)
	if len(k.Hidden) == 0 || k.Parent == "" {
		return nil
	}
	code := map[string]bool{}
	for _, fc := range k.Files {
		if !testFile(fc.Path) {
			code[fc.Path] = true
		}
	}
	names := map[string]bool{}
	current, header := "", false
	err := stream(ctx, root, func(line []byte) {
		text := string(line)
		switch {
		case strings.HasPrefix(text, "diff --git "):
			current, header = "", true
		case header && strings.HasPrefix(text, "+++ "):
			current, header = strings.TrimPrefix(unquote(strings.TrimPrefix(text, "+++ ")), "b/"), false
		case strings.HasPrefix(text, "@@"):
			header = false
		case !header && strings.HasPrefix(text, "+") && code[current]:
			for _, re := range declarations {
				if m := re.FindStringSubmatch(text[1:]); m != nil && words[m[1]] {
					names[m[1]] = true
				}
			}
		}
	}, "-c", "core.quotePath=false", "diff", "-U0", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color",
		"--src-prefix=a/", "--dst-prefix=b/", k.Parent, k.Ref)
	if err != nil {
		return err
	}
	k.HiddenNames = nil
	for name := range names {
		k.HiddenNames = append(k.HiddenNames, name)
	}
	sort.Strings(k.HiddenNames)
	return nil
}

// blobs reads files at a commit through one `git cat-file --batch`.
type blobs struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func openBlobs(ctx context.Context, root string) (*blobs, error) {
	cmd := journal.GitCommand(ctx, root, "cat-file", "--batch")
	cmd.Env = gitEnv()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &blobs{cmd: cmd, in: in, out: bufio.NewReader(out)}, nil
}

// read returns up to limit bytes (all with a negative limit) of name at
// rev, and false when rev has no such file.
func (b *blobs) read(rev, name string, limit int) ([]byte, bool, error) {
	if strings.ContainsAny(name, "\n") {
		return nil, false, nil
	}
	if _, err := fmt.Fprintf(b.in, "%s:%s\n", rev, name); err != nil {
		return nil, false, err
	}
	header, err := b.out.ReadString('\n')
	if err != nil {
		return nil, false, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || strings.HasSuffix(header, " missing\n") {
		return nil, false, nil
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, false, fmt.Errorf("git cat-file: unexpected header %q", header)
	}
	keep := size
	if limit >= 0 && limit < size {
		keep = limit
	}
	data := make([]byte, keep)
	if _, err := io.ReadFull(b.out, data); err != nil {
		return nil, false, err
	}
	if _, err := io.CopyN(io.Discard, b.out, int64(size-keep)+1); err != nil {
		return nil, false, err
	}
	return data, fields[1] == "blob", nil
}

func (b *blobs) close() {
	_ = b.in.Close()
	_ = b.cmd.Wait()
}
