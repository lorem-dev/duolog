package duolog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// agentNames are the names of today's agents. Outside agentNameHomes, code
// reaches an agent through its descriptor in internal/wiring, never by name.
var agentNames = []string{"claude", "codex"}

// Reasons of the places that may name an agent.
const (
	reasonAgentAdapter = "the agent's own adapter"
	reasonModeTables   = "the mode tables per agent and PinCodexSandbox stay in core/mode (settled by the person)"
	reasonBoundary     = "security boundary: moves only; its function bodies do not change before the 1.6.0 ports"
	reasonKept         = "kept for the boundary until the 1.6.0 ports"
)

// agentNameHomes are the folders and files whose non-test code may hold a
// string literal equal to an agent's name, each with its reason. A folder
// covers the packages below it.
var agentNameHomes = map[string]string{
	"internal/adapter/claude":    reasonAgentAdapter,
	"internal/adapter/codex":     reasonAgentAdapter,
	"internal/core/mode":         reasonModeTables,
	"internal/core/policy":       reasonBoundary,
	"internal/core/paidrules":    reasonBoundary,
	"internal/app/gate":          reasonBoundary,
	"internal/app/paidruns":      reasonBoundary,
	"internal/adapter/paidstore": reasonBoundary,
	"cmd/duolog/gate.go":         reasonBoundary,
	"cmd/duolog/leadguard.go":    reasonBoundary,
	"internal/workerenv":         reasonKept,
	"internal/quota":             reasonKept,
	// ReadLeadState and CodexMode; ValidSessionID.
	"internal/adapter/session/leadstate.go": reasonKept,
	"internal/adapter/session/register.go":  reasonKept,
}

// reasonPending is the reason of every file in pendingAgentNames.
const reasonPending = "moves to the agent descriptors in 1.6.0"

// pendingAgentNames are the files outside agentNameHomes that still name an
// agent, keyed by file. The list is closed and may only shrink: a file leaves
// it when its names move to the agent descriptors, and none joins it.
var pendingAgentNames = map[string]string{
	"cmd/duolog/calibrate.go":                reasonPending,
	"cmd/duolog/hook.go":                     reasonPending,
	"cmd/duolog/roster.go":                   reasonPending,
	"cmd/duolog/skill.go":                    reasonPending,
	"cmd/duolog/sync.go":                     reasonPending,
	"cmd/duolog/telemetry.go":                reasonPending,
	"internal/adapter/config/agents.go":      reasonPending,
	"internal/adapter/config/workermodes.go": reasonPending,
	"internal/adapter/kit/caps.go":           reasonPending,
	"internal/app/delegate/run.go":           reasonPending,
	"internal/app/delegate/snapshot.go":      reasonPending,
	"internal/app/delegate/workermode.go":    reasonPending,
	"internal/app/doctor/catalog.go":         reasonPending,
	"internal/app/doctor/doctor.go":          reasonPending,
	"internal/app/doctor/workermodes.go":     reasonPending,
	"internal/app/hook/compact.go":           reasonPending,
	"internal/app/hook/hook.go":              reasonPending,
	"internal/app/quota/calibration.go":      reasonPending,
	"internal/app/quota/observe.go":          reasonPending,
	"internal/app/quota/report.go":           reasonPending,
	"internal/app/sync/sync.go":              reasonPending,
	"internal/core/agentmodel/agentmodel.go": reasonPending,
	"internal/core/budget/budget.go":         reasonPending,
	"internal/core/config/learning.go":       reasonPending,
	"internal/core/config/modelrows.go":      reasonPending,
	"internal/core/config/routing.go":        reasonPending,
	"internal/core/config/workermodes.go":    reasonPending,
	"internal/core/route/catalog.go":         reasonPending,
}

// maxPendingAgentNames is the length of pendingAgentNames. It may only go
// down.
const maxPendingAgentNames = 28

