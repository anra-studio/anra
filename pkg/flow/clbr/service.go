// Package clbr coordinates peer collaboration independently of its transport.
package clbr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/model/delegator"
	"github.com/go-playground/validator/v10"
)

var ErrInvalidParams = errors.New("invalid collaboration parameters")

// Result combines domain response metadata with text. Suppressed is local
// turn state and must not be serialized as an A2A response status.
type Result struct {
	*cp.ResponseMetadata
	Answer     string
	Suppressed bool
}

// Sender and Borrower are implemented by the real A2A clients. Protocol
// discovery, encoding and provenance validation remain at those boundaries.
type Sender interface {
	Send(context.Context, string) (Result, error)
}

type Borrower interface {
	BorrowContext(context.Context, *pb.BorrowRequest) (*pb.Decision, []*pb.Quote, error)
}

type Request struct {
	Self    string `validate:"required"`
	Peer    string `validate:"required"`
	Message string `validate:"required"`
	Context []*pb.Ref
}

type Connect func(context.Context, string) (Sender, error)

// Service is stateless and its zero value is usable. Turn-local collaboration
// state belongs to model/delegator in context, not to this service or transport.
type Service struct{}

func (s *Service) Send(ctx context.Context, req Request, connect Connect) (Result, error) {
	if err := validator.New().Struct(req); err != nil {
		return Result{}, fmt.Errorf("%w: request: %w", ErrInvalidParams, err)
	}
	if req.Peer == req.Self || strings.TrimSpace(req.Message) == "" || connect == nil {
		return Result{}, ErrInvalidParams
	}
	path := delegator.FromContext(ctx)
	outgoing, err := path.Next(ctx, req.Self, req.Peer, req.Context)
	if errors.Is(err, delegator.ErrNoNewContext) {
		return Result{
			Suppressed: true,
		}, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Result{}, err
	}
	if err != nil {
		return Result{}, fmt.Errorf("%w: delegation: %w", ErrInvalidParams, err)
	}
	peer, err := connect(outgoing, req.Peer)
	if err != nil {
		return Result{}, err
	}
	result, err := peer.Send(outgoing, req.Message)
	if err != nil {
		return Result{}, err
	}
	path.Observe(req.Peer, result.ResponseMetadata)
	return result, nil
}

// Borrow records progress only after the transport has validated and completely
// read the authorized view. A denied or pending request never clears BUSY memory.
func (s *Service) Borrow(ctx context.Context, peer string, client Borrower, request *pb.BorrowRequest) (*pb.Decision, []*pb.Quote, error) {
	decision, pages, err := client.BorrowContext(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	if decision.GetStatus() == pb.DecisionStatus_DECISION_STATUS_GRANTED && decision.Lease != nil {
		delegator.FromContext(ctx).Borrowed(peer, decision.Lease)
	}
	return decision, pages, nil
}

// Runtime owns admission and execution. BusyError reports an unaccepted call;
// clbr turns it into a collaboration outcome while the transport encodes it.
type (
	Runtime interface {
		Run(context.Context, string) (string, error)
	}
	BusyError interface {
		error
		BusyView() *pb.Ref
	}
)

func (s *Service) Run(ctx context.Context, runtime Runtime, input string) (Result, error) {
	output, err := runtime.Run(ctx, input)
	var busy BusyError
	if errors.As(err, &busy) {
		return Result{
			ResponseMetadata: &cp.ResponseMetadata{
				Status: cp.Status_STATUS_BUSY,
				Retry:  cp.RetryHint_RETRY_HINT_AFTER_NEW_VIEW,
				View:   busy.BusyView(),
			},
			Answer: "Agent is busy; request was not accepted.",
		}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{
		ResponseMetadata: &cp.ResponseMetadata{
			Status: cp.Status_STATUS_COMPLETED,
		},
		Answer: output,
	}, nil
}
