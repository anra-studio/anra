package a2a

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/gen/anra/a2a/tape/v1/tapev1connect"
)

const ViewPath = "/" + tapev1connect.TapeViewServiceName

// NewViewHandler serves the generated Connect tape-view API.
// Mount it at ViewPath + "/". Identity comes only from Authenticate.
func NewViewHandler(s *Service, auth Authenticate) (http.Handler, error) {
	if s == nil || auth == nil {
		return nil, ErrInvalidParams
	}
	server := connect.NewServer(validate.NewServerInterceptor())
	tapev1connect.RegisterTapeViewServiceHandler(server, &connectViews{service: s})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server, connecthttp.WithReadMaxBytes(MaxBodyBytes), connecthttp.WithSendMaxBytes(MaxBodyBytes))
	errorWriter := connecthttp.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := auth(r)
		if err != nil || p == "" {
			_ = errorWriter.Write(w, r, connect.NewError(connect.CodeUnauthenticated, "unauthenticated peer"))
			return
		}
		if !activated(r.Header) {
			_ = errorWriter.Write(w, r, connectError(ErrInactive))
			return
		}
		w.Header().Set(ExtensionHeader, ExtensionURI)
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, p)))
	}), nil
}

type connectViews struct{ service *Service }

var _ tapev1connect.TapeViewServiceHandler = (*connectViews)(nil)

func connectPeer(ctx context.Context) string {
	p, _ := ctx.Value(peerKey{}).(string)
	return p
}

func (h *connectViews) Borrow(ctx context.Context, req *pb.BorrowRequest) (*pb.BorrowResponse, error) {
	r := BorrowRequest{RequestID: req.RequestId, Purpose: req.Purpose, Query: req.Query}
	if req.Bounds != nil {
		bounds, err := rangeFromProto(req.Bounds)
		if err != nil {
			return nil, connectError(err)
		}
		r.Bounds = &bounds
	}
	d, err := h.service.Borrow(ctx, connectPeer(ctx), r)
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.BorrowResponse{Decision: decisionToProto(d)}, nil
}

func (h *connectViews) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	d, err := h.service.Get(ctx, connectPeer(ctx), req.RequestId)
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.GetResponse{Decision: decisionToProto(d)}, nil
}

func (h *connectViews) Read(ctx context.Context, req *pb.ReadRequest) (*pb.ReadResponse, error) {
	p, err := h.service.Read(ctx, connectPeer(ctx), ReadRequest{ViewID: req.ViewId, Cursor: req.Cursor, PageSize: int(req.PageSize)})
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.ReadResponse{Page: pageToProto(p)}, nil
}

func (h *connectViews) Release(ctx context.Context, req *pb.ReleaseRequest) (*pb.ReleaseResponse, error) {
	if err := h.service.Release(ctx, connectPeer(ctx), req.ViewId); err != nil {
		return nil, connectError(err)
	}
	return &pb.ReleaseResponse{Released: true}, nil
}

// Preserve public error categories without exposing storage or policy details.
func connectError(err error) error {
	for _, mapping := range connectErrors {
		if errors.Is(err, mapping.err) {
			return connect.NewError(mapping.code, mapping.err.Error()).WithCause(err)
		}
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, "canceled").WithCause(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, "deadline exceeded").WithCause(err)
	}
	return connect.NewError(connect.CodeInternal, "internal error")
}

var connectErrors = []struct {
	err  error
	code connect.Code
}{
	{ErrInvalidParams, connect.CodeInvalidArgument},
	{ErrInactive, connect.CodeFailedPrecondition},
	{ErrConflict, connect.CodeAlreadyExists},
	{ErrUnavailable, connect.CodeNotFound},
	{ErrStorage, connect.CodeInternal},
	{ErrUnsupported, connect.CodeUnimplemented},
}

func fromConnectError(err error) error {
	var rpc *connect.Error
	if errors.As(err, &rpc) {
		if rpc.Code() == connect.CodeInvalidArgument {
			return errors.Join(ErrInvalidParams, err)
		}
		for _, mapping := range connectErrors {
			if rpc.Code() == mapping.code && rpc.Message() == mapping.err.Error() {
				return errors.Join(mapping.err, err)
			}
		}
	}
	return err
}
