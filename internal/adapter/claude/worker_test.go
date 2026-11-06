package claude

import (
	"strings"
	"testing"
)

// TestAnswerSchemaKeepsDecisions: the schema Claude is asked to answer in names
// the worker's decisions, as Codex's instruction does.
func TestAnswerSchemaKeepsDecisions(t *testing.T) {
	if !strings.Contains(answerSchema, `"decisions"`) {
		t.Fatal("the answer format misses decisions")
	}
}
