package mcp

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestToolNamesAreTheFour(t *testing.T) {
	var names []string
	for _, tool := range Tools() {
		names = append(names, tool.Name)
	}
	want := []string{ToolDelegate, ToolPeer, ToolAnswerAsk, ToolStatus}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}
}

func TestSchemasStayUnderBudget(t *testing.T) {
	data, err := json.Marshal(Tools())
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		t.Fatal(err)
	}
	if compact.Len() > SchemaBudgetBytes {
		t.Fatalf("tool schemas are %d bytes, budget %d", compact.Len(), SchemaBudgetBytes)
	}
	t.Logf("tool schemas: %d bytes", compact.Len()) // the pinned total goes in the commit message
}

func TestDescriptionsAreShort(t *testing.T) {
	for _, tool := range Tools() {
		if len(tool.Description) > 220 || strings.Count(tool.Description, ". ") > 1 {
			t.Errorf("%s: description %q is over two sentences or 220 bytes", tool.Name, tool.Description)
		}
	}
}

// Every schema property is a JSON field of the input struct, and back.
func TestSchemaMatchesInputStructs(t *testing.T) {
	inputs := map[string]any{ToolDelegate: DelegateInput{}, ToolPeer: PeerInput{}, ToolAnswerAsk: AnswerAskInput{}}
	for _, tool := range Tools() {
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(tool.InputSchema, &s); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		in, ok := inputs[tool.Name]
		if !ok {
			if len(s.Properties) != 0 {
				t.Errorf("%s takes no input, schema has %v", tool.Name, s.Properties)
			}
			continue
		}
		fields := jsonFields(reflect.TypeOf(in))
		for name := range s.Properties {
			if !slices.Contains(fields, name) {
				t.Errorf("%s: schema property %q has no struct field", tool.Name, name)
			}
		}
		for _, f := range fields {
			if _, ok := s.Properties[f]; !ok {
				t.Errorf("%s: struct field %q is not in the schema", tool.Name, f)
			}
		}
	}
}

func TestAnswerAskIsNeverAllowed(t *testing.T) {
	if slices.Contains(AllowedTools, AnswerAskTool) || slices.Contains(AllowedTools, "mcp__duolog") {
		t.Fatalf("AllowedTools = %v must not cover answer_ask", AllowedTools)
	}
}

func TestPeerSchemaAcceptsOptionalWorktree(t *testing.T) {
	var schema struct {
		Properties map[string]struct{ Type string } `json:"properties"`
		Required   []string                         `json:"required"`
	}
	if err := json.Unmarshal([]byte(peerSchema), &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["worktree"].Type != "string" || slices.Contains(schema.Required, "worktree") {
		t.Fatal("peer open must accept an optional worktree string")
	}
}

func jsonFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}
