// Package rex drives panes through the Rex command line. It never interprets
// what a pane prints: callers get a bounded tail and treat it as data.
package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ErrSocket reports exit code 4, which a sandbox causes by blocking the Rex socket.
var ErrSocket = errors.New("rex socket is unreachable (exit 4); this duolog command must leave the sandbox")

// Timeouts are variables so tests need not wait for them.
var (
	// CommandTimeout bounds every rex call that is not a wait.
	CommandTimeout = 15 * time.Second
	// ExitWait is how long the exit probe waits for a running program.
	ExitWait = 500 * time.Millisecond
)

const (
	maxOutput = 256 * 1024
	maxStderr = 4 * 1024
)

// Client is a connection to one Rex session.
type Client struct {
	Bin     string
	Session string
	// Anchor is the pane a split goes beside; empty means the focused pane.
	Anchor string
	// Dir is the new pane's working directory. Label names a split pane or a new tab.
	Dir   string
	Label string
}

// Presence is what Rex says about a block.
type Presence int

const (
	Running Presence = iota
	Exited
	Gone
)

// Error is a failed rex call. Output holds a bounded, single-line excerpt.
type Error struct {
	Cmd    string
	Code   int
	Output string
}

func (e *Error) Error() string {
	return fmt.Sprintf("rex %s failed (exit %d): %s", e.Cmd, e.Code, e.Output)
}

// IsNotFound reports the recorded closed-pane output: exit 3 and "not found".
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == 3 && strings.Contains(e.Output, "not found")
}

// Locate finds rex in PATH, then in binDir (REX_BIN_DIR), then in the first
// match of globs, which are application bundle patterns. It skips the other
// program named rex; see Shadow.
func Locate(binDir string, globs []string) (string, bool) {
	if path, err := exec.LookPath("rex"); err == nil && !foreign(path) {
		return path, true
	}
	name := "rex"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if binDir != "" {
		path := filepath.Join(binDir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	for _, pattern := range globs {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, path := range matches {
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				return path, true
			}
		}
	}
	return "", false
}

// Shadow returns the rex in PATH when it is not the Rex CLI, so that doctor can
// say why the command rex starts something else.
func Shadow() (string, bool) {
	path, err := exec.LookPath("rex")
	return path, err == nil && foreign(path)
}

// foreign reports whether path is a script, such as the Perl deployment tool
// that package managers also install as rex. Given Rex arguments that tool runs
// tasks from a Rexfile, so duolog does not start it. The Rex CLI is a compiled
// program, so the first line tells them apart without running either. A rex in
// REX_BIN_DIR is the user's own choice and is not checked.
func foreign(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, _ := bufio.NewReader(io.LimitReader(f, 256)).ReadString('\n')
	return strings.HasPrefix(line, "#!")
}

// BundleGlobs lists the macOS application locations of the rex helper.
func BundleGlobs() []string {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return []string{"/Applications/Rex*.app/Contents/Helpers/*/Contents/MacOS/rex"}
}

// Detect returns a client when the process runs inside a Rex session and a rex
// executable can be found.
func Detect() (Client, bool) {
	session := os.Getenv("REX_SESSION")
	if session == "" {
		return Client{}, false
	}
	bin, ok := Locate(os.Getenv("REX_BIN_DIR"), BundleGlobs())
	if !ok {
		return Client{}, false
	}
	return Client{Bin: bin, Session: session, Anchor: os.Getenv("REX_BLOCK")}, true
}

// tail keeps the last limit bytes written to it.
type tail struct {
	limit int
	buf   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.limit:]...)
	}
	return len(p), nil
}

// head keeps the first limit bytes written to it.
type head struct {
	limit int
	buf   []byte
}

