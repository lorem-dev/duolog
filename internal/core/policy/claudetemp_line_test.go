package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A new path in the own temp folder stops being the worker's own when another
// command of the line that can make a symlink or other special file names it,
// or a folder above it: that command may run first. A redirect, mkdir, touch
// and the read programs make none.
func TestMadeInLine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude's temp folder on POSIX")
	}
	temp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(temp, "scratchpad"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(temp, "scratchpad", "n", "f")
	for _, tc := range []struct {
		line string
		made bool
	}{
		{"echo x > {f}", false},
		{"mkdir -p {n} && echo x > {f}", false},
		{"touch {f} && echo x >> {f}", false},
		{"go test > {f}; tail {f}", false},
		{"cat {f} | head; wc -l {f}; grep x {f}; rg x {n}; ls {n}; diff {f} go.mod", false},
		{"sort {f} | uniq | cut -c1 | nl; sed -n 1,5p {f}", false},
		{"cp go.mod {f}; cp -fpv go.sum {f}", false},
		{"ln -s /elsewhere {n} && echo x > {f}", true},
		{"cp -P a/l {n} && echo x > {f}", true},
		{"cp -R a {n} && echo x > {f}", true},
		{"cp --preserve=all a {f}", true},
		{"mv a {n} && echo x > {f}", true},
		{"install a {f}", true},
		{"tar -xf a.tar -C {n}", true},
		{"rsync -a a/ {n}", true},
		{"unzip a.zip -d {n}", true},
		{"git checkout -- {f}", true},
		{"patch -o {f} < a.diff", true},
		{"ls -R {n} && echo x > {f}", true},
		{"sed -n 'w {f}' go.mod", true},
		{"xargs mkdir -p {n} < dirs.txt", true},
		{"python3 make.py {n}", true},
	} {
		line := strings.NewReplacer("{f}", target, "{n}", filepath.Dir(target)).Replace(tc.line)
		a := Action{Kind: "exec", Target: line, Temp: []string{temp}}
		if got := madeInLine(a, target); got != tc.made {
			t.Errorf("%s: made=%v, want %v", tc.line, got, tc.made)
		}
	}
}
