// Package view defines public Anra view content, provenance and codecs.
// Approval and request state belong to flow/borrow; lease lifetimes to flow/grant.
package view

import (
	"encoding/json"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// Quote carries attributed content in a collaboration channel.
// Metadata in a Quote does not grant permission to read or execute anything.
type Quote struct {
	Channel    Channel    `json:"channel"`
	Provenance Provenance `json:"provenance"`
	Entries    []Entry    `json:"entries"`
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
		Entries: make([]Entry, 0, len(q.Entries)),
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
