package duolog_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const telemetryPackage = "github.com/lorem-dev/duolog/internal/telemetry"

func TestTelemetryDependencies(t *testing.T) {
	t.Run("fixtures", func(t *testing.T) {
		cases := []struct {
			path, module string
			forbidden    bool
		}{
			{"net/http", "", true},
			{"net", "", true},
			{"net/netip", "", true},
			{"crypto/tls", "", true},
			{"os/exec", "", true},
			{"plugin", "", true},
			{"syscall/js", "", true},
			{"example.com/newdep", "example.com/newdep", true},
			{"github.com/spf13/cobra", "github.com/spf13/cobra", true},
			{"github.com/lorem-dev/duolog/internal/config", "github.com/lorem-dev/duolog", true},
			{"github.com/lorem-dev/duolog/internal/telemetryx", "github.com/lorem-dev/duolog", true},
			{"os", "", false},
			{"crypto/hmac", "", false},
			{"encoding/json", "", false},
			{"github.com/gofrs/flock", "github.com/gofrs/flock", false},
			{"golang.org/x/sys/unix", "golang.org/x/sys", false},
			{"golang.org/x/sys/windows", "golang.org/x/sys", false},
			{telemetryPackage, "github.com/lorem-dev/duolog", false},
			{telemetryPackage + "_test", "github.com/lorem-dev/duolog", false},
		}
		for _, c := range cases {
			if got := forbiddenTelemetryDependency(c.path, c.module); got != c.forbidden {
				t.Errorf("forbiddenTelemetryDependency(%q, %q) = %v, want %v", c.path, c.module, got, c.forbidden)
			}
		}
	})
	t.Run("graph fixtures", func(t *testing.T) {
		const (
			xsys     = "golang.org/x/sys"
			testPath = telemetryPackage + " [" + telemetryPackage + ".test]"
		)
		cases := []struct {
			name      string
			packages  []listedPackage
			root      string
			allowExec bool
			want      []string
		}{
			{
				name: "direct net import",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{"os", "net"}},
					{ImportPath: "os"},
					{ImportPath: "net"},
				},
				want: []string{"net"},
			},
			{
				name: "net through x/sys/windows",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{xsysWindows}},
					{ImportPath: xsysWindows, Imports: []string{"net"}, Module: &listedModule{Path: xsys}},
					{ImportPath: "net", Imports: []string{"net/netip"}},
					{ImportPath: "net/netip"},
				},
			},
			{
				name: "net also through the standard library",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{xsysWindows, "log/syslog"}},
					{ImportPath: xsysWindows, Imports: []string{"net"}, Module: &listedModule{Path: xsys}},
					{ImportPath: "log/syslog", Imports: []string{"net"}},
					{ImportPath: "net", Imports: []string{"net/netip"}},
					{ImportPath: "net/netip"},
				},
				want: []string{"net", "net/netip"},
			},
			{
				name: "other forbidden package through x/sys/windows",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{xsysWindows}},
					{ImportPath: xsysWindows, Imports: []string{"net/http", "os/exec"}, Module: &listedModule{Path: xsys}},
					{ImportPath: "net/http"},
					{ImportPath: "os/exec"},
				},
				want: []string{"net/http", "os/exec"},
			},
			{
				name: "os/exec in production",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{"os/exec"}},
					{ImportPath: "os/exec"},
				},
				want: []string{"os/exec"},
			},
			{
				name: "os/exec in tests",
				packages: []listedPackage{
					{ImportPath: testPath, Imports: []string{"os/exec", "testing"}},
					{ImportPath: "os/exec"},
					{ImportPath: "testing"},
				},
				root:      testPath,
				allowExec: true,
			},
			{
				name: "net in tests",
				packages: []listedPackage{
					{ImportPath: testPath, Imports: []string{"net"}},
					{ImportPath: "net"},
				},
				root:      testPath,
				allowExec: true,
				want:      []string{"net"},
			},
			{
				name: "package missing from go list",
				packages: []listedPackage{
					{ImportPath: telemetryPackage, Imports: []string{"example.com/unlisted"}},
				},
				want: []string{"example.com/unlisted"},
			},
		}
		for _, c := range cases {
			packages := map[string]listedPackage{}
			for _, pkg := range c.packages {
				packages[pkg.ImportPath] = pkg
			}
			root := c.root
			if root == "" {
				root = telemetryPackage
			}
			got := telemetryViolations(packages, []string{root}, c.allowExec)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("%s: telemetryViolations = %q, want %q", c.name, got, c.want)
			}
		}
	})
	forEachTarget(t, func(t *testing.T, env []string) {
		listed := goListPackages(t, env, "-test", "-deps", "-json=ImportPath,Name,ForTest,Imports,Module", "./internal/telemetry")
		packages := map[string]listedPackage{}
		var roots, testRoots []string
		for _, pkg := range listed {
			packages[pkg.ImportPath] = pkg
			if pkg.Name == "main" || !isTelemetryPath(withoutTestVariant(pkg.ImportPath)) {
				continue
			}
			if pkg.ForTest != "" {
				testRoots = append(testRoots, pkg.ImportPath)
			} else if !strings.Contains(pkg.ImportPath, " ") {
				roots = append(roots, pkg.ImportPath)
			}
		}
		if len(roots) == 0 || len(testRoots) == 0 {
			t.Fatalf("go list did not report %s and its tests: roots %q, test roots %q", telemetryPackage, roots, testRoots)
		}
		for _, path := range telemetryViolations(packages, roots, false) {
			t.Errorf("telemetry depends on forbidden package %s", path)
		}
		// Tests start helper processes, so they may use os/exec. The generated
		// harness is not a root and stays out of the walk.
		for _, path := range telemetryViolations(packages, testRoots, true) {
			t.Errorf("telemetry tests depend on forbidden package %s", path)
		}
	})
}

