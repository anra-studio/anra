package a2a

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/model/delegator"
)

// Runtime is satisfied by *agent.Anra. The peer owns its runtime and tape;
// incoming A2A IDs never select a local owner.
type Runtime interface {
	Run(context.Context, string) (string, error)
}

// BusyError is the error a Runtime returns when it declines a request because
// another Agent Turn is active. The executor reports it as a BUSY outcome rather
// than a failure. BusyView is the latest committed TapeView the caller
// may ask to borrow, or nil.
type BusyError interface {
	error
	BusyView() *pb.TapeView
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
			input, err := requestText(exec.Message)
			if err != nil {
				yield(nil, err)
				return
			}
			ctx, collaborating, err := requestDelegation(ctx, exec.Message)
			if err != nil {
				yield(nil, err)
				return
			}
			result, output, err := runLocal(ctx, runtime, input)
			if err != nil {
				yield(nil, fmt.Errorf("anra execution failed: %w", err))
				return
			}
			reply := sdk.NewMessage(sdk.MessageRoleAgent, sdk.NewTextPart(output))
			reply.ContextID = exec.ContextID
			if !collaborating {
				// A generic caller activated no extension, so it receives text only.
				yield(reply, nil)
				return
			}
			meta, err := encodeCollaborationMetadata(result)
			if err != nil {
				yield(nil, err)
				return
			}
			reply.Extensions = []string{CollaborationURI}
			reply.Metadata = map[string]any{CollaborationURI: meta}
			yield(reply, nil)
		}
	}), nil
}

// requestText joins the message's text parts; any other part is invalid.
func requestText(m *sdk.Message) (string, error) {
	parts := make([]string, 0, len(m.Parts))
	for _, part := range m.Parts {
		if part == nil {
			return "", sdk.ErrInvalidParams
		}
		text, ok := part.Content.(sdk.Text)
		if !ok {
			return "", sdk.ErrInvalidParams
		}
		parts = append(parts, string(text))
	}
	input := strings.Join(parts, "\n")
	if strings.TrimSpace(input) == "" {
		return "", sdk.ErrInvalidParams
	}
	return input, nil
}

// requestDelegation carries the caller's delegation metadata into ctx and
// reports whether the caller activated the collaboration extension. A generic
// caller leaves ctx unchanged.
func requestDelegation(ctx context.Context, m *sdk.Message) (context.Context, bool, error) {
	if !collaborationEnabled(m) {
		return ctx, false, nil
	}
	d := new(cp.DelegationMetadata)
	if err := decodeCollaborationMetadata(m.Metadata[CollaborationURI], d); err != nil {
		return ctx, false, sdk.ErrInvalidParams
	}
	ctx, err := delegator.Accept(ctx, d)
	if err != nil {
		return ctx, false, sdk.ErrInvalidParams
	}
	return ctx, true, nil
}

// runLocal runs the request and describes the outcome. A busy agent is not an
// error: it answers with text saying the request was not accepted and a
// view hint, which the caller decides whether to expose.
func runLocal(ctx context.Context, runtime Runtime, input string) (*cp.ResponseMetadata, string, error) {
	output, err := runtime.Run(ctx, input)
	var busy BusyError
	if errors.As(err, &busy) {
		return &cp.ResponseMetadata{
			Status: cp.Status_STATUS_BUSY,
			Retry:  cp.RetryHint_RETRY_HINT_AFTER_NEW_VIEW,
			View:   busy.BusyView(),
		}, "Agent is busy; request was not accepted.", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &cp.ResponseMetadata{Status: cp.Status_STATUS_COMPLETED}, output, nil
}
