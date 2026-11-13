package mcp

import (
	"errors"
	"fmt"

	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/model/view"
)

// Error exposes only public, non-identifying diagnostics. Its underlying cause
// is available through Unwrap but is not included in its message or JSON.
type Error struct {
	Code string `json:"code"`

	Message string `json:"message"`

	cause error
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func (e *Error) Unwrap() error {
	return e.cause
}

// ErrorResponse is the public error envelope for HTTP and MCP tool responses.
type ErrorResponse struct {
	Error *Error `json:"error"`
}

var (
	ErrPermissionDenied = &Error{
		Code:    "permission_denied",
		Message: "unauthorized connection or access denied for target handoff anchor",
	}
	ErrInvalidReference = &Error{
		Code:    "invalid_reference",
		Message: "invalid handoff reference or missing role revision",
	}
	ErrFailedPrecondition = &Error{
		Code:    "failed_precondition",
		Message: "committed context cannot be encoded",
	}
	ErrUnavailable = &Error{
		Code:    "unavailable",
		Message: "context service temporarily unavailable",
	}
)

func wrapError(public *Error, cause error) *Error {
	return &Error{
		Code:    public.Code,
		Message: public.Message,
		cause:   fmt.Errorf("%w: %w", public, cause),
	}
}

func toViewError(err error) *Error {
	if errors.Is(err, borrow.ErrUnavailable) {
		return wrapError(ErrPermissionDenied, err)
	}
	if errors.Is(err, view.ErrUnsupported) {
		return wrapError(ErrFailedPrecondition, err)
	}
	return wrapError(ErrUnavailable, err)
}
