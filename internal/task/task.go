// Package task validates handoffs and builds bounded worker input.
package task

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
	"github.com/lorem-dev/duolog/internal/project"
)

var sections = []string{"Goal", "Read", "Write", "Limits", "Check"}

func parse(data []byte) (map[string]string, error) {
	result := make(map[string]string)
	otherLevels := !hasSecondLevelSections(string(data))
	current := ""
	next := 0
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			switch fence {
			case "":
				fence = marker
			case marker:
				fence = ""
			}
		}
		if name, ok := sectionHeading(line, next, otherLevels); fence == "" && ok {
			if next >= len(sections) || name != sections[next] {
				return nil, fmt.Errorf("task sections must be Goal, Read, Write, Limits, Check in order; unexpected %q", name)
			}
			current = name
			next++
			continue
		}
		if current != "" {
			result[current] += line + "\n"
		}
	}
	if next != len(sections) {
		return nil, fmt.Errorf("task missing section %s (a line \"## %s\")", sections[next], sections[next])
	}
	return result, nil
}

func Validate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = parse(data)
	return err
}

type Reference struct {
	Path        string
	First, Last int
}

var rangeSuffix = regexp.MustCompile(`^(.*):([0-9]+)(?:-([0-9]+))?$`)

// References returns the file paths in Read, preserving inclusive line ranges.
// An item names a file when it is quoted with backticks, when its first word
// looks like a path, or when that word is a file in the work root, as
// Makefile is. Any other item is prose and names nothing.
func References(path, root string) ([]Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts, err := parse(data)
	if err != nil {
		return nil, err
	}
	var refs []Reference
	for _, line := range strings.Split(parts["Read"], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
		var name string
		named := true
		if strings.HasPrefix(line, "`") {
			end := strings.Index(line[1:], "`")
			if end < 0 {
				return nil, fmt.Errorf("unclosed Read path")
			}
			name = line[1 : end+1]
			tail := line[end+2:]
			if strings.HasPrefix(tail, ":") && len(tail) > 1 && tail[1] >= '0' && tail[1] <= '9' {
				name += strings.Fields(tail)[0]
			}
		} else if fields := strings.Fields(line); len(fields) > 0 {
			name, named = fields[0], looksLikePath(fields[0])
		}
		// A list such as "- a.md, b.md" or a heading such as "- a.md:" ends
		// the path with punctuation that is not part of it.
		name = strings.TrimRight(name, ",:")
		if name == "" {
			continue
		}
		r := Reference{Path: name}
		match := rangeSuffix.FindStringSubmatch(name)
		if match != nil {
			r.Path = match[1]
		}
		if !named && !rootFile(root, r.Path) {
			continue // Prose, such as "- the notes from last week".
		}
		if match != nil {
			r.First, _ = strconv.Atoi(match[2])
			r.Last = r.First
			if match[3] != "" {
				r.Last, _ = strconv.Atoi(match[3])
			}
			if r.First < 1 || r.Last < r.First {
				return nil, fmt.Errorf("invalid Read range %q", name)
			}
		}
		r.Path = pathutil.Clean(r.Path)
		refs = append(refs, r)
	}
	return refs, nil
}

// WritePaths returns the files, folders and globs that Write lists, one per
// list item. An item counts only when it is quoted with backticks or looks like
// a path, so prose such as "- the changelog entry" names nothing.
func WritePaths(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parts, err := parse(data)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(parts["Write"], "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
		var name string
		if strings.HasPrefix(line, "`") {
			end := strings.Index(line[1:], "`")
			if end < 0 {
				continue
			}
			name = line[1 : end+1]
		} else if fields := strings.Fields(line); len(fields) > 0 && looksLikePath(fields[0]) {
			name = fields[0]
		}
		name = strings.TrimSuffix(name, ":")
		if match := rangeSuffix.FindStringSubmatch(name); match != nil {
			name = match[1]
		}
		if name != "" {
			paths = append(paths, pathutil.Clean(name))
		}
	}
	return paths, nil
}

// looksLikePath reports whether the first word of an unquoted list item names
// a file, folder or glob rather than starting prose such as "the changelog".
func looksLikePath(word string) bool {
	return strings.ContainsAny(word, `/\.*`)
}

