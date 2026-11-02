package mcp

import (
	"errors"
	"testing"

	"github.com/anra-studio/anra/pkg/model/projection"
)

func TestToolStreamClosesOnlyOnTerminalResponse(t *testing.T) {
	calls := make(openToolCalls)
	call := &projection.Part{FunctionCall: &projection.FunctionCall{ID: "call-1", Name: "lookup"}}
	if err := calls.observe(call); err != nil {
		t.Fatal(err)
	}
	for _, continuing := range []bool{true, true, false} {
		response := &projection.Part{FunctionResponse: &projection.FunctionResponse{ID: "call-1", Name: "lookup", WillContinue: &continuing}}
		if err := calls.observe(response); err != nil {
			t.Fatal(err)
		}
		if (len(calls) == 1) != continuing {
			t.Fatalf("pending calls = %v after willContinue=%t", calls, continuing)
		}
	}
	if err := calls.observe(&projection.Part{FunctionResponse: &projection.FunctionResponse{ID: "call-1", Name: "lookup"}}); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("response after termination = %v; want ErrFailedPrecondition", err)
	}
}

func TestToolResponsesMustMatchOneOpenCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		part *projection.Part
	}{
		{"duplicate call", &projection.Part{FunctionCall: &projection.FunctionCall{ID: "call-1", Name: "lookup"}}},
		{"orphan response", &projection.Part{FunctionResponse: &projection.FunctionResponse{ID: "call-2", Name: "lookup"}}},
		{"wrong tool", &projection.Part{FunctionResponse: &projection.FunctionResponse{ID: "call-1", Name: "other"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := openToolCalls{"id:call-1": "lookup"}
			if err := calls.observe(tc.part); !errors.Is(err, ErrFailedPrecondition) {
				t.Fatalf("observe() = %v; want ErrFailedPrecondition", err)
			}
			if len(calls) != 1 || calls["id:call-1"] != "lookup" {
				t.Fatalf("rejected part changed pending call: %v", calls)
			}
		})
	}
}
