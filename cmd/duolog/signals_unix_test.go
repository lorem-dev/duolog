//go:build !windows

package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
)

func TestHangupStopsTheWorker(t *testing.T) {
	if !slices.Contains(terminationSignals(), os.Signal(syscall.SIGHUP)) {
		t.Fatal("SIGHUP would leave the worker running with nobody watching")
	}
}
