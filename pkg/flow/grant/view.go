package grant

import (
	"fmt"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/samber/lo"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

// viewRef derives one lease target from an ordered native view collection.
// Only views from one owner/session may contribute to the same committed Ref.
func viewRef(source *pb.Source, identity string, views []*tv.EntryView) (*pb.Ref, error) {
	if len(views) == 0 {
		return nil, fmt.Errorf("%w: missing grant views", ErrInvalidParams)
	}
	for i, v := range views {
		if v == nil || v.Owner != identity {
			return nil, fmt.Errorf("%w: grant view owner mismatch", ErrInvalidParams)
		}
		if i > 0 && (v.SessionId != views[0].SessionId || views[i-1].Scope.SeqE.Cmp(v.Scope.SeqS) > 0) {
			return nil, fmt.Errorf("%w: grant views cross sessions or overlap", ErrInvalidParams)
		}
	}
	ref := &pb.Ref{
		Source: proto.Clone(source).(*pb.Source),
		Scopes: lo.Map(
			views,
			func(v *tv.EntryView, _ int) *pb.EntryRange {
				return &pb.EntryRange{
					SeqS: v.Scope.SeqS.String(),
					SeqE: v.Scope.SeqE.String(),
				}
			},
		),
	}
	if err := protovalidate.Validate(ref); err != nil {
		return nil, fmt.Errorf("%w: grant view: %w", ErrInvalidParams, err)
	}
	return ref, nil
}
