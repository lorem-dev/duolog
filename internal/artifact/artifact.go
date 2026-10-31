package artifact

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/store"
)

// Put saves an immutable snapshot and returns its content-addressed reference.
func Put(s *store.Store, path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	hash := fmt.Sprintf("%x", sum)[:12]
	dir := filepath.Join(s.Dir, "artifacts")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, hash)
	if existing, err := os.ReadFile(dest); err == nil {
		if !bytes.Equal(existing, content) {
			return "", fmt.Errorf("artifact hash collision: %s", hash)
		}
		return "a/" + hash, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".snapshot-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(file.Name(), dest); err != nil {
		// Another writer may have published the same snapshot on Windows.
		if existing, readErr := os.ReadFile(dest); readErr != nil || !bytes.Equal(existing, content) {
			return "", err
		}
	}
	return "a/" + hash, nil
}

// RepoRef checks a project-relative path and records its current Git blob hash.
func RepoRef(root, spec string) (string, error) {
	name, selection, err := parseRange(spec)
	if err != nil {
		return "", err
	}
	path, name, err := repoPath(root, name)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return name + "@" + blobHash(content) + selection.suffix(), nil
}

// Read returns snapshot bytes or current repository bytes, marking changed blobs stale.
func Read(s *store.Store, root, ref string) (content []byte, stale bool, err error) {
	name, selection, err := parseRange(ref)
	if err != nil {
		return nil, false, err
	}
	name = strings.ReplaceAll(name, `\`, "/")
	if at := strings.LastIndexByte(name, '@'); at >= 0 {
		hash := name[at+1:]
		if !validHash(hash, 8) {
			return nil, false, fmt.Errorf("invalid blob hash in ref %q", ref)
		}
		path, _, err := repoPath(root, name[:at])
		if err != nil {
			return nil, false, err
		}
		content, err = os.ReadFile(path)
		if err != nil {
			return nil, false, err
		}
		stale = blobHash(content) != hash
	} else {
		if !strings.HasPrefix(name, "a/") || !validHash(strings.TrimPrefix(name, "a/"), 12) {
			return nil, false, fmt.Errorf("invalid artifact ref %q", ref)
		}
		content, err = os.ReadFile(filepath.Join(s.Dir, "artifacts", name[2:]))
		if err != nil {
			return nil, false, err
		}
	}
	return selection.apply(content), stale, nil
}

func blobHash(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d%c", len(content), 0)
	h.Write(content)
	return fmt.Sprintf("%x", h.Sum(nil))[:8]
}

func validHash(hash string, length int) bool {
	_, err := hex.DecodeString(hash)
	return len(hash) == length && err == nil && hash == strings.ToLower(hash)
}

func repoPath(root, name string) (string, string, error) {
	name = strings.ReplaceAll(name, `\`, "/")
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", "", fmt.Errorf("ref must be relative to project root: %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", "", fmt.Errorf("ref outside project root: %q", name)
	}
	// Refuse symlink components so a relative ref cannot escape through a link.
	path := root
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("symlink in ref: %q", name)
		}
	}
	return path, filepath.ToSlash(clean), nil
}

type lineRange struct {
	first int
	last  int
}

func parseRange(spec string) (string, lineRange, error) {
	at := strings.LastIndexByte(spec, ':')
	if at < 0 {
		return spec, lineRange{}, nil
	}
	start, end, ok := strings.Cut(spec[at+1:], "-")
	first, firstErr := strconv.Atoi(start)
	last, lastErr := strconv.Atoi(end)
	if !ok || firstErr != nil || lastErr != nil || first < 1 || last < first {
		return "", lineRange{}, fmt.Errorf("invalid line range in %q", spec)
	}
	return spec[:at], lineRange{first, last}, nil
}

func (r lineRange) suffix() string {
	if r.first == 0 {
		return ""
	}
	return fmt.Sprintf(":%d-%d", r.first, r.last)
}

func (r lineRange) apply(content []byte) []byte {
	if r.first == 0 {
		return content
	}
	start := 0
	line := 1
	for i, b := range content {
		if b != '\n' {
			continue
		}
		if line == r.last {
			return content[start : i+1]
		}
		line++
		if line <= r.first {
			start = i + 1
		}
	}
	if line < r.first {
		return content[len(content):]
	}
	return content[start:]
}
