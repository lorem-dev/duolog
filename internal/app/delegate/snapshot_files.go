package delegate

import (
	"fmt"
	"hash"
	"io"
	"os"
)

// snapshotHash never follows a link outside the checkout and never opens a
// special file. Large regular files have bounded reads plus their size and mtime.
func snapshotHash(base, name string, h hash.Hash) []byte {
	root, err := os.OpenRoot(base)
	if err != nil {
		return []byte("unreadable")
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return h.Sum(nil)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := root.Readlink(name)
		if err == nil {
			_, _ = io.WriteString(h, link)
		}
	case info.Mode().IsRegular():
		f, err := root.Open(name)
		if err != nil {
			return []byte("unreadable")
		}
		defer f.Close()
		const limit = 64 << 20
		if _, err := io.Copy(h, io.LimitReader(f, limit)); err != nil {
			return []byte("unreadable")
		}
		if info.Size() > limit {
			_, _ = fmt.Fprintf(h, "size=%d mtime=%d", info.Size(), info.ModTime().UnixNano())
		}
	default:
		_, _ = io.WriteString(h, "special:"+info.Mode().String())
	}
	return h.Sum(nil)
}
