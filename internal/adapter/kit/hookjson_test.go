package kit

import (
	"encoding/json"
	"testing"
)

func TestAddFields(t *testing.T) {
	if got := string(AddFields(nil, map[string]string{"systemMessage": "x"})); got != "{\"systemMessage\":\"x\"}\n" {
		t.Fatalf("on nothing: %q", got)
	}
	got := AddFields([]byte("{\"decision\":\"block\",\"reason\":\"r\"}\n"), map[string]string{"systemMessage": "x"})
	var m map[string]string
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("%q: %v", got, err)
	}
	if m["decision"] != "block" || m["reason"] != "r" || m["systemMessage"] != "x" || got[len(got)-1] != '\n' {
		t.Fatalf("lost a key: %q", got)
	}
	for _, in := range []string{"not json\n", "[1,2]\n", "null"} {
		if got := string(AddFields([]byte(in), map[string]string{"systemMessage": "x"})); got != in {
			t.Fatalf("changed %q to %q", in, got)
		}
	}
}

func TestAddFieldsKeepsNestedJSON(t *testing.T) {
	in := "{\"hookSpecificOutput\":{\"hookEventName\":\"Stop\",\"n\":1}}\n"
	got := AddFields([]byte(in), map[string]string{"systemMessage": "x"})
	var m struct {
		Specific struct {
			N int `json:"n"`
		} `json:"hookSpecificOutput"`
		Msg string `json:"systemMessage"`
	}
	if err := json.Unmarshal(got, &m); err != nil || m.Specific.N != 1 || m.Msg != "x" {
		t.Fatalf("%q %v", got, err)
	}
}
