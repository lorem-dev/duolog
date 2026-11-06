package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A layer that does not load names its file and, when YAML or the key check
// knows it, the line of the problem. The error text stays as it was.
func TestLoadErrorNamesFileAndLine(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, message string
		line                int
	}{
		{"unknown key", "roles:\n  plan:\n    use:\n      effrot: high\n", "config.roles.plan.use.effrot: unknown key", 4},
		{"invalid value", "roles:\n  plan:\n    use:\n      effort: hgh\n", `config.roles.plan.use.effort: invalid value "hgh"`, 4},
		{"syntax", "roles:\n  plan: [\n", "yaml: line 2:", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := isolatedRoot(t)
			userDir, err := os.UserConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(userDir, "duolog", "config.yaml")
			writeConfig(t, path, tc.yaml)
			_, err = Load(root)
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("Load error %v is not a LoadError", err)
			}
			if le.File != path || le.Line != tc.line || !strings.HasPrefix(err.Error(), path+": ") || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("LoadError %+v (%v), want %s line %d", le, err, path, tc.line)
			}
			problem := Problem(err)
			want := "config error in " + path + ", line " + strconv.Itoa(tc.line) + ": "
			if !strings.HasPrefix(problem, want) || !strings.Contains(problem, tc.message) || strings.ContainsAny(problem, "\r\n") {
				t.Fatalf("Problem %q, want it to start %q", problem, want)
			}
		})
	}
}

// A problem found after the layers merged belongs to no single file.
func TestProblemWithoutFile(t *testing.T) {
	root := isolatedRoot(t)
	writeConfig(t, filepath.Join(root, ".duolog", "config.yaml"), "roles:\n  plan:\n    use:\n      model: nosuch\n")
	_, err := Load(root)
	if err == nil {
		t.Fatal("Load accepted an unknown model")
	}
	if got := Problem(err); got != "config error: "+err.Error() {
		t.Fatalf("Problem %q", got)
	}
	if got := Problem(errors.New("a\n  b")); got != "config error: a b" {
		t.Fatalf("Problem keeps line breaks: %q", got)
	}
}
