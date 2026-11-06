package grantRecord

import (
	"bytes"
	"encoding/json"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

type sourceBinding struct {
	Source storedSource
	Audit  uint64
}
type requestState struct {
	Key       string `json:"-" boltholdKey:"Key"`
	RequestID string
	Requester string
	Kind      string
	Request   storedRequest
	Status    storedStatus
	LeaseID   string `boltholdIndex:"LeaseID"`
	Lease     *storedLease
}
type grantRelease struct {
	LeaseID   string `boltholdKey:"LeaseID"`
	Requester string
}
type auditEntry struct {
	Seq  uint64
	Kind string
	Body json.RawMessage
}
type storedSource struct {
	AgentID string
	TapeID  string
}

func storeSource(s *pb.Source) storedSource {
	return storedSource{
		AgentID: s.AgentId,
		TapeID:  s.TapeId,
	}
}

func (s storedSource) message() *pb.Source {
	return &pb.Source{
		AgentId: s.AgentID,
		TapeId:  s.TapeID,
	}
}

type storedLease struct {
	Caller    string
	Ref       *pb.Ref
	ExpiresAt time.Time
}
type storedRequest struct {
	Value *pb.BorrowRequest
}

func storeRequest(r *pb.BorrowRequest) (storedRequest, error) {
	return storedRequest{
		Value: proto.Clone(r).(*pb.BorrowRequest),
	}, nil
}

func (r storedRequest) message() *pb.BorrowRequest {
	return proto.Clone(r.Value).(*pb.BorrowRequest)
}

func storeLease(v *pb.Lease) (storedLease, error) {
	return storedLease{
		Caller:    v.Caller,
		Ref:       proto.Clone(v.Ref).(*pb.Ref),
		ExpiresAt: v.ExpiresAt.AsTime(),
	}, nil
}

func (v storedLease) message(id string) *pb.Lease {
	return &pb.Lease{
		Id:        id,
		Caller:    v.Caller,
		Ref:       proto.Clone(v.Ref).(*pb.Ref),
		ExpiresAt: timestamppb.New(v.ExpiresAt),
	}
}

type storedStatus string

func storeStatus(s pb.DecisionStatus) (storedStatus, bool) {
	switch s {
	case pb.DecisionStatus_DECISION_STATUS_PENDING:
		return "pending", true
	case pb.DecisionStatus_DECISION_STATUS_DENIED:
		return "denied", true
	}
	return "", false
}

func (s storedStatus) status() (pb.DecisionStatus, bool) {
	switch s {
	case "pending":
		return pb.DecisionStatus_DECISION_STATUS_PENDING, true
	case "denied":
		return pb.DecisionStatus_DECISION_STATUS_DENIED, true
	}
	return pb.DecisionStatus_DECISION_STATUS_UNSPECIFIED, false
}
