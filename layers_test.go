package duolog_test

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
)

const modulePath = "github.com/lorem-dev/duolog"

// tier is a layer of the code. A package may import packages of its own tier
// and of the tiers its rules allow; see docs/architecture.md.
type tier int

const (
	tierCore   tier = iota // no I/O: facts, types, decisions
	tierApp                // use cases; own the ports
	tierKit                // non-agent adapters
	tierAgent              // internal/adapter/claude, internal/adapter/codex
	tierWiring             // the agent registry
	tierCmd                // commands and scripts
	tierTest               // test helpers and fakes
)

// legacyTier is the tier of a package that has not moved yet.
type legacyTier struct {
	tier tier
	app  string
}

// legacyTiers gives today's packages the tier of the part that stays at their
// path after the migration. A row goes when its package moves.
var legacyTiers = map[string]legacyTier{
	"internal/mode":     {tier: tierCore},
	"internal/pathutil": {tier: tierCore},
	"internal/router":   {tier: tierCore},
	"internal/usage":    {tier: tierCore},
	"internal/learning": {tier: tierCore},

	"internal/gate": {tier: tierApp, app: "gate"},

	"internal/config":     {tier: tierKit},
	"internal/models":     {tier: tierKit},
	"internal/runner":     {tier: tierKit},
	"internal/rex":        {tier: tierKit},
	"internal/session":    {tier: tierKit},
	"internal/bus":        {tier: tierKit},
	"internal/journal":    {tier: tierKit},
	"internal/store":      {tier: tierKit},
	"internal/project":    {tier: tierKit},
	"internal/auth":       {tier: tierKit},
	"internal/trust":      {tier: tierKit},
	"internal/trusthome":  {tier: tierKit},
	"internal/install":    {tier: tierKit},
	"internal/coauthor":   {tier: tierKit},
	"internal/workerenv":  {tier: tierKit},
	"internal/quota":      {tier: tierKit},
	"internal/telemetry":  {tier: tierKit},
	"internal/artifact":   {tier: tierKit},
	"internal/capability": {tier: tierKit},
	"plugin":              {tier: tierKit},

	"internal/config/schemagen": {tier: tierWiring},
}

// appOrder is the order of the app packages, lowest first. An app package may
// import only app packages earlier in the list.
var appOrder = []string{"launch", "lead", "quota", "learn", "paidruns", "gate", "delegate", "profile", "peer", "calibrate", "doctor", "sync", "roster", "hook", "install", "remember", "skilltest"}

