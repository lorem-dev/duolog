// render-install renders the POSIX and PowerShell installer templates to dist.
// A release passes its tag with -version so each asset installs its own
// release; without it the assets resolve the latest release. With -template and/or -o,
// it renders a single custom file instead.
//
// Usage: go run ./scripts/render-install [-version <tag>] [-template <path>] [-o <path>]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// templateData is what the template uses. Values are constant across
// versions of duolog, except Version: the copy in the repository says
// "latest", and a release copy names its own release tag.
type templateData struct {
	Owner              string
	Repo               string
	BinaryName         string
	GoModulePath       string
	SupportedPlatforms string
	Version            string
}

var releaseVersion = regexp.MustCompile(`^v[0-9][0-9A-Za-z.+-]*$`)

// checkVersion accepts "latest" or a release tag such as v1.4.0.
func checkVersion(version string) error {
	if version == "latest" || releaseVersion.MatchString(version) {
		return nil
	}
	return fmt.Errorf("version %q is neither latest nor a release tag such as v1.4.0", version)
}

func defaultData() templateData {
	platforms := []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}
	return templateData{
		Owner:              "lorem-dev",
		Repo:               "duolog",
		BinaryName:         "duolog",
		GoModulePath:       "github.com/lorem-dev/duolog/cmd/duolog",
		SupportedPlatforms: strings.Join(platforms, " "),
		Version:            "latest",
	}
}

func main() {
	var (
		tmplFlag = flag.String("template", "", "path to template")
		outFlag  = flag.String("o", "", "output path")
		verFlag  = flag.String("version", "latest", "release tag the installers default to, or latest")
	)
	flag.Parse()
	data := defaultData()
	data.Version = *verFlag
	if err := checkVersion(data.Version); err != nil {
		fmt.Fprintf(os.Stderr, "render-install: %v\n", err)
		os.Exit(1)
	}

	pairs := [][2]string{{"scripts/install-template/install.sh.tmpl", ".release/install.sh"}, {"scripts/install-template/install.ps1.tmpl", ".release/install.ps1"}}
	if *tmplFlag != "" || *outFlag != "" {
		if *tmplFlag == "" {
			*tmplFlag = pairs[0][0]
		}
		if *outFlag == "" {
			*outFlag = pairs[0][1]
		}
		pairs = [][2]string{{*tmplFlag, *outFlag}}
	}
	for _, pair := range pairs {
		if err := renderTemplate(pair[0], pair[1], data); err != nil {
			fmt.Fprintf(os.Stderr, "render-install: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("rendered %s\n", pair[1])
	}
}

// renderTemplate parses the template at tmplPath, executes it with
// data, and writes the result to outPath with mode 0755.
func renderTemplate(tmplPath, outPath string, data templateData) error {
	body, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read template: %w", err)
	}

	tmpl, err := template.New("install").Option("missingkey=error").Parse(string(body))
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(outPath), err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}
	result := rendered.Bytes()
	if strings.HasSuffix(outPath, ".ps1") {
		result = bytes.ReplaceAll(result, []byte("\n"), []byte("\r\n"))
	}
	return os.WriteFile(outPath, result, 0755)
}
