package tools

import (
	"encoding/json"
	"testing"
)

func TestContextContentSurvivesToolResultJSONConversion(t *testing.T) {
	// ADK normalizes tool outputs through map[string]any. The content must stay
	// readable without becoming float64 numbers during that conversion.
	const content = `{"parts":[{"functionResponse":{"name":"lookup","response":{"id":9007199254740993,"fraction":0.12345678901234567890123456789,"tiny":1e-1000}}}]}`
	encoded, err := json.Marshal(contextEntry{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	var restored contextEntry
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Content != content {
		t.Fatalf("tool conversion changed content: %s", restored.Content)
	}
}