// knownViolations are the imports that break a rule today, keyed "from -> to",
// with the PR that removes each. The list may only shrink; a move may rename a
// key when the same import gets a new path at one end.
var knownViolations = map[string]string{
	"internal/adapter/config (test) -> internal/gate":           "PR 19",
	"internal/app/sync -> github.com/pelletier/go-toml/v2":      "PR 43",
	"internal/app/sync -> internal/adapter/models":              "PR 33",
	"internal/app/calibrate (test) -> internal/adapter/config":  "after PR 10: its tests load the config files",
	"internal/app/calibrate (test) -> internal/quota":           "PR 29",
	"internal/app/calibrate (test) -> internal/runner":          "PR 29",
	"internal/app/calibrate (test) -> internal/session":         "PR 29",
	"internal/app/calibrate (test) -> internal/wiring":          "PR 29: the cell tests run real delegations on wiring.DelegateDeps",
	"internal/app/calibrate -> github.com/pelletier/go-toml/v2": "PR 45",
	"internal/app/calibrate -> internal/install":                "PR 45",
	"internal/app/calibrate -> internal/trust":                  "PR 36",
	"internal/app/calibrate -> internal/trusthome":              "PR 36",
	"internal/app/calibrate -> internal/workerenv":              "PR 35",
	"internal/app/delegate -> internal/auth":                    "PR 38",
	"internal/app/delegate -> internal/capability":              "PR 40",
	"internal/app/delegate -> internal/adapter/config":          "after PR 10: RefuseRootEnv checks writable roots at launch",
	"internal/app/delegate (test) -> internal/adapter/models":   "PR 33",
	"internal/app/delegate (test) -> internal/quota":            "PR 39",
	"internal/app/delegate -> internal/runner":                  "PR 32",
	"internal/app/delegate -> internal/trust":                   "PR 36",
	"internal/app/delegate -> internal/trusthome":               "PR 36",
	"internal/app/delegate -> internal/workerenv":               "PR 35",
	"internal/app/delegate -> os/exec":                          "PR 32",
	"internal/app/doctor (test) -> internal/adapter/config":     "after PR 10: its tests load the config files",
	"internal/app/doctor (test) -> internal/quota":              "PR 39",
	"internal/app/doctor -> internal/capability":                "PR 40",
	"internal/app/doctor -> internal/install":                   "PR 37",
	"internal/app/doctor -> internal/trust":                     "PR 36",
	"internal/app/doctor -> internal/trusthome":                 "PR 36",
	"internal/app/doctor -> internal/workerenv":                 "PR 35",
	"internal/app/doctor -> os/exec":                            "PR 44",
	"internal/gate -> github.com/gofrs/flock":                   "PR 20, boundary",
	"internal/gate -> github.com/pelletier/go-toml/v2":          "PR 20, boundary",
	"internal/gate -> internal/bus":                             "PR 20, boundary",
	"internal/gate -> internal/config":                          "PR 20, boundary",
	"internal/gate -> internal/session":                         "PR 20, boundary",
	"internal/gate -> internal/store":                           "PR 20, boundary",
	"internal/gate -> internal/trust":                           "PR 20, boundary",
	"internal/gate -> internal/trusthome":                       "PR 20, boundary",
	"internal/app/hook -> internal/capability":                  "PR 40",
	"internal/app/hook -> internal/adapter/config":              "after PR 10: hooks load the config files",
	"internal/app/learn -> internal/adapter/config":             "after PR 10: learn loads the config files",
	"internal/core/learn -> go.yaml.in/yaml/v3":                 "PR 16: Render moves out once calibrate.go shrinks",
	"internal/core/learn (test) -> internal/adapter/config":     "after PR 10: its tests load the default config",
	"internal/core/learn -> internal/journal":                   "journal.Outcomes moves to core/record",
	"internal/app/paidruns -> github.com/gofrs/flock":           "PR 22, boundary",
	"internal/app/paidruns -> golang.org/x/sys/unix":            "PR 22, boundary; darwin only",
	"internal/app/paidruns -> golang.org/x/sys/windows":         "PR 22, boundary; windows only",
	"internal/app/paidruns -> internal/journal":                 "PR 22, boundary",
	"internal/app/paidruns -> internal/models":                  "PR 22, boundary",
	"internal/app/paidruns -> internal/quota":                   "PR 22, boundary",
	"internal/app/paidruns -> internal/store":                   "PR 22, boundary",
	"internal/app/paidruns -> internal/trusthome":               "PR 22, boundary",
	"internal/app/paidruns -> internal/workerenv":               "PR 22, boundary",
	"internal/app/paidruns -> syscall":                          "PR 22, boundary; darwin and linux only",
	"internal/app/peer (test) -> internal/quota":                "PR 29",
	"internal/app/peer -> internal/capability":                  "PR 40",
	"internal/app/peer -> internal/adapter/config":              "after PR 10: a peer loads the config files",
	"internal/app/peer -> internal/rex":                         "the Pane port, moved out of PR 29 to a later task",
	"internal/app/peer -> internal/runner":                      "PR 41",
	"internal/app/peer -> internal/workerenv":                   "PR 35",
	"internal/app/profile (test) -> internal/adapter/config":    "after PR 10: its tests load the config files",
	"internal/app/profile (test) -> internal/runner":            "PR 29",
	"internal/app/roster -> internal/capability":                "PR 40",
	"internal/app/roster -> internal/adapter/config":            "after PR 10: roster loads the config files",
	"internal/core/route (test) -> internal/adapter/config":     "after PR 10: its tests load the default config",
	"internal/session (test) -> internal/app/paidruns":          "PR 22",
	"internal/core/usage (test) -> internal/adapter/config":     "after PR 10: its tests load the default config",
}

// maxKnownViolations is the length of knownViolations. It may only go down.
const maxKnownViolations = 68

