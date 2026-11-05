package calibration

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Statements come from commits, plans, specs, ADRs, issues and pull
// requests, and a manifest takes them to Git, so one policy cleans them all.
var (
	// trailerLine is an author-like trailer such as Signed-off-by or
	// Co-Authored-By, with its line.
	trailerLine = regexp.MustCompile(`(?im)^[ \t]*[a-z][a-z0-9-]*-by:[^\n]*(?:\n|$)`)
	// emailAddress is an address in angle brackets, or a bare one whose
	// domain ends in letters, so a version such as pkg@1.2.3 stays.
	emailAddress = regexp.MustCompile(`<[^<>\s@]+@[^<>\s]+>|[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}\b`)
	// privateFigure is a money amount, a quota, usage or limit percentage,
	// or a token count: facts of a person's account, not of the project.
	privateFigure = regexp.MustCompile(`(?i)` +
		`[$\x{20AC}\x{00A3}\x{00A5}]\s?\d` +
		`|\b\d[\d,]*(?:\.\d+)?\s?(?:usd|eur|gbp|dollars?|euros?)\b` +
		`|\b(?:usd|eur|gbp)\s?\d` +
		`|\b(?:quota|usage|used|limits?|remaining|weekly|window|budget|spend|spent|credits?)\b[^\n.%]{0,30}?\d+(?:\.\d+)?\s?%` +
		`|\d+(?:\.\d+)?\s?%\s+(?:of\s+)?(?:(?:the|my|our|your|a|weekly|daily|monthly|hourly|five-hour)\s+)*(?:quota|usage|limits?|window|budget|allowance)\b` +
		`|\b\d[\d,.]*\s?[km]?\s+(?:(?:input|output|cached|prompt|completion)\s+)?tokens?\b`)
)

// privacy removes identities from a statement: trailer lines, email
// addresses and the names of the history's authors and committers.
type privacy struct {
	names *regexp.Regexp // nil when the history has no name worth removing
}

// newPrivacy matches every name as a whole, longest first, ignoring case.
// A one-letter name is left alone: removing it would cut ordinary words.
func newPrivacy(names []string) privacy {
	var alternatives []string
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if utf8.RuneCountInString(name) < 2 || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		alternatives = append(alternatives, name)
	}
	if len(alternatives) == 0 {
		return privacy{}
	}
	slices.SortStableFunc(alternatives, func(a, b string) int { return len(b) - len(a) })
	for i, name := range alternatives {
		pattern := regexp.QuoteMeta(name)
		if wordByte(name[0]) {
			pattern = `\b` + pattern
		}
		if wordByte(name[len(name)-1]) {
			pattern += `\b`
		}
		alternatives[i] = pattern
	}
	return privacy{names: regexp.MustCompile(`(?i)(?:` + strings.Join(alternatives, "|") + `)`)}
}

// sanitize returns statement without identities, and whether it needs a
// rewrite: it lost an identity, or it holds a private figure, which stays
// for the rewrite to drop with its sentence.
func (p privacy) sanitize(statement string) (string, bool) {
	cleaned := trailerLine.ReplaceAllString(statement, "")
	cleaned = emailAddress.ReplaceAllString(cleaned, "")
	if p.names != nil {
		cleaned = p.names.ReplaceAllString(cleaned, "")
	}
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, cleaned != strings.TrimSpace(statement) || privateFigure.MatchString(cleaned)
}

// check refuses text that the policy would change or flag: a name, an email
// address, an author-like trailer or a private figure. Text bound for Git
// fails closed rather than being cleaned on the way.
func (p privacy) check(text string) error {
	if cleaned, _ := p.sanitize(text); cleaned != strings.TrimSpace(text) {
		return errors.New("holds a name, an email address or an author trailer")
	}
	if privateFigure.MatchString(text) {
		return errors.New("holds a cost, quota, usage or token figure")
	}
	return nil
}

// HistoryNames returns the author and committer names of root's history,
// which no statement or shared file may hold.
func HistoryNames(ctx context.Context, root string) ([]string, error) {
	_, names, err := history(ctx, root)
	return names, err
}
