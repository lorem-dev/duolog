package duolog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// The rule for the characters Claude allows in an MCP name lives in
// internal/core/caps/names.go and nowhere else.
func TestClaudeNameCharsDefinedOnce(t *testing.T) {
	const literal = "`[^A-Za-z0-9_-]`"
	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".worktrees", ".cache", ".codegraph", ".superpowers", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "MustCompile" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "regexp" {
				return true
			}
			if arg, ok := call.Args[0].(*ast.BasicLit); ok && arg.Value == literal {
				found = append(found, filepath.ToSlash(path))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0] != "internal/core/caps/names.go" {
		t.Errorf("%d definitions %v, want 1 in internal/core/caps/names.go", len(found), found)
	}
}
