package a2a

import (
	"context"
	"encoding/json"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Publications resolves permitted immutable records using local data only.
// Implementations must not enter reasoning or call external services.
type Publications interface {
	Publication(context.Context, string, *pb.AnchorRef) (*pb.AnchorRef, error)
}

type PublicationExporter struct{}

func (PublicationExporter) Export(e te.EntryLike) (*pb.ExportedEntry, error) {
	r, err := anchor.Decode(e)
	if err != nil {
		return nil, ErrUnsupported
	}
	if r == nil {
		return (TextExporter{}).Export(e)
	}
	if r.Publication == nil {
		return nil, ErrUnsupported
	}
	data, err := json.Marshal(r.Publication)
	if err != nil {
		return nil, err
	}
	return &pb.ExportedEntry{Seq: e.GetID().String(), Kind: string(e.GetKind()), Summary: r.Publication.Summary, Timestamp: timestamppb.New(e.GetTimestamp()), Payload: &pb.Payload{MediaType: anchor.MediaType, JsonData: data}}, nil
}
func AdvertisePublications(card *sdk.AgentCard, endpoint string) error {
	if err := Advertise(card, endpoint); err != nil {
		return err
	}
	for i := range card.Capabilities.Extensions {
		ext := &card.Capabilities.Extensions[i]
		if ext.URI == ExtensionURI {
			ext.Params["publicationBorrowVersion"] = 1
			ext.Params["entryMediaTypes"] = []string{TextMediaType, anchor.MediaType}
		}
	}
	return nil
}
func (s *Service) publicationSelection(ctx context.Context, p string, req *pb.BorrowRequest) (Selection, te.Seq, error) {
	denied := Selection{Status: Denied}
	if s.config.Publications == nil {
		return denied, te.Seq{}, nil
	}
	ref, err := s.config.Publications.Publication(ctx, p, req.Publication)
	if err != nil {
		return denied, te.Seq{}, err
	}
	if ref == nil {
		return denied, te.Seq{}, nil
	}
	if validateMessage(ref) != nil || !proto.Equal(ref.Source, s.config.Source) {
		return denied, te.Seq{}, ErrInvalidParams
	}
	if req.Publication != nil && !proto.Equal(req.Publication, ref) {
		return denied, te.Seq{}, ErrInvalidParams
	}
	head := te.MustParseSeq(ref.Seq)
	r := tv.EntryRange{SeqS: head, SeqE: head.Next()}
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
func publicationMatches(ref *pb.AnchorRef, v *pb.View) bool {
	if ref == nil || v == nil || !proto.Equal(ref.Source, v.Source) || ref.Seq != v.SnapshotHead || len(v.Ranges) != 1 {
		return false
	}
	seq, err := te.ParseSeq(ref.Seq)
	return err == nil && v.Ranges[0].SeqS == ref.Seq && v.Ranges[0].SeqE == seq.Next().String()
}
