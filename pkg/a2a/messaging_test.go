package a2a

import (
	"errors"
	"testing"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
)

func TestDecodeCompletedResponseRequiresAnswerAndNoRetry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		text  string
		retry bool
	}{
		{
			name:  "completed response carrying retry advice",
			text:  "completed answer",
			retry: true,
		},
		{
			name: "completed response without text",
			text: " ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := map[string]any{"status": "STATUS_COMPLETED"}
			if tc.retry {
				metadata["retry"] = "RETRY_HINT_AFTER_NEW_VIEW"
			}
			message := sdk.NewMessage(sdk.MessageRoleAgent, sdk.NewTextPart(tc.text))
			message.Extensions = []string{CollaborationURI}
			message.Metadata = map[string]any{CollaborationURI: metadata}
			if result, err := decodeMessageResponse(message, true); !errors.Is(err, ErrInvalidParams) || result.Answer != "" {
				t.Fatalf("completed response decoded as %v, %v", result, err)
			}
		})
	}
}
