package a2a

import (
	"context"
	"net/http"

	"github.com/anra-studio/anra/pkg/model/view"

	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/gen/anra/a2a/tape/v1/tapev1connect"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/internal/httpserver"
)

const (
	ViewPath     = "/" + tapev1connect.TapeViewServiceName
	ViewRESTPath = "/v1/tape/"
)

// NewViewHandler serves the generated Connect and REST tape-view APIs.
// Mount it at ViewPath + "/" and ViewRESTPath. Identity comes only from Authenticate.
func NewViewHandler(s *view.Service, auth Authenticate) (http.Handler, error) {
	if s == nil || auth == nil {
		return nil, ErrInvalidParams
	}
	server := connect.NewServer(viewAccess, validate.NewServerInterceptor())

	tapev1connect.RegisterTapeViewServiceHandler(
		server,
		&connectViews{
			service: s,
		},
	)

	handler, err := httpserver.NewHandler(server)
	if err != nil {
		return nil, err
	}

	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			p, err := auth(r)
			if err != nil {
				p = ""
			}

			handler.ServeHTTP(w, r.WithContext(context.WithValue(
				r.Context(),
				peerKey{},
				p,
			)))
		},
	), nil
}

// Access checks run before validation/dispatch for every transport. Its adapter
// writes each error in the caller's protocol, including Vanguard REST status JSON.
func viewAccess(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		if connectPeer(ctx) == "" {
			return connect.NewError(connect.CodeUnauthenticated, "unauthenticated peer")
		}
		info, ok := connect.CallInfoForServerContext(ctx)
		if !ok || !extensionActive(info.RequestHeader()) {
			return connectError(ErrInactive)
		}
		info.ResponseHeader().Set(ExtensionHeader, ExtensionURI)
		return next(ctx, spec, stream)
	}
}

type connectViews struct {
	service *view.Service
}

var _ tapev1connect.TapeViewServiceHandler = (*connectViews)(nil)

func connectPeer(ctx context.Context) string {
	p, _ := ctx.Value(
		peerKey{},
	).(string)

	return p
}

func (h *connectViews) Borrow(ctx context.Context, req *pb.BorrowRequest) (*rpcpb.BorrowResponse, error) {
	d, err := h.service.Borrow(ctx, connectPeer(ctx), req)
	if err != nil {
		return nil, connectError(err)
	}

	return &rpcpb.BorrowResponse{
		Decision: d,
	}, nil
}

func (h *connectViews) Get(ctx context.Context, req *rpcpb.GetRequest) (*rpcpb.GetResponse, error) {
	d, err := h.service.Get(ctx, connectPeer(ctx), req.RequestId)
	if err != nil {
		return nil, connectError(err)
	}

	return &rpcpb.GetResponse{
		Decision: d,
	}, nil
}

func (h *connectViews) Read(ctx context.Context, req *rpcpb.ReadRequest) (*rpcpb.ReadResponse, error) {
	p, err := h.service.Read(ctx, connectPeer(ctx), req.LeaseId, req.Cursor, req.PageSize)
	if err != nil {
		return nil, connectError(err)
	}

	return &rpcpb.ReadResponse{
		Quote:      p.Quote.Proto(),
		NextCursor: p.NextCursor,
	}, nil
}

func (h *connectViews) Release(ctx context.Context, req *rpcpb.ReleaseRequest) (*rpcpb.ReleaseResponse, error) {
	if err := h.service.Release(ctx, connectPeer(ctx), req.LeaseId); err != nil {
		return nil, connectError(err)
	}

	return &rpcpb.ReleaseResponse{
		Released: true,
	}, nil
}
