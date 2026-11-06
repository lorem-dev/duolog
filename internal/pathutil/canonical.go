package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

// Canonical spells a local path the way two spellings of one folder compare
// equal: symlinks in its longest existing prefix resolved (Resolved), cleaned,
// and lower-cased when the volume ignores case. macOS and Windows always
// fold: on a volume that does not ignore case that only makes more paths
// overlap, never fewer.
func Canonical(name string) string {
	resolved := filepath.Clean(Resolved(filepath.Clean(name)))
	if foldsCase(resolved) {
		resolved = strings.ToLower(resolved)
	}
	return resolved
}

// Overlap reports whether one of two local folders is the other or lies
// inside it, compared by Canonical.
func Overlap(a, b string) bool {
	ca, cb := Canonical(a), Canonical(b)
	return Within(ca, cb) || Within(cb, ca)
}

// Within reports whether child is parent or lies inside it. It compares the
// spellings as given.
func Within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// foldsCase asks the volume of name whether it ignores case: the deepest
// existing ancestor with a letter in its name, spelled with its case swapped,
// is the same folder.
func foldsCase(name string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return true
	}
	for dir := name; ; dir = filepath.Dir(dir) {
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		base := filepath.Base(dir)
		swapped := strings.Map(func(r rune) rune {
			if unicode.IsUpper(r) {
				return unicode.ToLower(r)
			}
			return unicode.ToUpper(r)
		}, base)
		if swapped == base {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		other, err := os.Stat(filepath.Join(parent, swapped))
		return err == nil && os.SameFile(info, other)
	}
}
