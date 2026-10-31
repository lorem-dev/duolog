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
		var out bytes.Buffer
		cmd := newRootCommand()
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"version"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.String() != v+"\n" {
			t.Fatal(out.String())
		}
	}
}
