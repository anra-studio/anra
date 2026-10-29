package a2a

import (
	"context"
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/validate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/gen/anra/a2a/tape/v1/tapev1connect"
	"github.com/anra-studio/anra/internal/httpserver"
)

const ViewPath = "/" + tapev1connect.TapeViewServiceName
const ViewRESTPath = "/v1/tape/"

// NewViewHandler serves the generated Connect and REST tape-view APIs.
// Mount it at ViewPath + "/" and ViewRESTPath. Identity comes only from Authenticate.
func NewViewHandler(s *Service, auth Authenticate) (http.Handler, error) {
	if s == nil || auth == nil {
		return nil, ErrInvalidParams
	}
	server := connect.NewServer(viewAccess, validate.NewServerInterceptor())
	tapev1connect.RegisterTapeViewServiceHandler(server, &connectViews{service: s})
	handler, err := httpserver.NewHandler(server, MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := auth(r)
		if err != nil {
			p = ""
		}
		handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, p)))
	}), nil
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

type connectViews struct{ service *Service }

var _ tapev1connect.TapeViewServiceHandler = (*connectViews)(nil)

func connectPeer(ctx context.Context) string {
	p, _ := ctx.Value(peerKey{}).(string)
	return p
}

func (h *connectViews) Borrow(ctx context.Context, req *pb.BorrowRequest) (*pb.BorrowResponse, error) {
	d, err := h.service.Borrow(ctx, connectPeer(ctx), req)
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.BorrowResponse{Decision: d}, nil
}

func (h *connectViews) Get(ctx context.Context, req *pb.GetRequest) (*pb.GetResponse, error) {
	d, err := h.service.Get(ctx, connectPeer(ctx), req.RequestId)
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.GetResponse{Decision: d}, nil
}

func (h *connectViews) Read(ctx context.Context, req *pb.ReadRequest) (*pb.ReadResponse, error) {
	p, err := h.service.Read(ctx, connectPeer(ctx), req)
	if err != nil {
		return nil, connectError(err)
	}
	return &pb.ReadResponse{Page: p}, nil
}

func (h *connectViews) Release(ctx context.Context, req *pb.ReleaseRequest) (*pb.ReleaseResponse, error) {
	if err := h.service.Release(ctx, connectPeer(ctx), req.ViewId); err != nil {
		return nil, connectError(err)
	}
	return &pb.ReleaseResponse{Released: true}, nil
}
