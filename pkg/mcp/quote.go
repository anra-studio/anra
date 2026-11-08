package mcp

import (
	"context"

	"github.com/anra-studio/anra/pkg/model/view"
)

func (s *Service) readTapeView(ctx context.Context, target *ReadTarget) (*view.Quote, *Error) {
	quote, err := s.config.Borrowing.ReadAll(ctx, target.Caller, target.LeaseID)
	if err == nil {
		return quote, nil
	}
	return nil, toViewError(err)
}
