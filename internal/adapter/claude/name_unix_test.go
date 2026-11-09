//go:build unix

package claude

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO in place of the record does not block the caller.
func TestLeadNameDoesNotBlockOnAFIFO(t *testing.T) {
	home := isolateHome(t)
	claudeHome := filepath.Join(home, "claude-config")
	writeSessionRecord(t, claudeHome, 1, `{"name":"Real"}`)
	if err := syscall.Mkfifo(filepath.Join(claudeHome, "sessions", "4.json"), 0o600); err != nil {
		t.Skip("no FIFOs:", err)
	}
	got := make(chan string, 1)
	go func() { got <- (Lead{}).Name(os.Environ(), 4) }()
	select {
	case name := <-got:
		if name != "" {
			t.Fatalf("Name = %q", name)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Name blocked on a FIFO")
	}
}