func TestTelemetryImporters(t *testing.T) {
	t.Run("fixtures", func(t *testing.T) {
		cases := []struct {
			path    string
			allowed bool
		}{
			{telemetryPackage, true},
			{telemetryPackage + "_test", true},
			{telemetryPackage + ".test", true},
			{"github.com/lorem-dev/duolog/cmd/duolog", true},
			{"github.com/lorem-dev/duolog/internal/balance", true},
			{"github.com/lorem-dev/duolog/internal/hook", true},
			{"github.com/lorem-dev/duolog/internal/delegate", false},
			{"github.com/lorem-dev/duolog/internal/delegate_test", false},
			{"github.com/lorem-dev/duolog/internal/hookx", false},
			{"github.com/lorem-dev/duolog/internal/router", false},
			{"github.com/lorem-dev/duolog/internal", false},
			{"github.com/lorem-dev/duolog", false},
			{"example.com/other/internal/hook", false},
		}
		for _, c := range cases {
			if got := allowedTelemetryImporter(c.path); got != c.allowed {
				t.Errorf("allowedTelemetryImporter(%q) = %v, want %v", c.path, got, c.allowed)
			}
		}
	})
	forEachTarget(t, func(t *testing.T, env []string) {
		// -test adds test variants, whose Imports include test imports.
		packages := goListPackages(t, env, "-test", "-json=ImportPath,Name,Imports", "./...")
		seen := false
		for _, pkg := range packages {
			path := withoutTestVariant(pkg.ImportPath)
			seen = seen || path == telemetryPackage
			if pkg.Name == "main" && strings.HasSuffix(path, ".test") {
				continue // generated test harness
			}
			for _, imported := range pkg.Imports {
				if withoutTestVariant(imported) == telemetryPackage && !allowedTelemetryImporter(path) {
					t.Errorf("%s imports %s", pkg.ImportPath, telemetryPackage)
				}
			}
		}
		if !seen {
			t.Errorf("go list did not report %s", telemetryPackage)
		}
	})
}

