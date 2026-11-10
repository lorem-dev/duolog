//go:build !windows

package quota

import (
	"context"
	"os/exec"
	"testing"
)

func TestStopUsesTreeCancellation(t *testing.T) {
	// Mutation killed: forced shutdown uses Process.Kill and skips configured tree cancellation.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "exec sleep 60")
	cancelled := false
	cmd.Cancel = func() error { cancelled = true; return cmd.Process.Kill() }
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop(cmd, nil)
	if !cancelled {
		t.Fatal("tree cancellation was skipped")
	}
}
