package process

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/lorem-dev/duolog/internal/core/run"
)

// A missing program keeps the text of its cause and matches both the cause and
// run.ErrBinaryMissing.
func TestMissingBinaryKeepsText(t *testing.T) {
	cause := &exec.Error{Name: "claude", Err: exec.ErrNotFound}
	err := error(missingBinary{cause})
	if err.Error() != cause.Error() {
		t.Fatalf("text %q, want %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, exec.ErrNotFound) || !errors.Is(err, run.ErrBinaryMissing) {
		t.Fatalf("%v does not match both ErrNotFound and ErrBinaryMissing", err)
	}
}