// Reasons of the allowed edges, one per target.
const (
	reasonStore    = "state folder handle, renames and locks; the same for every agent, no second backend, no seam (design section 3 port rule)"
	reasonJournal  = "append-only journal in the state folder; same reason"
	reasonBus      = "message files in the state folder; same reason"
	reasonCoauthor = "co-author records in the state folder; the templates per agent come from config"
	reasonProject  = "git rev-parse of the project and state roots, and git helpers; git is the project's tool, not an agent's"
	reasonSession  = "lead and peer registry in the state folder; its agent parts go behind the Lead port in Task 34"
	reasonPlugin   = "embedded skill names, read-only data"
)

// allowedEdges are the app-to-kit imports where a port buys nothing, by the
// port rule of the design (section 3): a port only for behaviour that differs
// per agent, a planned second backend, or a test seam at a process, clock or
// random boundary. Keyed "from -> to"; the value is the reason. Targets are
// closed, see allowedTargets.
var allowedEdges = map[string]string{
	"internal/app/calibrate -> internal/journal": reasonJournal,
	"internal/app/calibrate -> internal/project": reasonProject,
	"internal/app/calibrate -> internal/store":   reasonStore,
	"internal/app/delegate -> internal/bus":      reasonBus,
	"internal/app/delegate -> internal/coauthor": reasonCoauthor,
	"internal/app/delegate -> internal/journal":  reasonJournal,
	"internal/app/delegate -> internal/project":  reasonProject,
	"internal/app/delegate -> internal/session":  reasonSession,
	"internal/app/delegate -> internal/store":    reasonStore,
	"internal/app/doctor -> internal/session":    reasonSession,
	"internal/app/hook -> internal/bus":          reasonBus,
	"internal/app/hook -> internal/session":      reasonSession,
	"internal/app/hook -> internal/store":        reasonStore,
	"internal/app/peer -> internal/bus":          reasonBus,
	"internal/app/peer -> internal/coauthor":     reasonCoauthor,
	"internal/app/peer -> internal/project":      reasonProject,
	"internal/app/peer -> internal/session":      reasonSession,
	"internal/app/peer -> internal/store":        reasonStore,
	"internal/app/profile -> internal/journal":   reasonJournal,
	"internal/app/profile -> internal/project":   reasonProject,
	"internal/app/profile -> internal/store":     reasonStore,
	"internal/app/roster -> internal/session":    reasonSession,
	"internal/app/sync -> internal/project":      reasonProject,
	"internal/app/sync -> internal/store":        reasonStore,
	"internal/app/sync -> plugin":                reasonPlugin,
}

// allowedTargets is the closed set of targets an allowed edge may have.
var allowedTargets = []string{
	"internal/store", "internal/journal", "internal/bus", "internal/coauthor",
	"internal/project", "internal/adapter/session", "internal/session", "plugin",
}

// invalidAllowedEdges returns the keys of m that are not valid allowed edges,
// sorted: the from must be internal/app/<x>, the to one of allowedTargets, and
// the reason not empty.
func invalidAllowedEdges(m map[string]string) []string {
	var bad []string
	for key, reason := range m {
		from, to, ok := strings.Cut(key, " -> ")
		parts := strings.Split(from, "/")
		appFrom := ok && len(parts) == 3 && parts[0] == "internal" && parts[1] == "app" && parts[2] != ""
		if !appFrom || !slices.Contains(allowedTargets, to) || strings.TrimSpace(reason) == "" {
			bad = append(bad, key)
		}
	}
	sort.Strings(bad)
	return bad
}

// boundaryExceptions are the imports of the security boundary that stay on
// purpose ("security boundary: moves only"). Empty until PR 20.
var boundaryExceptions = map[string]string{}

// layerViolation is one import that breaks a rule.
type layerViolation struct {
	Key, Rule string
}

