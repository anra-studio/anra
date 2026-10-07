// Package a2a connects Anra peers through standard A2A and the optional
// permissioned tape-view method extension. Local tape ownership never comes
// from request parameters.
package a2a

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

const ExtensionURI = "urn:anra:a2a:tape-view:v1"
const ExtensionHeader = "A2A-Extensions"
const TextMediaType = "text/plain"
const MaxPageSize = 100
const MaxBodyBytes = 1 << 20
const MaxEntryBytes = 8 << 10

var (
	ErrInvalidParams = errors.New("invalid parameters")
	ErrConflict      = errors.New("request ID conflict")
	ErrUnavailable   = errors.New("view unavailable")
	ErrStorage       = errors.New("tape storage failure")
	ErrInactive      = errors.New("tape-view extension inactive")
	ErrUnsupported   = errors.New("unsupported entry representation")
)

type Source struct {
	AgentID string `json:"agentId" validate:"required"`
	TapeID  string `json:"tapeId" validate:"required"`
}

func validRange(r tv.EntryRange, head te.Seq) bool {
	return !r.SeqS.IsZero() && r.SeqS.Cmp(r.SeqE) < 0 && r.SeqE.Cmp(head.Next()) <= 0
}

type View struct {
	ID           string          `json:"viewId"`
	Source       Source          `json:"source"`
	SnapshotHead te.Seq          `json:"snapshotHead"`
	Ranges       []tv.EntryRange `json:"ranges"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}
type BorrowRequest struct {
	RequestID string         `json:"requestId" validate:"required,maxbytes=256"`
	Purpose   string         `json:"purpose" validate:"required,maxbytes=8192"`
	Query     string         `json:"query" validate:"required,maxbytes=8192"`
	Bounds    *tv.EntryRange `json:"bounds,omitempty" validate:"omitempty"`
}

func (r BorrowRequest) validate() error {
	return validateStruct(r)
}

type Status string

const (
	Granted Status = "granted"
	Denied  Status = "denied"
	Pending Status = "pending"
)

type Decision struct {
	RequestID string `json:"requestId"`
	Status    Status `json:"status"`
	View      *View  `json:"view,omitempty"`
}
type Payload struct {
	MediaType string          `json:"mediaType"`
	Data      json.RawMessage `json:"data"`
}

// ExportedEntry adds transport metadata to tape-go's public entry projection.
// It contains no owner identity or CustomEntry extensions.
type ExportedEntry struct {
	tv.ProjectedEntry
	Timestamp time.Time `json:"timestamp"`
	Payload   *Payload  `json:"payload,omitempty"`
}
type ReadRequest struct {
	ViewID   string `json:"viewId" validate:"required"`
	Cursor   string `json:"cursor,omitempty"`
	PageSize int    `json:"pageSize,omitempty" validate:"gte=0,lte=100"`
}

func (r ReadRequest) validate() error { return validateStruct(r) }

type Page struct {
	ViewID       string          `json:"viewId"`
	Source       Source          `json:"source"`
	SnapshotHead te.Seq          `json:"snapshotHead"`
	Entries      []ExportedEntry `json:"entries"`
	NextCursor   string          `json:"nextCursor,omitempty"`
}

func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalidParams
	}
	return nil
}
