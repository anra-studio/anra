// Package mcp serves immutable handoff context to authenticated external runtimes.
// Tape, role definitions, and credential verification are supplied by the caller.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/go-playground/validator/v10"
)

const (
	ReadRole     = "read_role"
	ReadTapeView = "read_tape_view"
)

// ReadTarget identifies the caller-bound lease and immutable role revision
// selected by the host for an authorized tool call. It carries no authorization
// policy or lifetime; the shared borrowing flow owns the lease lifecycle.
type ReadTarget struct {
	LeaseID string `json:"leaseId" validate:"required,max=256"`

	Caller string `json:"caller" validate:"required,max=256"`

	Role RoleRef `json:"role"`
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