func TestLayers(t *testing.T) {
	t.Run("rules", func(t *testing.T) {
		m := modulePath + "/"
		cases := []struct {
			from, to string
			test     bool
			want     string
		}{
			{"internal/mode", "os", false, "core-stdlib"},
			{"internal/mode", "os", true, ""},
			{"internal/core/route", "net/url", false, "core-stdlib"},
			{"internal/core/route", "github.com/bmatcuk/doublestar/v4", false, ""},
			{"internal/core/config", "go.yaml.in/yaml/v3", false, ""},
			{"internal/core/route", "go.yaml.in/yaml/v3", false, "core-module"},
			{"internal/core/route", m + "internal/store", false, "core-imports-outer"},
			{"internal/core/route", m + "internal/store", true, "core-imports-outer"},
			{"internal/app/delegate", m + "internal/app/peer", false, "app-order"},
			{"internal/app/peer", m + "internal/app/delegate", false, ""},
			{"internal/app/delegate", "os", false, ""},
			{"internal/app/delegate", "os/exec", false, "app-stdlib"},
			{"internal/app/delegate", "github.com/gofrs/flock", false, "app-module"},
			{"internal/app/delegate", m + "internal/adapter/kit", false, "app-imports-adapter"},
			{"internal/app/delegate", m + "internal/wiring", false, "app-imports-outer"},
			{"internal/adapter/kit", m + "internal/app/delegate", false, "kit-imports-outer"},
			{"internal/adapter/claude", m + "internal/adapter/codex", false, "agent-imports-outer"},
			{"internal/adapter/claude", m + "internal/adapter/kit", false, ""},
			{"internal/wiring", m + "cmd/duolog", false, "wiring-imports-cmd"},
			{"internal/app/delegate", m + "internal/testbin", false, "test-only"},
			{"internal/app/delegate", m + "internal/testbin", true, ""},
			{"internal/app/delegate/delegatetest", m + "internal/app/delegate", false, ""},
			{"internal/cores/route", "fmt", false, "unknown-tier"},
			{"internal/app/delegate", m + "internal/newthing", false, "unknown-tier"},
			{"internal/app/hook", m + "internal/telemetry", false, ""},
			{"internal/app/delegate", m + "internal/runner", false, "app-imports-adapter"},
			{"internal/router", m + "internal/models", false, "core-imports-outer"},
		}
		for _, c := range cases {
			if got := layerRule(c.from, c.to, c.test); got != c.want {
				t.Errorf("layerRule(%q, %q, %v) = %q, want %q", c.from, c.to, c.test, got, c.want)
			}
		}
	})
	t.Run("graph", func(t *testing.T) {
		m := modulePath + "/"
		pkgs := []listedPackage{
			{ImportPath: m + "internal/mode", Imports: []string{"os", "strings"}},
			{ImportPath: m + "internal/mode [" + m + "internal/mode.test]", ForTest: m + "internal/mode",
				Imports: []string{"os", "strings", "testing", m + "internal/store [" + m + "internal/mode.test]"}},
			{ImportPath: m + "internal/mode.test", Name: "main", Imports: []string{m + "internal/mode [" + m + "internal/mode.test]"}},
			{ImportPath: m + "internal/mode_test [" + m + "internal/mode.test]", ForTest: m + "internal/mode",
				Imports: []string{m + "internal/mode [" + m + "internal/mode.test]", "os"}},
		}
		want := []layerViolation{
			{Key: "internal/mode (test) -> internal/store", Rule: "core-imports-outer"},
			{Key: "internal/mode -> os", Rule: "core-stdlib"},
		}
		if got := layerViolations(pkgs); !slices.Equal(got, want) {
			t.Errorf("layerViolations = %v, want %v", got, want)
		}
	})
	t.Run("graph without imports", func(t *testing.T) {
		m := modulePath + "/"
		pkgs := []listedPackage{{ImportPath: m + "internal/newthing", Name: "newthing"}}
		want := []layerViolation{{Key: "internal/newthing -> (no tier)", Rule: "unknown-tier"}}
		if got := layerViolations(pkgs); !slices.Equal(got, want) {
			t.Errorf("layerViolations = %v, want %v", got, want)
		}
	})
	t.Run("graph production path ending in _test", func(t *testing.T) {
		m := modulePath + "/"
		pkgs := []listedPackage{
			{ImportPath: m + "internal/core/io_test", Name: "iohelpers", Imports: []string{"os"}},
			{ImportPath: m + "internal/core/io_test [" + m + "internal/core/io_test.test]", ForTest: m + "internal/core/io_test",
				Name: "iohelpers", Imports: []string{"os", "testing"}},
			{ImportPath: m + "internal/core/io_test.test", Name: "main",
				Imports: []string{m + "internal/core/io_test [" + m + "internal/core/io_test.test]"}},
		}
		want := []layerViolation{{Key: "internal/core/io_test -> os", Rule: "core-stdlib"}}
		if got := layerViolations(pkgs); !slices.Equal(got, want) {
			t.Errorf("layerViolations = %v, want %v", got, want)
		}
		if base, ok := packageBase(pkgs[2], testMains(pkgs)); ok {
			t.Errorf("packageBase of the generated test main = %q, want none", base)
		}
		if base, ok := packageBase(pkgs[0], testMains(pkgs)); !ok || base != "internal/core/io_test" {
			t.Errorf("packageBase of the production package = %q, %v", base, ok)
		}
	})
	t.Run("telemetry adapter path", func(t *testing.T) {
		m := modulePath + "/"
		if got := layerRule("internal/app/delegate", m+"internal/adapter/telemetry", false); got != "app-imports-adapter" {
			t.Errorf("layerRule to internal/adapter/telemetry = %q, want app-imports-adapter", got)
		}
	})
	t.Run("allowed", func(t *testing.T) {
		bad := map[string]string{
			"internal/app/delegate -> internal/runner": "x",
			"internal/core/route -> internal/store":    "x",
			"internal/app/peer -> internal/bus":        "",
		}
		want := []string{"internal/app/delegate -> internal/runner", "internal/app/peer -> internal/bus", "internal/core/route -> internal/store"}
		if got := invalidAllowedEdges(bad); !slices.Equal(got, want) {
			t.Errorf("invalidAllowedEdges = %q, want %q", got, want)
		}
		if got := invalidAllowedEdges(map[string]string{"internal/app/peer -> internal/bus": "reason"}); len(got) != 0 {
			t.Errorf("invalidAllowedEdges of a valid edge = %q, want none", got)
		}
		if got := invalidAllowedEdges(allowedEdges); len(got) != 0 {
			t.Errorf("allowedEdges has invalid keys %q", got)
		}
	})
	t.Run("stale", func(t *testing.T) {
		known := map[string]string{"a -> b": "", "c -> d": ""}
		if got := staleViolations(known, map[string]bool{"a -> b": true}); !slices.Equal(got, []string{"c -> d"}) {
			t.Errorf("staleViolations with one found = %q, want [c -> d]", got)
		}
		if got := staleViolations(known, map[string]bool{"a -> b": true, "c -> d": true}); len(got) != 0 {
			t.Errorf("staleViolations with both found = %q, want none", got)
		}
	})

	found := map[string]bool{}
	listed := map[string]bool{}
	targets := 0
	forEachTarget(t, func(t *testing.T, env []string) {
		pkgs := goListPackages(t, env, "-test", "-json=ImportPath,Name,ForTest,Imports", "./...")
		mains := testMains(pkgs)
		for _, pkg := range pkgs {
			if rel, ok := packageBase(pkg, mains); ok {
				listed[rel] = true
			}
		}
		for _, v := range layerViolations(pkgs) {
			found[v.Key] = true
			_, known := knownViolations[v.Key]
			_, excepted := boundaryExceptions[v.Key]
			_, allowed := allowedEdges[v.Key]
			if !known && !excepted && !allowed {
				t.Errorf("%s breaks %s; fix the import or, in a move, rename its key in knownViolations", v.Key, v.Rule)
			}
		}
		targets++
	})
	if targets == 6 {
		for _, key := range staleViolations(knownViolations, found) {
			t.Errorf("%s no longer happens on any target; delete it from knownViolations and lower maxKnownViolations", key)
		}
		for _, key := range staleViolations(allowedEdges, found) {
			t.Errorf("%s no longer happens on any target; delete it from allowedEdges", key)
		}
		for _, rel := range staleLegacyTiers(listed) {
			t.Errorf("%s is not a package any more; delete it from legacyTiers", rel)
		}
	}
	if len(knownViolations) != maxKnownViolations {
		t.Errorf("knownViolations has %d entries; set maxKnownViolations to that number (it may only go down)", len(knownViolations))
	}
}

