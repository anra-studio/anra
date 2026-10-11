package a2a

import (
	"encoding/json"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

// These private DTOs preserve the journal and fingerprint JSON formats. RPCs
// and service operations use generated protobuf messages directly.
type storedSource struct {
	AgentID string `json:"agentId" validate:"required"`
	TapeID  string `json:"tapeId" validate:"required"`
}

type storedView struct {
	ID           string          `json:"viewId"`
	Source       storedSource    `json:"source"`
	SnapshotHead te.Seq          `json:"snapshotHead"`
	Ranges       []tv.EntryRange `json:"ranges"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}
type storedRequest struct {
	RequestID string         `json:"requestId"`
	Purpose   string         `json:"purpose"`
	Query     string         `json:"query"`
	Bounds    *tv.EntryRange `json:"bounds,omitempty"`
}

type storedStatus string

type storedDecision struct {
	RequestID string       `json:"requestId"`
	Status    storedStatus `json:"status"`
	View      *storedView  `json:"view,omitempty"`
}
type storedPayload struct {
	MediaType string          `json:"mediaType"`
	Data      json.RawMessage `json:"data"`
}

// storedEntry preserves the legacy JSON representation used by fingerprints.
// It contains no owner identity or CustomEntry extensions.
type storedEntry struct {
	tv.ProjectedEntry
	Timestamp time.Time      `json:"timestamp"`
	Payload   *storedPayload `json:"payload,omitempty"`
}

func storeSource(s *pb.Source) storedSource {
	return storedSource{AgentID: s.GetAgentId(), TapeID: s.GetTapeId()}
}
func (s storedSource) message() *pb.Source { return &pb.Source{AgentId: s.AgentID, TapeId: s.TapeID} }

func storeRequest(r *pb.BorrowRequest) (storedRequest, error) {
	out := storedRequest{RequestID: r.GetRequestId(), Purpose: r.GetPurpose(), Query: r.GetQuery()}
	if r.GetBounds() != nil {
		b, err := rangeFromProto(r.Bounds)
		if err != nil {
			return out, err
		}
		out.Bounds = &b
	}
	return out, nil
}
func (r storedRequest) message() *pb.BorrowRequest {
	out := &pb.BorrowRequest{RequestId: r.RequestID, Purpose: r.Purpose, Query: r.Query}
	if r.Bounds != nil {
		out.Bounds = rangeToProto(*r.Bounds)
	}
	return out
}

func storeDecision(d *pb.Decision) (storedDecision, error) {
	out := storedDecision{RequestID: d.GetRequestId(), Status: storedStatus(map[pb.DecisionStatus]string{Granted: "granted", Denied: "denied", Pending: "pending"}[d.GetStatus()])}
	if v := d.GetView(); v != nil {
		head, err := te.ParseSeq(v.SnapshotHead)
		if err != nil {
			return out, err
		}
		if v.ExpiresAt == nil || v.ExpiresAt.CheckValid() != nil {
			return out, ErrInvalidParams
		}
		out.View = &storedView{ID: v.ViewId, Source: storeSource(v.Source), SnapshotHead: head, ExpiresAt: v.ExpiresAt.AsTime(), Ranges: []tv.EntryRange{}}
		for _, r := range v.Ranges {
			b, err := rangeFromProto(r)
			if err != nil {
				return out, err
			}
			out.View.Ranges = append(out.View.Ranges, b)
		}
	}
	return out, nil
}
func (d storedDecision) message() *pb.Decision {
	out := &pb.Decision{RequestId: d.RequestID, Status: map[storedStatus]pb.DecisionStatus{"granted": Granted, "denied": Denied, "pending": Pending}[d.Status]}
	if v := d.View; v != nil {
		out.View = &pb.View{ViewId: v.ID, Source: v.Source.message(), SnapshotHead: v.SnapshotHead.String(), ExpiresAt: timestamppb.New(v.ExpiresAt)}
		for _, r := range v.Ranges {
			out.View.Ranges = append(out.View.Ranges, rangeToProto(r))
		}
	}
	return out
}

func storeEntry(e *pb.ExportedEntry) (storedEntry, error) {
	seq, err := te.ParseSeq(e.GetSeq())
	if err != nil {
		return storedEntry{}, err
	}
	if e.GetTimestamp() == nil || e.Timestamp.CheckValid() != nil {
		return storedEntry{}, ErrInvalidParams
	}
	out := storedEntry{ProjectedEntry: tv.ProjectedEntry{Seq: seq, Kind: te.EntryKind(e.Kind), Summary: e.Summary}, Timestamp: e.Timestamp.AsTime()}
	if e.Payload != nil {
		out.Payload = &storedPayload{MediaType: e.Payload.MediaType, Data: e.Payload.JsonData}
	}
	return out, nil
}

type storedGrant struct {
	Schema    string         `json:"schema"`
	Source    storedSource   `json:"source"`
	Requester string         `json:"requester"`
	Request   storedRequest  `json:"request"`
	Decision  storedDecision `json:"decision"`
	Session   string         `json:"session,omitempty"`
	HeadHash  string         `json:"headHash,omitempty"`
	EntryHash string         `json:"entryHash,omitempty"`
	Codec     string         `json:"codec,omitempty"`
	Closed    bool           `json:"closed,omitempty"`
}

func (r grantRecord) MarshalJSON() ([]byte, error) {
	request, err := storeRequest(r.Request)
	if err != nil {
		return nil, err
	}
	decision, err := storeDecision(r.Decision)
	if err != nil {
		return nil, err
	}
	return json.Marshal(storedGrant{r.Schema, storeSource(r.Source), r.Requester, request, decision, r.Session, r.HeadHash, r.EntryHash, r.Codec, r.Closed})
}
func (r *grantRecord) UnmarshalJSON(data []byte) error {
	var stored storedGrant
	if err := decode(data, &stored); err != nil {
		return err
	}
	*r = grantRecord{Schema: stored.Schema, Source: stored.Source.message(), Requester: stored.Requester, Request: stored.Request.message(), Decision: stored.Decision.message(), Session: stored.Session, HeadHash: stored.HeadHash, EntryHash: stored.EntryHash, Codec: stored.Codec, Closed: stored.Closed}
	return nil
}
