// Package borrow owns request admission, approval and authorized content reads.
package borrow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/flow/grant"
	"github.com/anra-studio/anra/pkg/model/view"
	"github.com/go-playground/validator/v10"
	"github.com/scbizu/tape-go/pkg/tape"
)

var (
	ErrInvalidParams = errors.New("invalid parameters")
	ErrConflict      = errors.New("request ID conflict")
	ErrUnavailable   = errors.New("view unavailable")
	ErrStorage       = errors.New("tape storage failure")
)

const (
	Granted = pb.DecisionStatus_DECISION_STATUS_GRANTED
	Denied  = pb.DecisionStatus_DECISION_STATUS_DENIED
	Pending = pb.DecisionStatus_DECISION_STATUS_PENDING
)

// Selection is a review outcome for a new request. Pending completion uses the
// explicit Approve and Reject operations instead of another Selection.
type Selection struct {
	Status pb.DecisionStatus `validate:"oneof=1 2 3"`
	TTL    time.Duration
}

type Config struct {
	Source   *pb.Source     `validate:"required"`
	Tape     *tape.Tape     `validate:"required"`
	Views    Views          `validate:"required"`
	Journal  *Journal       `validate:"required"`
	Grants   *grant.Service `validate:"required"`
	Exporter Exporter

	// Review evaluates a new request. Nil grants eligible committed views for
	// one hour. Recorded requests replay without invoking this policy again.
	Review func(context.Context, string, *pb.BorrowRequest) (Selection, error)
}

// Service borrows its dependencies. mu preserves per-service callback ordering;
// the shared Journal owns state and atomic commits across all service instances.
type Service struct {
	mu       sync.Mutex
	config   Config
	validate *validator.Validate
}

// New initializes borrowing defaults. The host must wire the Tape's grant
// handler and Grants to the same Journal, Source and owner.
func New(c Config) (*Service, error) {
	validate := validator.New()
	if err := validate.Struct(c); err != nil {
		return nil, fmt.Errorf("%w: borrowing config: %w", ErrInvalidParams, err)
	}
	if c.Exporter == nil {
		c.Exporter = view.ContextExporter{}
	}
	if c.Review == nil {
		c.Review = func(context.Context, string, *pb.BorrowRequest) (Selection, error) {
			return Selection{
				Status: Granted,
				TTL:    time.Hour,
			}, nil
		}
	}
	return &Service{
		config:   c,
		validate: validate,
	}, nil
}

func recordError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, grant.ErrInvalidParams):
		return fmt.Errorf("%w: %w", ErrInvalidParams, err)
	case errors.Is(err, grant.ErrUnavailable):
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalidParams), errors.Is(err, ErrUnavailable), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrStorage, err)
	}
}