// tierOf returns the tier of the module-relative package path rel ("" for the
// module root) and, for an app package, its name. ok is false for a package
// that has no tier.
func tierOf(rel string) (t tier, app string, ok bool) {
	parts := strings.Split(rel, "/")
	last := parts[len(parts)-1]
	switch {
	case strings.HasPrefix(rel, "internal/core/"):
		return tierCore, "", true
	case strings.HasPrefix(rel, "internal/app/"):
		if len(parts) > 3 && strings.HasSuffix(last, "test") {
			return tierTest, "", true
		}
		if !slices.Contains(appOrder, parts[2]) {
			return 0, "", false
		}
		return tierApp, parts[2], true
	case under(rel, "internal/adapter/claude"), under(rel, "internal/adapter/codex"):
		return tierAgent, "", true
	case strings.HasPrefix(rel, "internal/adapter/"):
		return tierKit, "", true
	case under(rel, "internal/wiring/wiringtest"):
		return tierTest, "", true
	case under(rel, "internal/wiring"):
		return tierWiring, "", true
	case rel == "", under(rel, "cmd"), under(rel, "scripts"):
		return tierCmd, "", true
	case under(rel, "internal/testbin"):
		return tierTest, "", true
	}
	// legacyTiers comes before the name rule, so that a package that has not
	// moved is not taken for a test helper because of its name.
	if l, found := legacyTiers[rel]; found {
		return l.tier, l.app, true
	}
	if strings.HasSuffix(last, "test") {
		return tierTest, "", true
	}
	return 0, "", false
}

