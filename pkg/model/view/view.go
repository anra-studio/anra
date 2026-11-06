// Package view owns committed-view borrowing and public quoted content.
package view

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrInvalidParams = errors.New("invalid parameters")
	ErrConflict      = errors.New("request ID conflict")
	ErrUnavailable   = errors.New("view unavailable")
	ErrStorage       = errors.New("tape storage failure")
	ErrUnsupported   = errors.New("unsupported entry representation")
)

const (
	Granted     = pb.DecisionStatus_DECISION_STATUS_GRANTED
	Denied      = pb.DecisionStatus_DECISION_STATUS_DENIED
	Pending     = pb.DecisionStatus_DECISION_STATUS_PENDING
	MaxPageSize = 100
)

type Channel string

const (
	Primary     Channel = "primary"
	Observation Channel = "observation"
	Borrowed    Channel = "borrowed"
)

// Provenance is issuer-owned, public attribution, never a credential.
type Provenance struct {
	Caller  string  `json:"caller"`
	LeaseID string  `json:"leaseId"`
	Ref     *pb.Ref `json:"ref"`
}

func (p Provenance) MarshalJSON() ([]byte, error) {
	return protojson.Marshal(
		&pb.Provenance{
			Caller:  p.Caller,
			LeaseId: p.LeaseID,
			Ref:     p.Ref,
		},
	)
}

func (p *Provenance) UnmarshalJSON(data []byte) error {
	var value pb.Provenance
	if err := protojson.Unmarshal(data, &value); err != nil {
		return err
	}
	p.Caller = value.Caller
	p.LeaseID = value.LeaseId
	p.Ref = value.Ref
	return nil
}

// Entry contains public content only; JSON number lexemes remain opaque.
type Entry struct {
	Seq       string          `json:"seq"`
	Kind      string          `json:"kind"`
	Timestamp time.Time       `json:"timestamp"`
	MediaType string          `json:"mediaType"`
	Content   json.RawMessage `json:"content"`
}

// Quote is content read under one lease, together with its source attribution.
// Metadata in a Quote does not grant permission to read or execute anything.
type Quote struct {
	Channel    Channel    `json:"channel"`
	Provenance Provenance `json:"provenance"`
	Entries    []Entry    `json:"entries"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

// Proto encodes public payloads without decoding or changing their JSON.
func (q *Quote) Proto() *pb.Quote {
	out := &pb.Quote{
		Channel: string(q.Channel),
		Provenance: &pb.Provenance{
			Caller:  q.Provenance.Caller,
			LeaseId: q.Provenance.LeaseID,
			Ref:     q.Provenance.Ref,
		},
		NextCursor: q.NextCursor,
	}
	for _, e := range q.Entries {
		out.Entries = append(
			out.Entries,
			&pb.ExportedEntry{
				Seq:       e.Seq,
				Kind:      e.Kind,
				Timestamp: timestamppb.New(e.Timestamp),
				Payload: &pb.Payload{
					MediaType: e.MediaType,
					JsonData:  e.Content,
				},
			},
		)
	}
	return out
}

// FromProto consumes a schema-validated protocol Quote. Metadata and payloads
// are borrowed read-only; this conversion does not repeat protocol validation.
func FromProto(q *pb.Quote) *Quote {
	out := &Quote{
		Channel: Channel(q.Channel),
		Provenance: Provenance{
			Caller:  q.Provenance.Caller,
			LeaseID: q.Provenance.LeaseId,
			Ref:     q.Provenance.Ref,
		},
		Entries:    make([]Entry, 0, len(q.Entries)),
		NextCursor: q.NextCursor,
	}
	for _, e := range q.Entries {
		out.Entries = append(
			out.Entries,
			Entry{
				Seq:       e.Seq,
				Kind:      e.Kind,
				Timestamp: e.Timestamp.AsTime(),
				MediaType: e.Payload.MediaType,
				Content:   json.RawMessage(e.Payload.JsonData),
			},
		)
	}
	return out
}

// TapeReader borrows an initialized Tape. The host owns writes and lifetime.
type (
	TapeReader interface {
		Range(context.Context, tv.EntryRange, ...storage.RangeBy) (tv.EntryView, error)
	}
	// Views returns an exact, committed Ref, or nil when no view is eligible.
	// Providers own commitment and canonical scope ordering; results stay immutable.
	Views interface {
		View(context.Context, string, *pb.Ref) (*pb.Ref, error)
	}
)
