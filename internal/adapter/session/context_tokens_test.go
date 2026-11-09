package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestClaudeContextTokens(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, lines ...string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	turn := func(in, read, create int) string {
		return `{"type":"assistant","message":{"model":"m","usage":{"input_tokens":` + strconv.Itoa(in) +
			`,"cache_read_input_tokens":` + strconv.Itoa(read) + `,"cache_creation_input_tokens":` + strconv.Itoa(create) + `,"output_tokens":9}}}`
	}
	user := `{"type":"user","message":{"role":"user","content":"hi"}}`

	p := write("a.jsonl", turn(1, 10, 100), user, turn(3, 4000, 500), user)
	if n, ok := ClaudeContextTokens(p); !ok || n != 4503 {
		t.Errorf("last turn = %d, %v; want 4503", n, ok)
	}
	// A turn with no usage, or all zero, is skipped for the one before it.
	p = write("b.jsonl", turn(2, 20, 30), `{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":0,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`,
		`{"type":"assistant","message":{"model":"m"}}`)
	if n, ok := ClaudeContextTokens(p); !ok || n != 52 {
		t.Errorf("skipped = %d, %v; want 52", n, ok)
	}
	// A sidechain turn is not the lead's.
	p = write("c.jsonl", turn(1, 1, 1), `{"type":"assistant","isSidechain":true,"message":{"usage":{"input_tokens":9999,"cache_read_input_tokens":0,"cache_creation_input_tokens":0}}}`)
	if n, ok := ClaudeContextTokens(p); !ok || n != 3 {
		t.Errorf("sidechain = %d, %v; want 3", n, ok)
	}
	// Unreadable: no file, no turn, junk.
	p = write("d.jsonl", user, "not json")
	for _, path := range []string{filepath.Join(dir, "missing"), p, ""} {
		if n, ok := ClaudeContextTokens(path); ok || n != 0 {
			t.Errorf("%q = %d, %v; want none", path, n, ok)
		}
	}
}
