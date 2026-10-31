package duolog_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBinaryModules(t *testing.T) {
	// A new binary dependency needs an explicit review, even when it is indirect.
	allowed := map[string]bool{
		"github.com/lorem-dev/duolog":          true,
		"github.com/bmatcuk/doublestar/v4":     true,
		"github.com/gofrs/flock":               true,
		"github.com/inconshreveable/mousetrap": true,
		"github.com/pelletier/go-toml/v2":      true,
		"github.com/spf13/cobra":               true,
		"github.com/spf13/pflag":               true,
		"go.yaml.in/yaml/v3":                   true,
		"golang.org/x/sys":                     true,
	}
	for _, targetOS := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(targetOS+"/"+arch, func(t *testing.T) {
				cmd := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./cmd/duolog")
				cmd.Env = append(os.Environ(), "GOOS="+targetOS, "GOARCH="+arch, "CGO_ENABLED=0")
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				output, err := cmd.Output()
				if err != nil {
					t.Fatalf("go list: %v\n%s", err, stderr.String())
				}
				for _, name := range strings.Fields(string(output)) {
					if !allowed[name] {
						t.Errorf("unreviewed binary module: %s", name)
					}
				}
			})
		}
	}
}