func (h *head) Write(p []byte) (int, error) {
	if room := h.limit - len(h.buf); room > 0 {
		h.buf = append(h.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

type result struct {
	stdout string
	stderr string
	code   int
}

// run reports a nonzero exit as a result, not an error: callers decide what
// each code means. Stdin is explicit and empty.
func (c Client) run(ctx context.Context, timeout time.Duration, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Stdin = strings.NewReader("")
	out := &tail{limit: maxOutput}
	errs := &head{limit: maxStderr}
	cmd.Stdout, cmd.Stderr = out, errs
	err := cmd.Run()
	r := result{stdout: string(out.buf), stderr: string(errs.buf)}
	if cmd.ProcessState != nil {
		r.code = cmd.ProcessState.ExitCode()
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return r, err
	}
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	return r, nil
}

// call runs a command that must succeed.
func (c Client) call(ctx context.Context, args ...string) (string, error) {
	r, err := c.run(ctx, CommandTimeout, args...)
	if err != nil {
		return "", err
	}
	if r.code == 4 {
		return "", ErrSocket
	}
	if r.code != 0 {
		return "", &Error{Cmd: args[0], Code: r.code, Output: oneLine(r.stderr + " " + r.stdout)}
	}
	return r.stdout, nil
}

// Split opens a pane beside the anchor without taking focus and returns the new
// block ID. The program starts directly, with no shell.
func (c Client) Split(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("rex split: missing command")
	}
	args := []string{"split", "-s", c.Session}
	if c.Anchor != "" {
		args = append(args, "-b", c.Anchor)
	}
	args = append(args, "--focus=false", "--json", "--shell=none")
	if c.Dir != "" {
		args = append(args, "-c", c.Dir)
	}
	if c.Label != "" {
		args = append(args, "--label", c.Label)
	}
	args = append(args, "--")
	out, err := c.call(ctx, append(args, argv...)...)
	if err != nil {
		return "", err
	}
	var created struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || len(created.BlockIDs) == 0 || created.BlockIDs[0] == "" {
		return "", errors.New("rex split: reply has no block_ids[0]")
	}
	return created.BlockIDs[0], nil
}

// NewWindow opens a tab in the same session without changing the lead's focus.
// NAME labels the tab; the program starts directly, with no shell.
func (c Client) NewWindow(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("rex window new: missing command")
	}
	args := []string{"window", "new", "-s", c.Session}
	if c.Label != "" {
		args = append(args, c.Label)
	}
	args = append(args, "--focus=false", "--json", "--shell=none")
	if c.Dir != "" {
		args = append(args, "-c", c.Dir)
	}
	args = append(args, "--")
	out, err := c.call(ctx, append(args, argv...)...)
	if err != nil {
		return "", err
	}
	var created struct {
		BlockIDs []string `json:"block_ids"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || len(created.BlockIDs) == 0 || created.BlockIDs[0] == "" {
		return "", errors.New("rex window new: reply has no block_ids[0]")
	}
	return created.BlockIDs[0], nil
}

// WindowID reads a pane's current window. Empty block selects the focused pane.
func (c Client) WindowID(ctx context.Context, block string) (string, error) {
	args := []string{"block", "inspect", "-s", c.Session, "--json"}
	if block != "" {
		args = append(args, block)
	}
	out, err := c.call(ctx, args...)
	if err != nil {
		return "", err
	}
	var inspected struct {
		Block struct {
			WindowID string `json:"window_id"`
		} `json:"block"`
	}
	if err := json.Unmarshal([]byte(out), &inspected); err != nil || inspected.Block.WindowID == "" {
		return "", errors.New("rex block inspect: reply has no block.window_id")
	}
	return inspected.Block.WindowID, nil
}

// Send types text into a pane without pressing enter. The caller owns the
// pause before SendKey.
func (c Client) Send(ctx context.Context, block, text string) error {
	_, err := c.call(ctx, "send", "-s", c.Session, "-b", block, text)
	return err
}

// SendKey presses keys, such as enter or ctrl+u, in a pane.
func (c Client) SendKey(ctx context.Context, block string, keys ...string) error {
	_, err := c.call(ctx, append([]string{"send-key", "-s", c.Session, "-b", block}, keys...)...)
	return err
}

// Capture returns the last lines non-blank lines of a pane, without escape
// sequences. The rest of the scrollback is dropped here.
func (c Client) Capture(ctx context.Context, block string, lines int) ([]byte, error) {
	out, err := c.call(ctx, "capture", "-s", c.Session, "-b", block, "--trim", "--unwrap")
	if err != nil {
		return nil, err
	}
	return lastLines(out, lines, false), nil
}

// CaptureStyled is Capture that keeps the SGR sequences of the capture: the
// colours and weights that tell an agent's dim suggestion from typed text.
// Every other escape sequence and control character is dropped, and a line
// that holds only styles counts as blank.
func (c Client) CaptureStyled(ctx context.Context, block string, lines int) ([]byte, error) {
	// Rex's default format is plain text; only the vt format keeps the styles.
	out, err := c.call(ctx, "capture", "-s", c.Session, "-b", block, "--format", "vt", "--trim", "--unwrap")
	if err != nil {
		return nil, err
	}
	return lastLines(out, lines, true), nil
}

func lastLines(text string, n int, styled bool) []byte {
	if n <= 0 {
		n = 20
	}
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimRightFunc(clean(line, false), unicode.IsSpace) == "" {
			continue
		}
		kept = append(kept, strings.TrimRightFunc(clean(line, styled), unicode.IsSpace))
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return []byte(strings.Join(kept, "\n"))
}

// escapeRe matches a CSI sequence, an OSC sequence such as a hyperlink, and a
// two-byte escape such as a charset switch. sgrRe is the CSI that sets styles.
var (
	escapeRe = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)?|[ -/]*[0-~])") //nolint:gocritic // ECMA-48 ranges: [ -/] intermediate bytes 0x20-0x2F, [@-~] final bytes 0x40-0x7E
	sgrRe    = regexp.MustCompile("^\x1b\\[[0-9;:]*m$")
)

// clean drops escape sequences and control characters. keepSGR keeps the
// sequences that set styles and nothing else.
func clean(s string, keepSGR bool) string {
	var b strings.Builder
	last := 0
	for _, at := range escapeRe.FindAllStringIndex(s, -1) {
		b.WriteString(dropControls(s[last:at[0]]))
		if seq := s[at[0]:at[1]]; keepSGR && sgrRe.MatchString(seq) {
			b.WriteString(seq)
		}
		last = at[1]
	}
	b.WriteString(dropControls(s[last:]))
	return b.String()
}

func dropControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(clean(s, false)), " ")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Probe tells a running program from an exited one and from a closed pane.
// Both inspect and capture succeed for an exited program, and rex send loses
// its text, so only the wait result separates the cases: it prints exit_code
// once the program has ended and nothing before that.
func (c Client) Probe(ctx context.Context, block string) (Presence, error) {
	r, err := c.run(ctx, CommandTimeout, "block", "inspect", "-s", c.Session, "--json", block)
	if err != nil {
		return Gone, err
	}
	if r.code == 4 {
		return Gone, ErrSocket
	}
	if r.code != 0 {
		e := &Error{Cmd: "block inspect", Code: r.code, Output: oneLine(r.stderr + " " + r.stdout)}
		if IsNotFound(e) {
			return Gone, nil
		}
		return Gone, e
	}
	r, err = c.run(ctx, ExitWait+CommandTimeout, "wait", "-s", c.Session, block, "--for", ExitWait.String(), "--json")
	if err != nil {
		return Gone, err
	}
	if r.code == 4 {
		return Gone, ErrSocket
	}
	var ended struct {
		ExitCode *int `json:"exit_code"`
	}
	if json.Unmarshal(bytes.TrimSpace([]byte(r.stdout)), &ended) == nil && ended.ExitCode != nil {
		return Exited, nil
	}
	out := oneLine(r.stderr + " " + r.stdout)
	if IsNotFound(&Error{Code: r.code, Output: out}) {
		return Gone, nil
	}
	// Measured on real rex: wait on a running program exits 1 with
	// "wait timed out after 500ms". Any other result is an error, never Running.
	if r.code == 1 && strings.Contains(out, "wait timed out") {
		return Running, nil
	}
	return Gone, &Error{Cmd: "wait", Code: r.code, Output: out}
}

// Alive reports whether the program in a block still runs. It captures at most
// lines lines to confirm that the pane answers, and drops them.
func (c Client) Alive(ctx context.Context, block string, lines int) (bool, error) {
	presence, err := c.Probe(ctx, block)
	if err != nil || presence != Running {
		return false, err
	}
	if _, err := c.Capture(ctx, block, lines); err != nil {
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Close closes a pane. A pane that is already gone is not an error.
func (c Client) Close(ctx context.Context, block string) error {
	_, err := c.call(ctx, "block", "close", "-s", c.Session, block)
	if IsNotFound(err) {
		return nil
	}
	return err
}
