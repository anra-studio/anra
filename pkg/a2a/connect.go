package a2a

import (
	"context"
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
		if !extensionActive(r.Header) {
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