// TestAgentNames fails on a string literal equal to an agent's name in a
// non-test Go file outside agentNameHomes and pendingAgentNames. Test-tier
// folders are skipped. Comments are free.
func TestAgentNames(t *testing.T) {
	fset := token.NewFileSet()
	found := map[string][]string{}
	err := filepath.WalkDir(".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".cache", ".codegraph", ".superpowers", "node_modules", "testdata":
				return filepath.SkipDir
			}
			if testTier(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(name)
		for _, lit := range agentNameLiterals(file) {
			found[rel] = append(found[rel], strconv.Itoa(fset.Position(lit.Pos()).Line))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, rel := range slices.Sorted(maps.Keys(found)) {
		if home := agentNameHome(rel); home != "" {
			used[home] = true
			continue
		}
		if _, pending := pendingAgentNames[rel]; pending {
			continue
		}
		t.Errorf("%s:%s names an agent; take the name from its agent.Descriptor", rel, strings.Join(found[rel], ","))
	}
	for _, rel := range slices.Sorted(maps.Keys(pendingAgentNames)) {
		if pendingAgentNames[rel] != reasonPending {
			t.Errorf("%s: the reason must be %q", rel, reasonPending)
		}
		if _, ok := found[rel]; !ok {
			t.Errorf("%s names no agent any more; delete it from pendingAgentNames and lower maxPendingAgentNames", rel)
		}
	}
	if len(pendingAgentNames) != maxPendingAgentNames {
		t.Errorf("pendingAgentNames has %d entries; set maxPendingAgentNames to that number (it may only go down)", len(pendingAgentNames))
	}
	for _, home := range slices.Sorted(maps.Keys(agentNameHomes)) {
		if strings.TrimSpace(agentNameHomes[home]) == "" {
			t.Errorf("%s: no reason", home)
		}
		if !used[home] {
			t.Errorf("%s names no agent any more; delete it from agentNameHomes", home)
		}
	}
}

// TestAgentNameLiteralsFindsOnlyLiterals keeps the scan honest: it reports a
// literal equal to an agent's name, in either quoting, and passes comments,
// identifiers and longer strings.
func TestAgentNameLiteralsFindsOnlyLiterals(t *testing.T) {
	src := "package p\n\n" +
		"// claude and \"codex\" in a comment are free.\n" +
		"var (\n" +
		"\ta = \"claude\"\n" +
		"\tb = `codex`\n" +
		"\tc = \"claude-code\"\n" +
		"\tcodex = 1\n" +
		")\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, lit := range agentNameLiterals(file) {
		got = append(got, lit.Value)
	}
	if want := []string{`"claude"`, "`codex`"}; !slices.Equal(got, want) {
		t.Errorf("literals = %v, want %v", got, want)
	}
	if got := agentNameHome("internal/adapter/claude/run.go"); got != "internal/adapter/claude" {
		t.Errorf("home of an adapter file = %q", got)
	}
	if got := agentNameHome("internal/adapter/claudex/run.go"); got != "" {
		t.Errorf("home of a folder that only starts like one = %q, want none", got)
	}
	for dir, want := range map[string]bool{
		"internal/app/delegate/delegatetest": true,
		"internal/adapter/workertest":        true,
		"internal/app/skilltest":             false,
		"internal/app/delegate":              false,
	} {
		if got := testTier(dir); got != want {
			t.Errorf("testTier(%q) = %v, want %v", dir, got, want)
		}
	}
}

// testTier reports whether the folder dir holds test-tier code: the fakes and
// contract tests in a folder whose name ends in "test", such as
// internal/app/delegate/delegatetest. An app package of appOrder, such as
// internal/app/skilltest, is a use case and is scanned.
func testTier(dir string) bool {
	rel := filepath.ToSlash(dir)
	base := path.Base(rel)
	if !strings.HasSuffix(base, "test") {
		return false
	}
	return rel != "internal/app/"+base || !slices.Contains(appOrder, base)
}

// agentNameLiterals returns the string literals of file that equal an agent's
// name.
func agentNameLiterals(file *ast.File) []*ast.BasicLit {
	var lits []*ast.BasicLit
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && slices.Contains(agentNames, s) {
			lits = append(lits, lit)
		}
		return true
	})
	return lits
}

// agentNameHome returns the entry of agentNameHomes that holds the
// module-relative file rel, or "".
func agentNameHome(rel string) string {
	for home := range agentNameHomes {
		if under(rel, home) {
			return home
		}
	}
	return ""
}
