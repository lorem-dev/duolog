// Package taskspec validates handoffs and builds bounded worker input. It
// reads task files and the files they name through fs.FS: a caller passes
// os.DirFS of a folder, and names are slash paths relative to it.
package taskspec

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/lorem-dev/duolog/internal/core/pathutil"
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
			advance, err := sectionAdvance(name, next, result)
			if err != nil {
				return nil, err
			}
			current = name
			result[name] = ""
			next += advance
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

// Validate checks that the task file name in fsys has its sections in order.
func Validate(fsys fs.FS, name string) error {
	data, err := fs.ReadFile(fsys, name)
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

// References returns the file paths in Read of the task file name in fsys,
// preserving inclusive line ranges. An item names a file when it is quoted
// with backticks, when its first word looks like a path, or when that word is
// a file in work, the work root, as Makefile is. Any other item is prose and
// names nothing. The task file need not be in work: a task copy can live in
// the shared state of another worktree.
func References(fsys fs.FS, name string, work fs.FS) ([]Reference, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	return references(data, work)
}

func references(data []byte, work fs.FS) ([]Reference, error) {
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
		if !named && !rootFile(work, r.Path) {
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
// a path, so prose such as "- the changelog entry" names nothing. The task
// file is name in fsys.
func WritePaths(fsys fs.FS, name string) ([]string, error) {
	return sectionPaths(fsys, name, "Write")
}

// ReadPaths returns the files, folders and globs that Read lists, by the
// rules of WritePaths, with a line range cut off. An entry may be absolute or
// start with ~.
func ReadPaths(fsys fs.FS, name string) ([]string, error) {
	return sectionPaths(fsys, name, "Read")
}

// sectionPaths returns the paths that a section of the task file name in fsys
// lists, as WritePaths says.
func sectionPaths(fsys fs.FS, name, section string) ([]string, error) {
	names, err := sectionNames(fsys, name, section)
	for i, n := range names {
		names[i] = pathutil.Clean(n)
	}
	return names, err
}

// WriteFolders returns the cleaned Write entries that were written with a
// trailing slash. They name a folder, even one that does not exist yet, which
// WritePaths no longer shows once the slash is cleaned off.
func WriteFolders(fsys fs.FS, name string) ([]string, error) {
	names, err := sectionNames(fsys, name, "Write")
	var folders []string
	for _, n := range names {
		if strings.HasSuffix(pathutil.Slashes(n), "/") {
			folders = append(folders, pathutil.Clean(n))
		}
	}
	return folders, err
}

// sectionNames returns the entries that a section lists, as written.
func sectionNames(fsys fs.FS, name, section string) ([]string, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	parts, err := parse(data)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(parts[section], "\n") {
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
			paths = append(paths, name)
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
// it as before rather than skipping it as prose. The word has no slash or
// dot, so it names an entry of the root itself.
func rootFile(work fs.FS, name string) bool {
	info, err := fs.Lstat(work, name)
	return err == nil && !info.IsDir()
}

// Locate names path, absolute or relative to root, as a slash path below
// root, and refuses a path outside it: "..", another folder's absolute path
// and a Windows drive path on another system. It looks at no file; Resolve
// then refuses links. full is the cleaned absolute path.
func Locate(root, path string) (name, full string, err error) {
	_, name, full, err = locate(root, path)
	return name, full, err
}

func locate(root, path string) (absRoot, name, full string, err error) {
	path = pathutil.Native(path)
	if pathutil.IsAbs(path) && !filepath.IsAbs(path) {
		return "", "", "", fmt.Errorf("path outside root: %s", path)
	}
	// Callers pass an absolute root; Abs only cleans it.
	if root, err = filepath.Abs(root); err != nil {
		return "", "", "", err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", "", "", fmt.Errorf("path outside root: %s", path)
	}
	return root, filepath.ToSlash(rel), path, nil
}

// Resolve refuses traversal and symlinks before any task input is opened.
// name is a slash path relative to the root of fsys: a name that leaves the
// root, as ".." or an absolute path does, is refused, and so is a symlink at
// any part of it. A missing part ends the walk, since nothing below it exists
// to be opened.
func Resolve(fsys fs.ReadLinkFS, name string) (string, error) {
	link, err := walk(fsys, name)
	if err != nil {
		return "", err
	}
	if link != "" {
		return "", fmt.Errorf("symlink input refused: %s", link)
	}
	return name, nil
}

// walk returns the first part of name that is a symlink, or "".
func walk(fsys fs.ReadLinkFS, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", fmt.Errorf("path outside root: %s", name)
	}
	current := ""
	for part := range strings.SplitSeq(name, "/") {
		current = path.Join(current, part)
		info, err := fsys.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return current, nil
		}
	}
	return "", nil
}

// Tree is a folder that task input is read from. FS reads below it, and Dir
// is its absolute path, so input may also name a file in it by an absolute
// path. Callers build FS with os.DirFS(dir), which reads links since Go 1.25.
type Tree struct {
	FS  fs.ReadLinkFS
	Dir string
}

// Resolve confines path, absolute or relative to the tree, to the tree, as
// Locate and Resolve do. It returns the slash name to open in t.FS and the
// cleaned absolute path. Errors name absolute paths, as they did when the
// walk used os.Lstat.
func (t Tree) Resolve(path string) (name, full string, err error) {
	root, name, full, err := locate(t.Dir, path)
	if err != nil {
		return "", "", err
	}
	link, err := walk(t.FS, name)
	if err != nil {
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) && fs.ValidPath(pathErr.Path) {
			pathErr.Path = filepath.Join(root, filepath.FromSlash(pathErr.Path))
		}
		return "", "", err
	}
	if link != "" {
		return "", "", fmt.Errorf("symlink input refused: %s", filepath.Join(root, filepath.FromSlash(link)))
	}
	return name, full, nil
}

// Text reads the reference from the work tree t.
func (r Reference) Text(t Tree) ([]byte, error) {
	data, _, err := r.read(t, -1)
	return data, err
}

// errDirectory is the cause for a reference that names a folder. Windows
// reports reading one as "Incorrect function", so the folder is found first.
var errDirectory = errors.New("a directory")

// read returns the reference's content when it holds at most limit bytes; a
// negative limit has none. Content over the limit is not read into memory, or
// not kept: fits is false, and the range is still checked against the file.
func (r Reference) read(t Tree, limit int) (content []byte, fits bool, err error) {
	name, full, err := t.Resolve(r.Path)
	if err != nil {
		return nil, false, err
	}
	f, err := t.FS.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, &fs.PathError{Op: "read", Path: full, Err: errDirectory}
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
		// read finds a folder with Stat before reading it, so the system's
		// own error for reading one never comes here.
		if errors.Is(pathErr.Err, errDirectory) {
			return "a directory"
		}
		return pathErr.Err.Error()
	}
	return err.Error()
}

// Inline builds the worker's input: the task file at path in task, then the
// files its Read section names in work, within maxBytes. task is work itself,
// or the folder that holds the task copy when the copy lives in the shared
// state of another worktree; the caller chooses it, and path must still lie
// in it. Read references are always confined to work.
func Inline(work, task Tree, path string, maxBytes int, allowRead ...func(string) bool) ([]byte, error) {
	name, _, err := task.Resolve(path)
	if err != nil {
		return nil, err
	}
	data, err := fs.ReadFile(task.FS, name)
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
	refs, err := references(data, work.FS)
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
		if _, _, err := work.Resolve(ref.Path); err != nil {
			return nil, err
		}
		head, tail := "\n<read path="+strconv.Quote(label)+">\n", "\n</read>\n"
		content, fits, err := ref.read(work, max(budget-len(head)-len(tail), 0))
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
	return name, otherLevels && next < len(sections) && (name == sections[next] || (next == 4 && name == "Run"))
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

// sectionAdvance accepts optional Run between Limits and Check exactly once.
func sectionAdvance(name string, next int, parts map[string]string) (int, error) {
	if name == "Run" && next == 4 {
		if _, exists := parts[name]; !exists {
			return 0, nil
		}
	}
	if next < len(sections) && name == sections[next] {
		return 1, nil
	}
	return 0, fmt.Errorf("task sections must be Goal, Read, Write, Limits, optional Run, Check in order; unexpected %q", name)
}