// rootFile reports whether a bare word, such as Makefile, names something in
// the work root other than a folder. A link counts, so Inline still refuses
// it as before rather than skipping it as prose.
func rootFile(root, name string) bool {
	info, err := os.Lstat(filepath.Join(root, pathutil.Native(name)))
	return err == nil && !info.IsDir()
}

// Resolve refuses traversal and symlinks before opening any task input.
func Resolve(root, path string) (string, error) {
	path = pathutil.Native(path)
	if pathutil.IsAbs(path) && !filepath.IsAbs(path) {
		return "", fmt.Errorf("path outside root: %s", path)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path outside root: %s", path)
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink input refused: %s", current)
		}
	}
	return path, nil
}

func (r Reference) Text(root string) ([]byte, error) {
	data, _, err := r.read(root, -1)
	return data, err
}

// errDirectory is the cause for a reference that names a folder. Windows
// reports reading one as "Incorrect function", so the folder is found first.
var errDirectory = errors.New("a directory")

// read returns the reference's content when it holds at most limit bytes; a
// negative limit has none. Content over the limit is not read into memory, or
// not kept: fits is false, and the range is still checked against the file.
func (r Reference) read(root string, limit int) (content []byte, fits bool, err error) {
	path, err := Resolve(root, r.Path)
	if err != nil {
		return nil, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, &fs.PathError{Op: "read", Path: path, Err: errDirectory}
	}
	if r.First == 0 {
		if limit < 0 {
			data, err := io.ReadAll(f)
			return data, err == nil, err
		}
		if info.Size() > int64(limit) {
			return nil, false, nil
		}
		// The file can grow after Stat, so the read stops past the limit.
		data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
		if err != nil {
			return nil, false, err
		}
		if len(data) > limit {
			return nil, false, nil
		}
		return data, true, nil
	}
	// Lines end after "\n"; the last one may have none.
	reader := bufio.NewReader(f)
	var kept bytes.Buffer // It doubles as it grows, so a range costs little more than it keeps.
	fits = true
	lines, open := 0, false
	for lines < r.Last {
		chunk, err := reader.ReadSlice('\n')
		if len(chunk) > 0 && lines+1 >= r.First && fits {
			if limit >= 0 && kept.Len()+len(chunk) > limit {
				fits = false
				kept = bytes.Buffer{}
			} else {
				kept.Write(chunk)
			}
		}
		switch {
		case err == nil:
			lines, open = lines+1, false
			continue
		case errors.Is(err, bufio.ErrBufferFull):
			open = true // The line goes on in the next chunk.
			continue
		case !errors.Is(err, io.EOF):
			return nil, false, err
		}
		if open || len(chunk) > 0 {
			lines++
		}
		break
	}
	if lines < r.Last {
		return nil, false, &rangeError{path: r.Path, lines: lines}
	}
	if !fits {
		return nil, false, nil
	}
	// A range holds at least one line, so this is never empty.
	return kept.Bytes(), true, nil
}

// rangeError is a Read range that ends past the file's last line.
type rangeError struct {
	path  string
	lines int
}

func (e *rangeError) Error() string {
	return fmt.Sprintf("Read range exceeds %s, which has %d lines", e.path, e.lines)
}

// readCause says why a reference could not be read, without its absolute path.
func readCause(err error) string {
	var short *rangeError
	if errors.As(err, &short) {
		return fmt.Sprintf("the range ends past line %d", short.lines)
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		if errors.Is(pathErr.Err, fs.ErrNotExist) {
			return "no such file"
		}
		if errors.Is(pathErr.Err, errDirectory) || errors.Is(pathErr.Err, syscall.EISDIR) {
			return "a directory"
		}
		return pathErr.Err.Error()
	}
	return err.Error()
}

