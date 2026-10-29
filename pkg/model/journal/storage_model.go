package journal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// decode reads one record, rejecting unknown fields so an older or newer record
// shape cannot be read as a partly valid one. bolthold also decodes record keys
// with it, which are plain scalars.
func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

// Only tape-go's numeric ranges need conversion at the record boundary.
func rangeToProto(r tv.EntryRange) *pb.EntryRange {
	return &pb.EntryRange{SeqS: r.SeqS.String(), SeqE: r.SeqE.String()}
}
func rangeFromProto(r *pb.EntryRange) (tv.EntryRange, error) {
	if r == nil {
		return tv.EntryRange{}, ErrStorage
	}
	s, err := te.ParseSeq(r.SeqS)
	if err != nil {
		return tv.EntryRange{}, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	e, err := te.ParseSeq(r.SeqE)
	if err != nil {
		return tv.EntryRange{}, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	return tv.EntryRange{SeqS: s, SeqE: e}, nil
}

// The records below are the journal's storage format. bolthold names a bucket
// after each type and fills the boltholdKey field from the record's key on read;
// requestState also indexes its view ID.
type (
	// sourceBinding records the source tape this file belongs to and counts the
	// records appended to the audit trail.
	sourceBinding struct {
		Source storedSource `json:"source"`
		Audit  uint64       `json:"audit"`
	}

	// requestState is a request's current outcome: pending, denied or granted.
	// A granted record is never rewritten, and its view ID is the indexed field.
	requestState struct {
		Key       string        `json:"-" boltholdKey:"Key"`
		RequestID string        `json:"requestId"`
		Requester string        `json:"requester"`
		Kind      string        `json:"kind"`
		Request   storedRequest `json:"request"`
		Status    storedStatus  `json:"status,omitempty"`
		ViewID    string        `json:"viewId,omitempty" boltholdIndex:"ViewID"`
		View      *storedView   `json:"view,omitempty"`
	}

	// grantRelease marks a granted view as released; the grant is not rewritten.
	grantRelease struct {
		ViewID    string `json:"viewId" boltholdKey:"ViewID"`
		Requester string `json:"requester"`
	}

	// auditEntry is one append-only audit record, keyed by its own sequence.
	auditEntry struct {
		Seq  uint64          `json:"seq"`
		Kind string          `json:"kind"`
		Body json.RawMessage `json:"body"`
	}
)

type storedSource struct {
	AgentID string `json:"agentId"`
	TapeID  string `json:"tapeId"`
}

// storedView is a view without its ID, which lives on the request state and in
// the view index.
type storedView struct {
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
	View      *pb.TapeView   `json:"view,omitempty"`
}

type storedStatus string

const (
	pendingStatus storedStatus = "pending"
	deniedStatus  storedStatus = "denied"
)

func storeSource(s *pb.Source) storedSource {
	return storedSource{AgentID: s.GetAgentId(), TapeID: s.GetTapeId()}
}
func (s storedSource) message() *pb.Source { return &pb.Source{AgentId: s.AgentID, TapeId: s.TapeID} }

func storeRequest(r *pb.BorrowRequest) (storedRequest, error) {
	out := storedRequest{RequestID: r.GetRequestId(), Purpose: r.GetPurpose(), Query: r.GetQuery(), View: anchor.CloneRef(r.View)}
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
	out := &pb.BorrowRequest{RequestId: r.RequestID, Purpose: r.Purpose, Query: r.Query, View: anchor.CloneRef(r.View)}
	if r.Bounds != nil {
		out.Bounds = rangeToProto(*r.Bounds)
	}
	return out
}

func storeView(v *pb.View) (storedView, error) {
	head, err := te.ParseSeq(v.GetSnapshotHead())
	if err != nil {
		return storedView{}, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	if v.GetExpiresAt() == nil || v.ExpiresAt.CheckValid() != nil {
		return storedView{}, ErrStorage
	}
	out := storedView{Source: storeSource(v.Source), SnapshotHead: head, ExpiresAt: v.ExpiresAt.AsTime(), Ranges: []tv.EntryRange{}}
	for _, r := range v.Ranges {
		b, err := rangeFromProto(r)
		if err != nil {
			return storedView{}, err
		}
		out.Ranges = append(out.Ranges, b)
	}
	return out, nil
}
func (v storedView) message(viewID string) *pb.View {
	out := &pb.View{ViewId: viewID, Source: v.Source.message(), SnapshotHead: v.SnapshotHead.String(), ExpiresAt: timestamppb.New(v.ExpiresAt)}
	for _, r := range v.Ranges {
		out.Ranges = append(out.Ranges, rangeToProto(r))
	}
	return out
}

// Only requests that have no grant are stored with a status; a grant is
// recorded by its view.
func storeStatus(s pb.DecisionStatus) (storedStatus, bool) {
	switch s {
	case pb.DecisionStatus_DECISION_STATUS_PENDING:
		return pendingStatus, true
	case pb.DecisionStatus_DECISION_STATUS_DENIED:
		return deniedStatus, true
	}
	return "", false
}
func (s storedStatus) status() (pb.DecisionStatus, bool) {
	switch s {
	case pendingStatus:
		return pb.DecisionStatus_DECISION_STATUS_PENDING, true
	case deniedStatus:
		return pb.DecisionStatus_DECISION_STATUS_DENIED, true
	}
	return pb.DecisionStatus_DECISION_STATUS_UNSPECIFIED, false
}