// layerRule returns the name of the rule that the import of to (a full import
// path) by from (module-relative) breaks, or "". test marks a test edge: an
// import that only the package's tests have.
func layerRule(from, to string, test bool) string {
	toRel, duolog := moduleRelative(to)
	if duolog && (toRel == from || toRel == "internal/telemetry") {
		return "" // TestTelemetryDependencies owns the telemetry edges
	}
	fromTier, fromApp, ok := tierOf(from)
	if !ok {
		return "unknown-tier"
	}
	if duolog {
		toTier, toApp, ok := tierOf(toRel)
		if !ok {
			return "unknown-tier"
		}
		return duologRule(from, fromTier, fromApp, toRel, toTier, toApp, test)
	}
	if test {
		return "" // tests may use any standard package and module
	}
	return externalRule(from, fromTier, to)
}

func duologRule(from string, fromTier tier, fromApp, to string, toTier tier, toApp string, test bool) string {
	outer := func(tiers ...tier) bool { return slices.Contains(tiers, toTier) }
	switch {
	case toTier == tierTest:
		if !test && fromTier != tierTest {
			return "test-only"
		}
	case fromTier == tierCore && toTier != tierCore:
		return "core-imports-outer"
	case fromTier == tierApp && outer(tierKit, tierAgent):
		return "app-imports-adapter"
	case fromTier == tierApp && outer(tierWiring, tierCmd):
		return "app-imports-outer"
	case fromTier == tierApp && toTier == tierApp && slices.Index(appOrder, toApp) >= slices.Index(appOrder, fromApp):
		return "app-order"
	case fromTier == tierKit && outer(tierApp, tierAgent, tierWiring, tierCmd):
		return "kit-imports-outer"
	case fromTier == tierAgent && (outer(tierApp, tierWiring, tierCmd) || toTier == tierAgent && agentOf(to) != agentOf(from)):
		return "agent-imports-outer"
	case fromTier == tierWiring && toTier == tierCmd:
		return "wiring-imports-cmd"
	case fromTier == tierTest && toTier == tierCmd:
		return "test-imports-cmd"
	}
	return ""
}

func externalRule(from string, fromTier tier, to string) string {
	standard := !strings.Contains(strings.Split(to, "/")[0], ".")
	doublestar := under(to, "github.com/bmatcuk/doublestar/v4")
	yaml := under(to, "go.yaml.in/yaml/v3")
	switch fromTier {
	case tierCore:
		if standard && (under(to, "os") || under(to, "net") || under(to, "syscall") || to == "plugin" || to == "unsafe") {
			return "core-stdlib"
		}
		yamlAllowed := yaml && from == "internal/core/config"
		if !standard && !doublestar && !yamlAllowed {
			return "core-module"
		}
	case tierApp:
		if standard && (to == "os/exec" || under(to, "net") || under(to, "syscall")) {
			return "app-stdlib"
		}
		if !standard && !doublestar && !yaml {
			return "app-module"
		}
	}
	return ""
}

