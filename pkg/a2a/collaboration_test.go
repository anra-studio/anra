package a2a

import (
	"errors"
	"strings"
	"testing"

	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
)

func TestDecodeCollaborationRequestMetadata(t *testing.T) {
	t.Parallel()

	const deadline = "2026-10-20T00:00:00Z"
	value := map[string]any{
		"rootRequestId": "delegation-root",
		"deadline":      deadline,
		"ancestors":     []string{"alice", "bob"},
	}
	var request cp.DelegationMetadata
	if err := decodeCollaborationMetadata(value, &request); err != nil {
		t.Fatal(err)
	}
	if request.RootRequestId != "delegation-root" || request.Deadline.AsTime().Format("2006-01-02T15:04:05Z") != deadline {
		t.Fatalf("decoded delegation = %v", &request)
	}

	value["deadline"] = "not-a-timestamp"
	if err := decodeCollaborationMetadata(value, new(cp.DelegationMetadata)); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("invalid timestamp returned %v", err)
	}

	// Each ancestor is valid on its own. Only the metadata envelope exceeds the limit.
	value["deadline"] = deadline
	ancestors := make([]string, maxCollaborationMetadataBytes/128+1)
	for i := range ancestors {
		ancestors[i] = strings.Repeat("x", 128)
	}
	value["ancestors"] = ancestors
	if err := decodeCollaborationMetadata(value, new(cp.DelegationMetadata)); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("oversized metadata returned %v", err)
	}
}

func TestDecodeCollaborationResponseMetadata(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		value map[string]any
	}{
		{
			name:  "unknown numeric status",
			value: map[string]any{"status": 99},
		},
		{
			name:  "invalid retry enum",
			value: map[string]any{"status": "STATUS_BUSY", "retry": 99},
		},
		{
			name:  "answer outside message parts",
			value: map[string]any{"status": "STATUS_COMPLETED", "answer": "text"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeCollaborationMetadata(tc.value, new(cp.ResponseMetadata)); !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("invalid metadata returned %v", err)
			}
		})
	}
}
