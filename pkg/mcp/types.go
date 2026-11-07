// Package mcp serves immutable handoff context to authenticated external runtimes.
// Tape, role definitions, and credential verification are supplied by the caller.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/go-playground/validator/v10"
)

const (
	ReadRole     = "read_role"
	ReadTapeView = "read_tape_view"
)

// Error contains only public, non-identifying diagnostics. Storage errors and
// credentials must never be embedded in it.
type Error struct {
	Code string `json:"code"`

	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// ErrorResponse is the public error envelope for HTTP and MCP tool responses.
type ErrorResponse struct {
	Error *Error `json:"error"`
}

var (
	ErrPermissionDenied = &Error{
		Code:    "permission_denied",
		Message: "unauthorized connection or missing grant for target handoff anchor",
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

// Grant is a credential verifier's immutable binding to one handoff and role
// revision. LeaseID refers to the shared view service's caller-bound lease;
// it carries no independent Tape range. Tool arguments cannot establish these
// identities. Lease termination and credential revocation are independent.
type Grant struct {
	LeaseID string `json:"leaseId" validate:"required,max=256"`

	Caller string `json:"caller" validate:"required,max=256"`

	Role RoleRef `json:"role"`

	AnchorSeq string `json:"anchorSeq" validate:"required,number,startsnotwith=0"`

	Permissions []string `json:"permissions" validate:"min=1,max=2,unique,dive,oneof=read_role read_tape_view"`

	ExpiresAt time.Time `json:"expiresAt" validate:"required"`
}

type toolInput struct {
	AnchorSeq string `json:"anchorSeq"`
}

var validate = validator.New(validator.WithRequiredStructEnabled())

func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ErrInvalidReference
	}
	return nil
}
