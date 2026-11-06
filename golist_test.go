package duolog_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