// xsysWindows imports net for its socket types; telemetry uses it for the
// Windows DACL, not for network calls.
const xsysWindows = "golang.org/x/sys/windows"

type listedPackage struct {
	ImportPath string
	Name       string
	ForTest    string
	Imports    []string
	Module     *listedModule
}

type listedModule struct {
	Path string
}

// telemetryViolations walks the import graph in packages from roots and
// returns the forbidden packages it reaches, sorted. net and net/netip are
// allowed only where they are reached through x/sys/windows alone; every
// other path to them, a direct import included, is a violation. allowExec
// permits os/exec, for test graphs.
func telemetryViolations(packages map[string]listedPackage, roots []string, allowExec bool) []string {
	direct := reachablePackages(packages, roots, xsysWindows)
	var violations []string
	for path := range reachablePackages(packages, roots, "") {
		pkg, listed := packages[path]
		name := withoutTestVariant(path)
		module := ""
		if pkg.Module != nil {
			module = pkg.Module.Path
		}
		switch {
		case !listed:
			violations = append(violations, path)
		case allowExec && name == "os/exec":
		case !direct[path] && (name == "net" || name == "net/netip"):
		case forbiddenTelemetryDependency(name, module):
			violations = append(violations, path)
		}
	}
	sort.Strings(violations)
	return violations
}

// reachablePackages returns roots and every package they import, directly or
// not, without following the imports of the package stop.
func reachablePackages(packages map[string]listedPackage, roots []string, stop string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		if seen[path] {
			continue
		}
		seen[path] = true
		if withoutTestVariant(path) != stop {
			queue = append(queue, packages[path].Imports...)
		}
	}
	return seen
}

// isTelemetryPath reports whether path is the telemetry package, one of its
// subpackages or its external test package.
func isTelemetryPath(path string) bool {
	path = strings.TrimSuffix(path, "_test")
	return path == telemetryPackage || strings.HasPrefix(path, telemetryPackage+"/")
}

// forEachTarget runs check for every release target with CGO disabled.
func forEachTarget(t *testing.T, check func(t *testing.T, env []string)) {
	for _, targetOS := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(targetOS+"/"+arch, func(t *testing.T) {
				check(t, append(os.Environ(), "GOOS="+targetOS, "GOARCH="+arch, "CGO_ENABLED=0"))
			})
		}
	}
}

func goListPackages(t *testing.T, env []string, args ...string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	var packages []listedPackage
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, pkg)
	}
	return packages
}

// withoutTestVariant strips the " [p.test]" suffix go list adds to packages
// recompiled for a test.
func withoutTestVariant(path string) string {
	path, _, _ = strings.Cut(path, " ")
	return path
}

// forbiddenTelemetryDependency reports whether the telemetry package may not
// depend on path, a package of module (empty for the standard library). The
// data never leaves the machine, so no network or process code, and no module
// beyond flock and x/sys.
func forbiddenTelemetryDependency(path, module string) bool {
	switch module {
	case "":
		for _, name := range []string{"net", "crypto/tls", "os/exec", "plugin", "syscall/js"} {
			if path == name || strings.HasPrefix(path, name+"/") {
				return true
			}
		}
		return false
	case "github.com/gofrs/flock", "golang.org/x/sys":
		return false
	case "github.com/lorem-dev/duolog":
		return !isTelemetryPath(path)
	default:
		return true
	}
}

// allowedTelemetryImporter reports whether the package path may import the
// telemetry package. Telemetry's own external tests and test harness count as
// telemetry.
func allowedTelemetryImporter(path string) bool {
	path = strings.TrimSuffix(strings.TrimSuffix(path, ".test"), "_test")
	switch path {
	case telemetryPackage,
		"github.com/lorem-dev/duolog/cmd/duolog",
		"github.com/lorem-dev/duolog/internal/balance",
		"github.com/lorem-dev/duolog/internal/hook":
		return true
	}
	return false
}
