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
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/go-playground/validator/v10"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	ReadRole          = "read_role"
	ReadContextWindow = "read_context_window"
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

type RoleRef struct {
	ID      string `json:"id" validate:"required,max=256"`
	Version string `json:"version" validate:"required,max=256"`
}

// RoleDefinition is the complete, reproducible definition, not a live lookup by
// display name. The caller chooses a version and cannot overwrite that version.
// The provider must supply a valid JSON object; the tags check only presence
// and JSON syntax, not the top-level JSON type.
type RoleDefinition struct {
	RoleRef
	Definition json.RawMessage `json:"definition" validate:"required,json"`
}

type Origin struct {
	Name      string `json:"name" validate:"required,max=256"`
	BindingID string `json:"bindingId,omitempty" validate:"max=256"`
	SessionID string `json:"sessionId,omitempty" validate:"max=256"`
	EventID   string `json:"eventId,omitempty" validate:"max=256"`
}

// WindowRef uses the existing protobuf TapeView constraints. Its scope is
// half-open and its excluded head is a persisted boundary entry.
type WindowRef struct {
	View *pb.TapeView `json:"view" validate:"required,structonly"`
}

func (w WindowRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		View json.RawMessage `json:"view"`
	}{anchor.ProtoJSON(w.View)})
}

func (w *WindowRef) UnmarshalJSON(data []byte) error {
	var v struct {
		View json.RawMessage `json:"view"`
	}
	if err := decode(data, &v); err != nil {
		return err
	}
	w.View = new(pb.TapeView)
	return protojson.Unmarshal(v.View, w.View)
}

// Grant is supplied by a trusted controller. It authorizes reading the role
// revision and the Tape window (SeqS to SeqE) that the handoff anchored at
// AnchorSeq committed. Tool arguments cannot establish any of these identities.
type Grant struct {
	Role        RoleRef   `json:"role"`
	AnchorSeq   string    `json:"anchorSeq" validate:"required,number,startsnotwith=0"`
	Window      WindowRef `json:"window"`
	Permissions []string  `json:"permissions" validate:"min=1,max=2,unique,dive,oneof=read_role read_context_window"`
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
