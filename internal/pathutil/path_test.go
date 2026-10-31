package pathutil

import "testing"

func TestWindowsPathsOnEveryHost(t *testing.T) {
	for _, tc := range []struct{ root, target, want string }{
		{`C:\Project`, `c:\PROJECT\src\..\SRC\Index.ts`, "src/index.ts"},
		{`C:/project`, `C:\Project\.claude\agents\Review.md`, ".claude/agents/review.md"},
		{`C:\project`, `C:\private\SECRET`, "../private/secret"},
		{`\\Server\Share\Project`, `\\server\share\project\src\file`, "src/file"},
	} {
		got, err := Relative(tc.root, tc.target)
		if err != nil || got != tc.want {
			t.Errorf("Relative(%q,%q)=%q %v", tc.root, tc.target, got, err)
		}
	}
	if _, err := Relative(`C:\project`, `D:\project\file`); err == nil {
		t.Fatal("different drives")
	}
	for _, name := range []string{`C:\outside`, `C:relative`, `\\host\share`, `..\secret`, `~\secret`} {
		if !Outside(name) {
			t.Errorf("outside path accepted: %q", name)
		}
	}
}

func TestWindowsRootsCannotBecomeRelative(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`C:\..\private`, "c:/private"},
		{`C:\project\..\..\secret`, "c:/secret"},
		{`\\server\share\..\..\secret`, "//server/share/secret"},
		{`C:\`, "c:/"},
	} {
		if got := Clean(tc.input); got != tc.want {
			t.Errorf("Clean(%q)=%q want %q", tc.input, got, tc.want)
		}
	}
	if got, err := Relative(`C:\`, `C:\file`); err != nil || got != "file" {
		t.Fatal(got, err)
	}
}
