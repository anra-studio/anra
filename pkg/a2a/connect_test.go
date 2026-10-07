package a2a_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/gen/anra/a2a/tape/v1/tapev1connect"
	a "github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
)

func TestConnectTapeViews(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, tape, "local", "first finding")
	appendText(t, tape, "local", "second finding")
	s := service(t, config(tape, journal, grantPolicy(1, 3)))
	lender, err := agent.New("local", tape)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := a.NewExecutor(lender)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	card := &sdk.AgentCard{Name: "researcher"}
	if err := a.Advertise(card, base+a.ViewPath); err != nil {
		t.Fatal(err)
	}
	handler, err := a.NewPeerHandler(card, a2asrv.NewHandler(executor), s, authenticate)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	oldResponse, err := server.Client().Post(base+"/tape-views", "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":"1","method":"tape/views/borrow","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("removed route returned HTTP %d", oldResponse.StatusCode)
	}
	oldCard := &sdk.AgentCard{Capabilities: sdk.AgentCapabilities{Extensions: []sdk.AgentExtension{{URI: a.ExtensionURI, Params: map[string]any{"endpoint": base + "/tape-views"}}}}}
	if _, err := a.NewClient(server.Client(), oldCard); !errors.Is(err, a.ErrInvalidParams) {
		t.Fatalf("legacy endpoint accepted: %v", err)
	}
	httpClient := &http.Client{Transport: credentialTransport{server.Client().Transport, "borrower-token"}, Timeout: 5 * time.Second}
	discovered, err := a.Discover(t.Context(), httpClient, base+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatal(err)
	}
	client, err := a.NewClient(httpClient, discovered)
	if err != nil {
		t.Fatal(err)
	}
	d, err := client.Borrow(t.Context(), request())
	if err != nil || d.Status != a.Granted {
		t.Fatalf("Borrow = %#v, %v", d, err)
	}
	again, err := client.Get(t.Context(), request().RequestID)
	if err != nil || again.View.ID != d.View.ID {
		t.Fatalf("Get = %#v, %v", again, err)
	}
	first, err := client.Read(t.Context(), a.ReadRequest{ViewID: d.View.ID, PageSize: 1})
	if err != nil || len(first.Entries) != 1 || first.NextCursor == "" {
		t.Fatalf("Read = %#v, %v", first, err)
	}
	second, err := client.Read(t.Context(), a.ReadRequest{ViewID: d.View.ID, Cursor: first.NextCursor, PageSize: 1})
	if err != nil || len(second.Entries) != 1 || second.NextCursor != "" || second.Entries[0].Summary != "second finding" {
		t.Fatalf("Read next = %#v, %v", second, err)
	}
	result, err := (a.BorrowTool{Client: client}).Execute(t.Context(), json.RawMessage(`{"requestId":"request-1","purpose":"debug storage","query":"bbolt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var borrowed struct {
		Context []a.Page `json:"context"`
	}
	if err := json.Unmarshal(result, &borrowed); err != nil || len(borrowed.Context) != 1 || len(borrowed.Context[0].Entries) != 2 {
		t.Fatalf("tool = %s, %v", result, err)
	}
	changed := request()
	changed.Query = "different"
	if _, err := client.Borrow(t.Context(), changed); !errors.Is(err, a.ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	intruderHTTP := &http.Client{Transport: credentialTransport{server.Client().Transport, "intruder-token"}}
	intruder, err := a.NewClient(intruderHTTP, discovered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := intruder.Get(t.Context(), request().RequestID); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder get = %v", err)
	}
	if _, err := intruder.Read(t.Context(), a.ReadRequest{ViewID: d.View.ID}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder read = %v", err)
	}
	if err := intruder.Release(t.Context(), d.View.ID); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder release = %v", err)
	}
	for _, jsonCodec := range []bool{false, true} {
		options := []connecthttp.Option{}
		if jsonCodec {
			options = append(options, connecthttp.WithProtoJSON())
		}
		raw := tapev1connect.NewTapeViewServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, base, options...)))
		if _, err := raw.Get(t.Context(), &pb.GetRequest{RequestId: request().RequestID}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("missing negotiation = %v", err)
		}
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set(a.ExtensionHeader, a.ExtensionURI)
		res, err := raw.Get(ctx, &pb.GetRequest{RequestId: request().RequestID})
		if err != nil || res.Decision.View.ViewId != d.View.ID {
			t.Fatalf("generated Get = %#v, %v", res, err)
		}
		if info.ResponseHeader().Get(a.ExtensionHeader) != a.ExtensionURI {
			t.Fatal("missing acknowledgement")
		}
		if _, err := raw.Get(ctx, &pb.GetRequest{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("empty request ID = %v", err)
		}
		if _, err := client.Get(t.Context(), ""); !errors.Is(err, a.ErrInvalidParams) || connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("domain validation error = %v", err)
		}
		if _, err := raw.Borrow(ctx, &pb.BorrowRequest{RequestId: "oversize", Query: strings.Repeat("x", a.MaxBodyBytes+1)}); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("oversize = %v", err)
		}
	}
	unauthenticated, err := a.NewClient(server.Client(), discovered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unauthenticated.Get(t.Context(), request().RequestID); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Get(canceled, request().RequestID); connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("canceled = %v", err)
	}
	if err := client.Release(t.Context(), d.View.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(t.Context(), a.ReadRequest{ViewID: d.View.ID}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("released view = %v", err)
	}
}
