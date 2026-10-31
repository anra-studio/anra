// Package mcp serves immutable handoff context to authenticated external runtimes.
// Its control-plane methods are local Go APIs, never remotely callable MCP tools.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/go-playground/validator/v10"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/encoding/protojson"
)

const HandoffSchema = "anra.dev/anchor/intr/v1"

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
type RoleDefinition struct {
	RoleRef
	Definition json.RawMessage `json:"definition" validate:"required,jsonobject"`
}

type Target struct {
	Name      string `json:"name" validate:"required,max=256"`
	BindingID string `json:"bindingId" validate:"required,max=256"`
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

type Handoff struct {
	Schema  string    `json:"schema" validate:"eq=anra.dev/anchor/intr/v1"`
	Type    string    `json:"type" validate:"eq=integration.handoff"`
	Role    RoleRef   `json:"role"`
	Target  Target    `json:"target"`
	Task    string    `json:"task" validate:"required,max=8192"`
	Context WindowRef `json:"context"`
	Origin  *Origin   `json:"origin,omitempty" validate:"omitempty"`
}

// Grant is supplied by a trusted controller. Every dimension must exactly match
// the committed handoff; neither tool arguments nor caller-supplied session IDs
// can establish any of these identities.
type Grant struct {
	RoleID      string    `json:"roleId" validate:"required,max=256"`
	BindingID   string    `json:"bindingId" validate:"required,max=256"`
	AnchorSeq   string    `json:"anchorSeq" validate:"required,seq"`
	Window      WindowRef `json:"window"`
	Permissions []string  `json:"permissions" validate:"min=1,max=2,unique,dive,oneof=read_role read_context_window"`
	ExpiresAt   time.Time `json:"expiresAt" validate:"required"`
}

type toolInput struct {
	AnchorSeq string `json:"anchorSeq" validate:"required,seq"`
}

var validate = func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	if err := v.RegisterValidation("seq", func(f validator.FieldLevel) bool {
		s, err := te.ParseSeq(f.Field().String())
		return err == nil && !s.IsZero()
	}); err != nil {
		panic(err)
	}
	if err := v.RegisterValidation("jsonobject", func(f validator.FieldLevel) bool {
		var object map[string]json.RawMessage
		return json.Unmarshal(f.Field().Bytes(), &object) == nil && object != nil
	}); err != nil {
		panic(err)
	}
	return v
}()

func validateWindow(w WindowRef) error {
	if validate.Struct(w) != nil || protovalidate.Validate(w.View) != nil {
		return ErrInvalidReference
	}
	return nil
}

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
