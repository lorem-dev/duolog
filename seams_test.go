package duolog_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// seamExemptions are the package-level variables that may hold a function.
// The key is "<package directory>.<name>", the value the reason. A variable of
// the security boundary stays as it is until the boundary ports.
var seamExemptions = map[string]string{
	"internal/app/gate.AskPoll":      "security boundary: moves only",
	"internal/app/paidruns.PaceRead": "security boundary: moves only",
	"internal/app/paidruns.publish":  "security boundary: moves only",
}

// seamRoots are the directories whose non-test files may declare no seam
// variable: a test replaces a field of the dependencies, never a variable.
var seamRoots = []string{"internal/app", "cmd/duolog"}

// TestNoSeamVars fails on a package-level var, in the app packages and the
// commands, whose value is a func literal, a function of another package
// (workerenv.GateBinary), or a function of its own package.
func TestNoSeamVars(t *testing.T) {
	var found []string
	for _, root := range seamRoots {
		dirs := map[string][]string{}
		err := filepath.WalkDir(root, func(name string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
				dirs[filepath.Dir(name)] = append(dirs[filepath.Dir(name)], name)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for dir, files := range dirs {
			parsed := parseFiles(t, files)
			own := topLevelFuncs(parsed)
			for name, file := range parsed {
				for _, seam := range seamVars(file, own, importedFuncs) {
					key := filepath.ToSlash(dir) + "." + seam.name
					if _, ok := seamExemptions[key]; ok {
						continue
					}
					found = append(found, filepath.ToSlash(name)+": var "+seam.name+" = "+seam.value)
				}
			}
		}
	}
	slices.Sort(found)
	for _, f := range found {
		t.Error(f)
	}
	for key, reason := range seamExemptions {
		if !strings.HasPrefix(reason, "security boundary: ") {
			t.Errorf("exemption %s: reason %q", key, reason)
		}
	}
}

// TestSeamVarsFindsEachShape keeps the scan honest: it reports the three
// shapes of a seam and passes calls and values.
func TestSeamVarsFindsEachShape(t *testing.T) {
	src := `package p

import (
	"errors"
	"regexp"

	"example.com/m/other"
)

var (
	literal  = func() {}
	foreign  = other.Run
	own      = helper
	typed    func() = helper
	value    = other.Name
	failure  = errors.New("x")
	pattern  = regexp.MustCompile("x")
	words    = []string{"a"}
	number   = 3
)

func helper() {}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	imported := func(importPath string) map[string]bool {
		if importPath == "example.com/m/other" {
			return map[string]bool{"Run": true}
		}
		return nil
	}
	var got []string
	for _, s := range seamVars(file, map[string]bool{"helper": true}, imported) {
		got = append(got, s.name)
	}
	if want := []string{"literal", "foreign", "own", "typed"}; !slices.Equal(got, want) {
		t.Fatalf("seams = %v, want %v", got, want)
	}
}

type seamVar struct{ name, value string }

// parseFiles parses the files of one package.
func parseFiles(t *testing.T, files []string) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		parsed[name] = file
	}
	return parsed
}

// topLevelFuncs names the functions, not the methods, that the files declare.
func topLevelFuncs(files map[string]*ast.File) map[string]bool {
	names := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// importedFuncs names the functions of the package at importPath: those of
// this module and of the standard library. Other packages have none here.
func importedFuncs(importPath string) map[string]bool {
	var dir string
	if rest, ok := strings.CutPrefix(importPath, modulePath+"/"); ok {
		dir = filepath.FromSlash(rest)
	} else if !strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
		pkg, err := build.Import(importPath, "", build.FindOnly)
		if err != nil {
			return nil
		}
		dir = pkg.Dir
	} else {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			files = append(files, filepath.Join(dir, n))
		}
	}
	fset := token.NewFileSet()
	names := map[string]bool{}
	for _, name := range files {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.IsExported() {
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// seamVars returns the package-level variables of file that hold a func
// literal, a function of the package (own), or a function of an imported
// package (funcsOf gives the exported functions of an import path).
func seamVars(file *ast.File, own map[string]bool, funcsOf func(importPath string) map[string]bool) []seamVar {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = importPath
	}
	var seams []seamVar
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, value := range vs.Values {
				if i >= len(vs.Names) {
					break
				}
				what := ""
				switch v := value.(type) {
				case *ast.FuncLit:
					what = "func literal"
				case *ast.Ident:
					if own[v.Name] {
						what = v.Name
					}
				case *ast.SelectorExpr:
					if x, ok := v.X.(*ast.Ident); ok && imports[x.Name] != "" && funcsOf(imports[x.Name])[v.Sel.Name] {
						what = x.Name + "." + v.Sel.Name
					}
				}
				if what != "" {
					seams = append(seams, seamVar{vs.Names[i].Name, what})
				}
			}
		}
	}
	return seams
}