func Inline(path, root string, maxBytes int, allowRead ...func(string) bool) ([]byte, error) {
	// The task copy can live in the shared state of another worktree. Its source
	// is resolved by the caller; read references are still confined to work root.
	var err error
	if _, resolveErr := Resolve(root, path); resolveErr != nil {
		shared := filepath.Join(project.StateRoot(root), ".duolog", "tasks")
		// The copy path can use an OS alias of the shared state directory.
		candidate := path
		if canonical, e := filepath.EvalSymlinks(path); e == nil {
			candidate = canonical
		}
		if canonical, e := filepath.EvalSymlinks(shared); e == nil {
			shared = canonical
		}
		if _, err := Resolve(shared, candidate); err != nil {
			return nil, resolveErr
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	prefix, suffix := []byte("<stdin>\n"), []byte("\n</stdin>\n")
	// The notes below only add to the task, so a task that alone exceeds the
	// limit fails before any reference is judged or opened.
	budget := maxBytes - len(prefix) - len(data) - len(suffix)
	if budget < 0 {
		return nil, fmt.Errorf("task exceeds inline_max_bytes (%d)", maxBytes)
	}
	refs, err := References(path, root)
	if err != nil {
		return nil, err
	}
	// The notes say what each reference lacks; reads keeps only content that
	// fits what the task and the reads before it leave, so a reference over
	// the limit is never held whole.
	var refused, unreadable []string
	var reads [][]byte
	for _, ref := range refs {
		label := ref.Path
		if ref.First > 0 {
			label += fmt.Sprintf(":%d-%d", ref.First, ref.Last)
		}
		if len(allowRead) > 0 && !allowRead[0](ref.Path) {
			refused = append(refused, ref.Path)
			continue
		}
		if _, err := Resolve(root, ref.Path); err != nil {
			return nil, err
		}
		head, tail := "\n<read path="+strconv.Quote(label)+">\n", "\n</read>\n"
		content, fits, err := ref.read(root, max(budget-len(head)-len(tail), 0))
		if err != nil {
			// The worker is told what is missing and why, without the work root.
			unreadable = append(unreadable, label+" ("+readCause(err)+")")
			continue
		}
		if !fits || len(head)+len(content)+len(tail) > budget {
			continue // It stays a reference in the task.
		}
		addition := append(append([]byte(head), content...), tail...)
		reads = append(reads, addition)
		budget -= len(addition)
	}
	var notes string
	if len(refused) > 0 {
		notes += "Not inlined by read policy: " + strings.Join(refused, ", ") + ". Keep these as references.\n"
	}
	if len(unreadable) > 0 {
		notes += "Not inlined, could not be read: " + strings.Join(unreadable, ", ") + ". Check these before relying on them.\n"
	}
	if notes != "" {
		data = []byte(Annotate(string(data), notes))
	}
	block := append(slices.Clip(prefix), data...)
	if len(block)+len(suffix) > maxBytes {
		return nil, fmt.Errorf("task exceeds inline_max_bytes (%d)", maxBytes)
	}
	// The notes take room the reads were measured against; a read that no
	// longer fits stays a reference.
	for _, addition := range reads {
		if len(block)+len(addition)+len(suffix) <= maxBytes {
			block = append(block, addition...)
		}
	}
	return append(block, suffix...), nil
}

// Annotate uses the same fence and heading rules as the task parser.
func Annotate(text, lines string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	fence, offset, next := "", 0, 0
	otherLevels := !hasSecondLevelSections(text)
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			switch fence {
			case "":
				fence = marker
			case marker:
				fence = ""
			}
		}
		offset += len(line)
		if name, ok := sectionHeading(strings.TrimRight(line, "\n"), next, otherLevels); fence == "" && ok && next < len(sections) && name == sections[next] {
			if name == "Limits" {
				return text[:offset] + lines + text[offset:]
			}
			next++
		}
	}
	return text
}

// sectionHeading reads a heading that starts a section. A "## " line always
// does, and a wrong name there is an error. Agents also write "# Goal" or
// "### Goal": when a task has no "## " section at all, those levels count if
// they name the section due next, and are ordinary text otherwise. A task
// with "## " sections keeps every other heading as text, as 1.0 did.
func sectionHeading(line string, next int, otherLevels bool) (string, bool) {
	level := len(line) - len(strings.TrimLeft(line, "#"))
	if level < 1 || level > 3 || !strings.HasPrefix(line[level:], " ") {
		return "", false
	}
	name := strings.TrimSpace(line[level:])
	if level == 2 {
		return name, true
	}
	return name, otherLevels && next < len(sections) && name == sections[next]
}

// hasSecondLevelSections reports whether a "## " line outside a fence names
// one of the sections.
func hasSecondLevelSections(text string) bool {
	fence := ""
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			switch fence {
			case "":
				fence = marker
			case marker:
				fence = ""
			}
		}
		if fence == "" && strings.HasPrefix(line, "## ") && slices.Contains(sections, strings.TrimSpace(line[3:])) {
			return true
		}
	}
	return false
}
