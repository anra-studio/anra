package mcp

import (
	"context"

	"github.com/anra-studio/anra/pkg/model/view"
)

func (s *Service) readTapeView(ctx context.Context, target *ReadTarget) (*view.Quote, *Error) {
	stream, err := s.config.Borrowing.Read(ctx, target.Caller, target.LeaseID)
	if err != nil {
		return nil, toViewError(err)
	}
	quote, err := view.Decode(stream)
	if err != nil {
		return nil, toViewError(err)
	}
	return quote, nil
}
