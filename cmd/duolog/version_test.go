package main

import (
	"bytes"
	"testing"
)

func TestVersion(t *testing.T) {
	old := version
	defer func() { version = old }()
	for _, v := range []string{"dev", "1.2.3"} {
		version = v
		// The flags print the same line as the command.
		for _, args := range [][]string{{"version"}, {"--version"}, {"-V"}} {
			var out bytes.Buffer
			cmd := newRootCommand(testDeps(t))
			cmd.SetOut(&out)
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(args, err)
			}
			if out.String() != v+"\n" {
				t.Fatalf("%v: %q", args, out.String())
			}
		}
	}
}
