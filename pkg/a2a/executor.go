package a2a

import (
	"context"
	"fmt"
	"iter"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// Runtime is satisfied by *agent.Anra. The peer owns its runtime and tape;
// incoming A2A IDs never select a local owner.
type Runtime interface {
	Run(context.Context, string) (string, error)
}

// NewExecutor bridges text-only A2A messages to Anra's tape-backed runtime and
// emits a direct Message. It does not synthesize a task for context borrowing.
func NewExecutor(runtime Runtime) (a2asrv.AgentExecutor, error) {
	if runtime == nil {
		return nil, ErrInvalidParams
	}
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, exec *a2asrv.ExecutorContext) iter.Seq2[sdk.Event, error] {
		return func(yield func(sdk.Event, error) bool) {
			if exec == nil || exec.Message == nil || exec.Message.Role != sdk.MessageRoleUser || exec.Message.TaskID != "" {
				yield(nil, sdk.ErrInvalidParams)
				return
			}
			parts := make([]string, 0, len(exec.Message.Parts))
			for _, part := range exec.Message.Parts {
				if part == nil {
					yield(nil, sdk.ErrInvalidParams)
					return
				}
				text, ok := part.Content.(sdk.Text)
				if !ok {
					yield(nil, sdk.ErrInvalidParams)
					return
				}
				parts = append(parts, string(text))
			}
			input := strings.Join(parts, "\n")
			if strings.TrimSpace(input) == "" {
				yield(nil, sdk.ErrInvalidParams)
				return
			}
			output, err := runtime.Run(ctx, input)
			if err != nil {
				yield(nil, fmt.Errorf("anra execution failed: %w", err))
				return
			}
			reply := sdk.NewMessage(sdk.MessageRoleAgent, sdk.NewTextPart(output))
			reply.ContextID = exec.ContextID
			yield(reply, nil)
		}
	}), nil
}
