package claude

import (
	"bytes"
	"encoding/json"
	"testing"
)

func parseSettings(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return doc
}

func checkOurEntry(t *testing.T, doc map[string]any) {
	t.Helper()
	line, _ := doc["statusLine"].(map[string]any)
	if line["type"] != "command" || line["command"] != "duolog statusline" || line["refreshInterval"] != float64(10) {
		t.Fatalf("statusLine %v", line)
	}
}

func TestStatusLineAddKeepsOthers(t *testing.T) {
	var i Installer
	data, err := i.StatusLine([]byte(`{"env":{"A":"1"}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseSettings(t, data)
	if env, _ := doc["env"].(map[string]any); env["A"] != "1" {
		t.Fatalf("env %v", doc["env"])
	}
	checkOurEntry(t, doc)
	data, err = i.StatusLine(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	doc = parseSettings(t, data)
	if len(doc) != 1 {
		t.Fatalf("doc %v", doc)
	}
	checkOurEntry(t, doc)
}

func TestStatusLineNeverReplaces(t *testing.T) {
	var i Installer
	theirs := []byte(`{"statusLine":{"type":"command","command":"~/my.sh"}}`)
	for _, add := range []bool{true, false} {
		got, err := i.StatusLine(theirs, add)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, theirs) {
			t.Fatalf("add %v changed %s", add, got)
		}
	}
}

func TestStatusLineRemoveOurs(t *testing.T) {
	var i Installer
	ours, err := i.StatusLine([]byte(`{"env":{"A":"1"}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := i.StatusLine(ours, false)
	if err != nil {
		t.Fatal(err)
	}
	doc := parseSettings(t, got)
	if _, ok := doc["statusLine"]; ok || len(doc) != 1 || doc["env"] == nil {
		t.Fatalf("doc %v", doc)
	}
	if gotOurs, any := i.OurStatusLine(ours); !gotOurs || !any {
		t.Fatalf("ours: %v %v", gotOurs, any)
	}
	if gotOurs, any := i.OurStatusLine([]byte(`{"statusLine":{"type":"command","command":"~/my.sh"}}`)); gotOurs || !any {
		t.Fatalf("theirs: %v %v", gotOurs, any)
	}
	if gotOurs, any := i.OurStatusLine([]byte(`{"env":{}}`)); gotOurs || any {
		t.Fatalf("none: %v %v", gotOurs, any)
	}
}
