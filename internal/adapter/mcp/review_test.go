package mcp

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestServeCancellationInterruptsIdleInput(t *testing.T) {
	// Mutation killed: waiting for ReadBytes on an open pipe ignores cancellation.
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&Server{}).Serve(ctx, r, io.Discard) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
}
