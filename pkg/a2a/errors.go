package a2a

import (
	"context"
	"errors"
	"fmt"

	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/model/view"

	"connectrpc.com/connect/v2"
)

// Errors returned by this package. Use errors.Is to test for them.
var (
	ErrInvalidParams = borrow.ErrInvalidParams
	ErrConflict      = borrow.ErrConflict
	ErrUnavailable   = borrow.ErrUnavailable
	ErrStorage       = borrow.ErrStorage
	ErrInactive      = errors.New("tape-view extension inactive")
	ErrUnsupported   = view.ErrUnsupported
)

// Preserve public error categories without exposing storage or policy details.
func connectError(err error) error {
	for _, mapping := range connectErrors {
		if errors.Is(err, mapping.err) {
			return connect.NewError(mapping.code, mapping.err.Error()).WithCause(err)
		}
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, "canceled").WithCause(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, "deadline exceeded").WithCause(err)
	}
	return connect.NewError(connect.CodeInternal, "internal error").WithCause(err)
}

var connectErrors = []struct {
	err error

	code connect.Code
}{
	{
		err:  ErrInvalidParams,
		code: connect.CodeInvalidArgument,
	},
	{
		err:  ErrInactive,
		code: connect.CodeFailedPrecondition,
	},
	{
		err:  ErrConflict,
		code: connect.CodeAlreadyExists,
	},
	{
		err:  ErrUnavailable,
		code: connect.CodeNotFound,
	},
	{
		err:  ErrStorage,
		code: connect.CodeInternal,
	},
	{
		err:  ErrUnsupported,
		code: connect.CodeUnimplemented,
	},
}

func fromConnectError(err error) error {
	var rpc *connect.Error
	if errors.As(err, &rpc) {
		if rpc.Code() == connect.CodeInvalidArgument {
			return fmt.Errorf("%w: %w", ErrInvalidParams, err)
		}
		for _, mapping := range connectErrors {
			if rpc.Code() == mapping.code && rpc.Message() == mapping.err.Error() {
				return fmt.Errorf("%w: %w", mapping.err, err)
			}
		}
	}
	return err
}
