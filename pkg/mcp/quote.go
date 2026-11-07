package mcp

import (
	"context"
	"errors"

	"github.com/anra-studio/anra/pkg/model/view"
)

func (s *Service) readTapeView(ctx context.Context, grant *Grant) (*view.Quote, error) {
	quote, err := s.config.Views.ReadAll(ctx, grant.Caller, grant.LeaseID)
	if err == nil {
		return quote, nil
	}
	if errors.Is(err, view.ErrUnavailable) {
		return nil, ErrPermissionDenied
	}
	if errors.Is(err, view.ErrUnsupported) {
		return nil, ErrFailedPrecondition
	}
	return nil, ErrUnavailable
}
