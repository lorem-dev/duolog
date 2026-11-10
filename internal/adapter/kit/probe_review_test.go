//go:build !windows

package kit

import (
	"context"
	"os"
	"testing"
	"time"

	corelaunch "github.com/lorem-dev/duolog/internal/core/launch"
)

func TestProbeDeadlineEndsInheritedPipes(t *testing.T) {
	// Mutation killed: killing only the wrapper leaves Output/Wait blocked on child stdout.
	for _, metadata := range []bool{true, false} {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		start := time.Now()
		var err error
		if metadata {
			_, err = Metadata(ctx, "sh", "-c", "sleep 2 & wait")
		} else {
			_, err = RunProbe(ctx, corelaunch.Process{Prefix: []string{"sh"}, Env: os.Environ()}, t.TempDir(), []string{"-c", "sleep 2 & wait"}, func(int) error { return nil })
		}
		cancel()
		if err == nil || time.Since(start) > time.Second {
			t.Fatalf("metadata=%v elapsed=%s err=%v", metadata, time.Since(start), err)
		}
	}
}
