package a2a

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Views resolves permitted, committed tape windows using local data only.
// Implementations must not enter reasoning or call external services.
type Views interface {
	View(context.Context, string, *pb.TapeView) (*pb.TapeView, error)
}

type ContextExporter struct{}

const ContextMediaType = "application/vnd.anra.context-entry+json;version=1"

// Export whitelists model content, excluding execution metadata, state deltas,
// thought parts, and arbitrary custom extensions. Tool calls/results retain
// their structured content within the lender-selected completed window.
func (ContextExporter) Export(e te.EntryLike) (*pb.ExportedEntry, error) {
	switch e.GetKind() {
	case te.EntryUser, te.EntryAssistant, te.EntryToolCall, te.EntryToolResult:
	default:
		return nil, ErrUnsupported
	}
	extensions := contextExtensions(e)
	if _, legacy := extensions["anra:runner-message/v1"]; legacy {
		return nil, ErrUnsupported
	}
	value, exists := extensions["adk:event"]
	if !exists {
		return (TextExporter{}).Export(e)
	}
	payload, ok := value.(string)
	if !ok {
		return nil, ErrUnsupported
	}
	content, summary, err := projectContext(payload)
	if err != nil {
		return nil, ErrUnsupported
	}
	data, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	return &pb.ExportedEntry{Seq: e.GetID().String(), Kind: string(e.GetKind()), Summary: summary, Timestamp: timestamppb.New(e.GetTimestamp()), Payload: &pb.Payload{MediaType: ContextMediaType, JsonData: data}}, nil
}
func AdvertiseViews(card *sdk.AgentCard, endpoint string) error {
	if err := Advertise(card, endpoint); err != nil {
		return err
	}
	for i := range card.Capabilities.Extensions {
		ext := &card.Capabilities.Extensions[i]
		if ext.URI == ExtensionURI {
			ext.Params["viewBorrowVersion"] = 1
			ext.Params["entryMediaTypes"] = []string{TextMediaType, ContextMediaType}
		}
	}
	return nil
}
func (s *Service) viewSelection(ctx context.Context, p string, req *pb.BorrowRequest) (Selection, te.Seq, error) {
	denied := Selection{Status: Denied}
	if s.config.Views == nil {
		return denied, te.Seq{}, nil
	}
	ref, err := s.config.Views.View(ctx, p, req.View)
	if err != nil {
		return denied, te.Seq{}, err
	}
	if ref == nil {
		return denied, te.Seq{}, nil
	}
	if validateMessage(ref) != nil || !proto.Equal(ref.Source, s.config.Source) {
		return denied, te.Seq{}, ErrInvalidParams
	}
	if req.View != nil && !proto.Equal(req.View, ref) {
		return denied, te.Seq{}, ErrInvalidParams
	}
	head := te.MustParseSeq(ref.SnapshotHead)
	r, err := rangeFromProto(ref.Scope)
	if err != nil {
		return denied, te.Seq{}, err
	}
	if req.Bounds != nil {
		bounds, err := rangeFromProto(req.Bounds)
		if err != nil {
			return denied, te.Seq{}, err
		}
		if r.SeqS.Cmp(bounds.SeqS) < 0 || r.SeqE.Cmp(bounds.SeqE) > 0 {
			return denied, te.Seq{}, nil
		}
	}
	return Selection{Status: Granted, Range: &r, TTL: time.Hour}, head, nil
}
func viewMatches(ref *pb.TapeView, v *pb.View) bool {
	if ref == nil || v == nil || !proto.Equal(ref.Source, v.Source) || ref.SnapshotHead != v.SnapshotHead || len(v.Ranges) != 1 {
		return false
	}
	return proto.Equal(ref.Scope, v.Ranges[0])
}