// layerViolations returns the imports in one target's go list output that
// break a rule, unique and sorted by key.
func layerViolations(pkgs []listedPackage) []layerViolation {
	mains := testMains(pkgs)
	production := map[string]map[string]bool{}
	for _, pkg := range pkgs {
		path := withoutTestVariant(pkg.ImportPath)
		if pkg.ForTest != "" || mains[path] {
			continue
		}
		imports := map[string]bool{}
		for _, imported := range pkg.Imports {
			imports[withoutTestVariant(imported)] = true
		}
		production[path] = imports
	}
	seen := map[string]bool{}
	var violations []layerViolation
	for _, pkg := range pkgs {
		if mains[withoutTestVariant(pkg.ImportPath)] {
			continue // generated test harness
		}
		base, test := testBase(pkg)
		from, duolog := moduleRelative(base)
		if !duolog {
			continue
		}
		if _, _, ok := tierOf(from); !ok {
			key := layerKey(from, "(no tier)", false)
			if !seen[key] {
				seen[key] = true
				violations = append(violations, layerViolation{Key: key, Rule: "unknown-tier"})
			}
			continue
		}
		for _, imported := range pkg.Imports {
			to := withoutTestVariant(imported)
			if test && production[base][to] {
				continue // the production package reports it
			}
			rule := layerRule(from, to, test)
			if rule == "" {
				continue
			}
			key := layerKey(from, to, test)
			if !seen[key] {
				seen[key] = true
				violations = append(violations, layerViolation{Key: key, Rule: rule})
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].Key < violations[j].Key })
	return violations
}

// staleViolations returns the keys of known that no target found, sorted.
func staleViolations(known map[string]string, found map[string]bool) []string {
	var stale []string
	for key := range known {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	return stale
}

// staleLegacyTiers returns the rows of legacyTiers that go list did not
// report, sorted.
func staleLegacyTiers(listed map[string]bool) []string {
	var stale []string
	for rel := range legacyTiers {
		if !listed[rel] {
			stale = append(stale, rel)
		}
	}
	sort.Strings(stale)
	return stale
}

// packageBase returns the module-relative path of the package that pkg
// builds or tests; ok is false for a test harness or a package outside the
// module.
func packageBase(pkg listedPackage, mains map[string]bool) (string, bool) {
	if mains[withoutTestVariant(pkg.ImportPath)] {
		return "", false
	}
	base, _ := testBase(pkg)
	return moduleRelative(base)
}

// testMains returns the import paths of the test mains go list generates. A
// main is the path in the " [path]" suffix of the packages built for a test,
// so a production package whose path ends in ".test" is not one.
func testMains(pkgs []listedPackage) map[string]bool {
	mains := map[string]bool{}
	for _, pkg := range pkgs {
		if _, variant, ok := strings.Cut(pkg.ImportPath, " ["); ok && pkg.ForTest != "" {
			mains[strings.TrimSuffix(variant, "]")] = true
		}
	}
	return mains
}

// testBase returns the import path of the package that pkg builds or tests,
// and whether pkg is a test variant. go list marks every variant with ForTest;
// the external test package of p is p_test with ForTest p. A production path
// that ends in _test has no ForTest and stays as it is.
func testBase(pkg listedPackage) (string, bool) {
	path := withoutTestVariant(pkg.ImportPath)
	if pkg.ForTest == "" {
		return path, false
	}
	if path == pkg.ForTest+"_test" {
		return pkg.ForTest, true
	}
	return path, true
}

// layerKey writes an import as "from -> to": module-relative for duolog
// packages ("." for the module root), the import path otherwise. A test
// edge's from ends in " (test)".
func layerKey(from, to string, test bool) string {
	if from == "" {
		from = "."
	}
	if test {
		from += " (test)"
	}
	if rel, ok := moduleRelative(to); ok {
		to = rel
		if to == "" {
			to = "."
		}
	}
	return fmt.Sprintf("%s -> %s", from, to)
}

// moduleRelative returns path relative to the duolog module ("" for the root)
// and whether path is a duolog package.
func moduleRelative(path string) (string, bool) {
	if path == modulePath {
		return "", true
	}
	rel, ok := strings.CutPrefix(path, modulePath+"/")
	return rel, ok
}

// agentOf returns the agent of an agent adapter path, such as claude for
// internal/adapter/claude/x.
func agentOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 {
		return ""
	}
	return parts[2]
}

// under reports whether path is prefix or a package below it.
func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
