package project

import (
	"reflect"
	"testing"
)

// PyprojectTool reads only the [tool] table: the project's other tables do
// not reach it, a document without it gives nil, and a document that is not
// TOML gives an error.
func TestPyprojectTool(t *testing.T) {
	doc := "[project]\nname = \"widget\"\ndependencies = [\"attrs\"]\n\n[tool.pytest.ini_options]\naddopts = \"-q\"\n"
	got, err := PyprojectTool([]byte(doc))
	want := map[string]any{"pytest": map[string]any{"ini_options": map[string]any{"addopts": "-q"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("PyprojectTool = %#v, %v; want %#v", got, err, want)
	}
	if got, err := PyprojectTool([]byte("[project]\nname = \"widget\"\n")); err != nil || got != nil {
		t.Fatalf("PyprojectTool without a tool table = %#v, %v; want nil", got, err)
	}
	if got, err := PyprojectTool(nil); err != nil || got != nil {
		t.Fatalf("PyprojectTool of no document = %#v, %v; want nil", got, err)
	}
	if _, err := PyprojectTool([]byte("[tool\n")); err == nil {
		t.Fatal("PyprojectTool of a document that is not TOML gave no error")
	}
}
