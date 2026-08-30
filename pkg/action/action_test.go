package action

import "testing"

func TestActionKeepsStructuredArguments(t *testing.T) {
	got := Action{ID: "activation/0/0", Name: "echo", Args: []byte(`{"text":"hello"}`)}
	if got.ID == "" || got.Name == "" || string(got.Args) != `{"text":"hello"}` {
		t.Fatalf("Action = %#v", got)
	}
}
