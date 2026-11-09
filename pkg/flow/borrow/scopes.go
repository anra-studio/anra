package borrow

import (
	"fmt"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func leaseScopes(ref *pb.Ref) ([]tv.EntryRange, error) {
	scopes := make([]tv.EntryRange, 0, len(ref.Scopes))
	for _, scope := range ref.Scopes {
		start, err := te.ParseSeq(scope.SeqS)
		if err != nil {
			return nil, fmt.Errorf("%w: lease scope start: %w", ErrStorage, err)
		}
		end, err := te.ParseSeq(scope.SeqE)
		if err != nil {
			return nil, fmt.Errorf("%w: lease scope end: %w", ErrStorage, err)
		}
		scopes = append(
			scopes,
			tv.EntryRange{
				SeqS: start,
				SeqE: end,
			},
		)
	}
	return scopes, nil
}
