package a2a

import (
	"encoding/json"
	"fmt"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func rangeToProto(r tv.EntryRange) *pb.EntryRange {
	return &pb.EntryRange{SeqS: r.SeqS.String(), SeqE: r.SeqE.String()}
}

func rangeFromProto(r *pb.EntryRange) (tv.EntryRange, error) {
	if r == nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	s, err := te.ParseSeq(r.SeqS)
	if err != nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	e, err := te.ParseSeq(r.SeqE)
	if err != nil {
		return tv.EntryRange{}, ErrInvalidParams
	}
	return tv.EntryRange{SeqS: s, SeqE: e}, nil
}

func decisionToProto(d Decision) *pb.Decision {
	status := map[Status]pb.DecisionStatus{Granted: pb.DecisionStatus_DECISION_STATUS_GRANTED, Denied: pb.DecisionStatus_DECISION_STATUS_DENIED, Pending: pb.DecisionStatus_DECISION_STATUS_PENDING}
	r := &pb.Decision{RequestId: d.RequestID, Status: status[d.Status]}
	if v := d.View; v != nil {
		r.View = &pb.View{ViewId: v.ID, Source: &pb.Source{AgentId: v.Source.AgentID, TapeId: v.Source.TapeID}, SnapshotHead: v.SnapshotHead.String(), ExpiresAt: timestamppb.New(v.ExpiresAt)}
		for _, bounds := range v.Ranges {
			r.View.Ranges = append(r.View.Ranges, rangeToProto(bounds))
		}
	}
	return r
}

func decisionFromProto(d *pb.Decision, id string) (Decision, error) {
	if d == nil {
		return Decision{}, ErrInvalidParams
	}
	status := map[pb.DecisionStatus]Status{pb.DecisionStatus_DECISION_STATUS_GRANTED: Granted, pb.DecisionStatus_DECISION_STATUS_DENIED: Denied, pb.DecisionStatus_DECISION_STATUS_PENDING: Pending}
	r := Decision{RequestID: d.RequestId, Status: status[d.Status]}
	if v := d.View; v != nil {
		if v.Source == nil || v.ExpiresAt == nil || v.ExpiresAt.CheckValid() != nil {
			return Decision{}, ErrInvalidParams
		}
		head, err := te.ParseSeq(v.SnapshotHead)
		if err != nil {
			return Decision{}, ErrInvalidParams
		}
		r.View = &View{ID: v.ViewId, Source: Source{AgentID: v.Source.AgentId, TapeID: v.Source.TapeId}, SnapshotHead: head, ExpiresAt: v.ExpiresAt.AsTime()}
		for _, bounds := range v.Ranges {
			b, err := rangeFromProto(bounds)
			if err != nil {
				return Decision{}, err
			}
			r.View.Ranges = append(r.View.Ranges, b)
		}
	}
	return r, validateDecision(r, id)
}

func pageToProto(p Page) *pb.Page {
	r := &pb.Page{ViewId: p.ViewID, Source: &pb.Source{AgentId: p.Source.AgentID, TapeId: p.Source.TapeID}, SnapshotHead: p.SnapshotHead.String(), NextCursor: p.NextCursor}
	for _, e := range p.Entries {
		entry := &pb.ExportedEntry{Seq: e.Seq.String(), Kind: string(e.Kind), Summary: e.Summary, Timestamp: timestamppb.New(e.Timestamp)}
		if e.Payload != nil {
			entry.Payload = &pb.Payload{MediaType: e.Payload.MediaType, JsonData: e.Payload.Data}
		}
		r.Entries = append(r.Entries, entry)
	}
	return r
}

func pageFromProto(p *pb.Page) (Page, error) {
	if p == nil || p.Source == nil {
		return Page{}, ErrInvalidParams
	}
	head, err := te.ParseSeq(p.SnapshotHead)
	if err != nil {
		return Page{}, ErrInvalidParams
	}
	r := Page{ViewID: p.ViewId, Source: Source{AgentID: p.Source.AgentId, TapeID: p.Source.TapeId}, SnapshotHead: head, NextCursor: p.NextCursor, Entries: make([]ExportedEntry, 0, len(p.Entries))}
	for _, e := range p.Entries {
		if e == nil || e.Timestamp == nil || e.Timestamp.CheckValid() != nil {
			return Page{}, ErrInvalidParams
		}
		seq, err := te.ParseSeq(e.Seq)
		if err != nil {
			return Page{}, ErrInvalidParams
		}
		entry := ExportedEntry{ProjectedEntry: tv.ProjectedEntry{Seq: seq, Kind: te.EntryKind(e.Kind), Summary: e.Summary}, Timestamp: e.Timestamp.AsTime()}
		if e.Payload != nil {
			if e.Payload.MediaType == "" || !json.Valid(e.Payload.JsonData) {
				return Page{}, fmt.Errorf("%w: invalid payload", ErrInvalidParams)
			}
			entry.Payload = &Payload{MediaType: e.Payload.MediaType, Data: json.RawMessage(e.Payload.JsonData)}
		}
		r.Entries = append(r.Entries, entry)
	}
	return r, nil
}
