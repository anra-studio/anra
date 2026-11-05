// Package mcp serves immutable handoff context to authenticated external runtimes.
// Tape, role definitions, and credential verification are supplied by the caller.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/go-playground/validator/v10"
)

const (
	ReadRole     = "read_role"
	ReadTapeView = "read_tape_view"
)

// Error contains only public, non-identifying diagnostics. Storage errors and
// credentials must never be embedded in it.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ErrorResponse is the public error envelope for HTTP and MCP tool responses.
type ErrorResponse struct {
	Error *Error `json:"error"`
}

var (
	ErrPermissionDenied   = &Error{"permission_denied", "unauthorized connection or missing grant for target handoff anchor"}
	ErrInvalidReference   = &Error{"invalid_reference", "invalid handoff reference or missing role revision"}
	ErrFailedPrecondition = &Error{"failed_precondition", "committed context cannot be reconstructed within the supported projection and size limits"}
	ErrUnavailable        = &Error{"unavailable", "context service temporarily unavailable"}
)

// Grant is supplied by a trusted controller. It authorizes reading the role
// revision and the Tape view (SeqS to SeqE) that the handoff anchored at
// AnchorSeq committed. Tool arguments cannot establish any of these identities.
// ID and Caller are public issuer-owned provenance; ID is not the bearer secret.
// View authorization includes stored thought content, but not private execution
// metadata or provider thought signatures.
type Grant struct {
	ID          string  `json:"id" validate:"required,max=256"`
	Caller      string  `json:"caller" validate:"required,max=256"`
	Role        RoleRef `json:"role"`
	AnchorSeq   string  `json:"anchorSeq" validate:"required,number,startsnotwith=0"`
	View        *pb.TapeView
	Permissions []string  `json:"permissions" validate:"min=1,max=2,unique,dive,oneof=read_role read_tape_view"`
	ExpiresAt   time.Time `json:"expiresAt" validate:"required"`
}

type toolInput struct {
	AnchorSeq string `json:"anchorSeq" validate:"required,number,startsnotwith=0"`
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
