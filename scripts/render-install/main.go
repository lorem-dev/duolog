// render-install renders the POSIX and PowerShell installer templates to dist.
// Both release assets resolve versions at runtime. With -template and/or -o,
// it renders a single custom file instead.
//
// Usage: go run ./scripts/render-install [-template <path>] [-o <path>]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// templateData is what the template uses. Values are constant across
// versions of duolog - the install script itself resolves the
// version at runtime.
type templateData struct {
	Owner              string
	Repo               string
	BinaryName         string
	GoModulePath       string
	SupportedPlatforms string
}

func defaultData() templateData {
	platforms := []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}
	return templateData{
		Owner:              "lorem-dev",
		Repo:               "duolog",
		BinaryName:         "duolog",
		GoModulePath:       "github.com/lorem-dev/duolog/cmd/duolog",
		SupportedPlatforms: strings.Join(platforms, " "),
	}
}

func main() {
	var (
		tmplFlag = flag.String("template", "", "path to template")
		outFlag  = flag.String("o", "", "output path")
	)
	flag.Parse()

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
		if err := renderTemplate(pair[0], pair[1], defaultData()); err != nil {
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
