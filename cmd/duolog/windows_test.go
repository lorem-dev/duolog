package main

import (
	"github.com/lorem-dev/duolog/internal/peer"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsPeerNotice(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native Windows command")
	}
	isolatedRouteConfig(t)
	t.Chdir(t.TempDir())
	t.Setenv("PATH", t.TempDir()) // No rex, agents, Bash or PowerShell required.
	for _, args := range [][]string{{"peer"}, {"peer", "open", "plan"}, {"peer", "wait", "id"}, {"peer", "close", "id"}} {
		out, err := executeBusCommand(t, args...)
		if err != nil || out != peer.WindowsNotice+"\n" {
			t.Fatal(args, out, err)
		}
	}
	out, err := executeBusCommand(t, "doctor")
	if err != nil || !strings.Contains(out, "info: "+peer.WindowsNotice) {
		t.Fatal(out, err)
	}
}
