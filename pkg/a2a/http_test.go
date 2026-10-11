package a2a_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	a "github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	runner "github.com/scbizu/tape-go/pkg/runner"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This transport represents credentials configured for this known peer only.
type credentialTransport struct {
	next  http.RoundTripper
	token string
}

func (tr credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+tr.token)
	return tr.next.RoundTrip(r)
}
func authenticate(r *http.Request) (string, error) {
	switch r.Header.Get("Authorization") {
	case "Bearer borrower-token":
		return "borrower", nil
	case "Bearer intruder-token":
		return "intruder", nil
	}
	return "", errors.New("invalid credential")
}

type runnerFunc func(context.Context, runner.Request) (runner.Response, error)

func (f runnerFunc) RunTurn(c context.Context, r runner.Request) (runner.Response, error) {
	return f(c, r)
}

func TestTwoIndependentPeersBorrowAndUseContext(t *testing.T) {
	ctx := context.Background()
	lenderTape := openTape(t, filepath.Join(t.TempDir(), "researcher"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, lenderTape, "local", "bbolt requires a single writer; concurrent updates need version checks")
	lender, err := agent.New("local", lenderTape, agent.WithRunner(runnerFunc(func(context.Context, runner.Request) (runner.Response, error) {
		return runner.Response{Output: "research response"}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config(lenderTape, journal, a.PolicyFunc(func(_ context.Context, p string, _ *pb.BorrowRequest, _ te.Seq) (a.Selection, error) {
		if p != "borrower" {
			return a.Selection{Status: a.Denied}, nil
		}
		return a.Selection{Status: a.Granted, Range: &tv.EntryRange{SeqS: seq(1), SeqE: seq(2)}, TTL: time.Hour}, nil
	}))
	s := service(t, cfg)
	server := httptest.NewUnstartedServer(nil)
	baseURL := "http://" + server.Listener.Addr().String()
	card := &sdk.AgentCard{Name: "researcher", Description: "storage specialist", Version: "0.1", SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(baseURL+a.CorePath, sdk.TransportProtocolJSONRPC)}, DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"}, Skills: []sdk.AgentSkill{{ID: "storage", Name: "Storage", Description: "bbolt storage", Tags: []string{"bbolt"}}}}
	if err := a.Advertise(card, baseURL+a.ViewPath); err != nil {
		t.Fatal(err)
	}
	executor, err := a.NewExecutor(lender)
	if err != nil {
		t.Fatal(err)
	}
	// Also prove the trusted HTTP peer identity reaches official SDK middleware.
	core := a2asrv.NewHandler(executor, a2asrv.WithCallInterceptors(checkIdentity{t: t}))
	handler, err := a.NewPeerHandler(card, core, s, authenticate)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	defer server.Close()
	httpClient := &http.Client{Transport: credentialTransport{server.Client().Transport, "borrower-token"}, Timeout: 5 * time.Second}
	discovered, err := a.Discover(ctx, httpClient, baseURL+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		t.Fatal(err)
	}
	client, err := a.NewClient(httpClient, discovered)
	if err != nil {
		t.Fatal(err)
	}
	best, err := a.BestPeer(ctx, a.RankerFunc(func(context.Context, string, []string) ([]a.Score, error) {
		return []a.Score{{Index: 0, Score: 1}}, nil
	}), "storage", []a.Peer{{CardURL: baseURL + a2asrv.WellKnownAgentCardPath, Card: discovered}})
	if err != nil || best.Card.Name != "researcher" {
		t.Fatalf("best peer = %#v %v", best, err)
	}
	// An extension-unaware official client still uses standard core A2A.
	ordinary, err := a2aclient.NewFromCard(ctx, discovered, a2aclient.WithJSONRPCTransport(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	native, err := ordinary.SendMessage(ctx, &sdk.SendMessageRequest{Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("hello"))})
	if err != nil {
		t.Fatal(err)
	}
	message, ok := native.(*sdk.Message)
	if !ok || message.Parts[0].Text() != "research response" {
		t.Fatalf("core result = %#v", native)
	}
	var events int
	for event, err := range ordinary.SendStreamingMessage(ctx, &sdk.SendMessageRequest{Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("stream hello"))}) {
		if err != nil {
			t.Fatal(err)
		}
		if event == nil {
			t.Fatal("empty stream event")
		}
		events++
	}
	if events == 0 {
		t.Fatal("core stream lost")
	}
	borrowerTape := openTape(t, filepath.Join(t.TempDir(), "borrower"), "borrower-local")
	turns := 0
	borrowedRunner := runnerFunc(func(_ context.Context, req runner.Request) (runner.Response, error) {
		turns++
		if turns == 1 {
			return runner.Response{ToolCalls: []runner.ToolCall{{ID: "borrow-call", Name: "borrow_peer_context", Arguments: json.RawMessage(`{"requestId":"runtime-borrow","purpose":"fix storage race","query":"bbolt"}`)}}}, nil
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != runner.RoleTool || len(last.ToolResults) != 1 || !strings.Contains(string(last.ToolResults[0].Result), "single writer") || !strings.Contains(string(last.ToolResults[0].Result), `"agentId":"researcher"`) || !strings.Contains(string(last.ToolResults[0].Result), `"seq":"1"`) {
			return runner.Response{}, fmt.Errorf("borrowed context/provenance missing: %#v", last)
		}
		return runner.Response{Output: "Use version checks for competing writes."}, nil
	})
	borrower, err := agent.New("borrower-local", borrowerTape, agent.WithRunner(borrowedRunner), agent.WithTool(a.BorrowTool{Client: client}))
	if err != nil {
		t.Fatal(err)
	}
	output, err := borrower.Run(ctx, "help with storage concurrency")
	if err != nil || output != "Use version checks for competing writes." {
		t.Fatalf("borrower run = %q %v", output, err)
	}
	// Restart the borrower: context remains a structured tool result, not a
	// forged local peer assistant message or a replayed remote tool execution.
	restarted, err := agent.New("borrower-local", borrowerTape, agent.WithRunner(runnerFunc(func(_ context.Context, req runner.Request) (runner.Response, error) {
		for _, m := range req.Messages {
			if m.Role == runner.RoleTool && len(m.ToolResults) == 1 && strings.Contains(string(m.ToolResults[0].Result), "single writer") {
				return runner.Response{Output: "memory retained"}, nil
			}
		}
		return runner.Response{}, errors.New("borrowed context lost on replay")
	})))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Run(ctx, "remember the peer finding"); err != nil {
		t.Fatal(err)
	}
	local, err := borrowerTape.Range(owner.WithOwnerId(ctx, "borrower-local"), tv.EntryRange{SeqS: te.SeqFromUint64(1), SeqE: te.SeqFromUint64(8)})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range local.Raw {
		if e.GetOwner() != "borrower-local" {
			t.Fatal("peer ownership imported")
		}
	}
	// Borrow/get/read/release all work over HTTP, including persisted closure.
	d, err := client.Borrow(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Get(ctx, request().RequestId)
	if err != nil || got.View.ViewId != d.View.ViewId {
		t.Fatalf("get = %#v %v", got, err)
	}
	if err := client.Release(ctx, d.View.ViewId); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Read(ctx, &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatal("released read succeeded")
	}
	intruderHTTP := &http.Client{Transport: credentialTransport{server.Client().Transport, "intruder-token"}}
	intruder, err := a.NewClient(intruderHTTP, discovered)
	if err != nil {
		t.Fatal(err)
	}
	denial, err := intruder.Borrow(ctx, request())
	if err != nil || denial.Status != a.Denied || denial.View != nil {
		t.Fatalf("denial = %#v %v", denial, err)
	}
}

type checkIdentity struct {
	a2asrv.PassthroughCallInterceptor
	t *testing.T
}

func (c checkIdentity) Before(ctx context.Context, call *a2asrv.CallContext, _ *a2asrv.Request) (context.Context, any, error) {
	if call.User == nil || !call.User.Authenticated || call.User.Name != "borrower" {
		c.t.Errorf("SDK identity missing: %#v", call.User)
	}
	return ctx, nil, nil
}

func TestExtensionHTTPValidationAndNegotiation(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, tape, "local", "entry")
	calls := 0
	cfg := config(tape, journal, a.PolicyFunc(func(_ context.Context, peer string, _ *pb.BorrowRequest, _ te.Seq) (a.Selection, error) {
		if peer != "borrower" {
			t.Errorf("unverified body identity reached policy: %q", peer)
		}
		calls++
		return a.Selection{Status: a.Denied}, nil
	}))
	h, err := a.NewViewHandler(service(t, cfg), authenticate)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, method, body, token, extension string
		status                               int
		code                                 string
	}{
		{"unauthenticated", "Borrow", `{}`, "", "", 401, "unauthenticated"},
		{"inactive", "Borrow", `{"requestId":"1","purpose":"test","query":"test"}`, "borrower-token", "", 400, "failed_precondition"},
		{"parse", "Borrow", `{`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"batch", "Borrow", `[]`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"old envelope", "Borrow", `{"jsonrpc":"2.0","id":"1","method":"tape/views/borrow","params":{"requestId":"1","purpose":"test","query":"test"}}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"unknown", "Unknown", `{}`, "borrower-token", a.ExtensionURI, 501, "unimplemented"},
		{"invalid bounds", "Borrow", `{"requestId":"1","purpose":"test","query":"test","bounds":{"seqS":"01","seqE":"2"}}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"reversed bounds", "Borrow", `{"requestId":"1","purpose":"test","query":"test","bounds":{"seqS":"10","seqE":"9"}}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"equal bounds", "Borrow", `{"requestId":"1","purpose":"test","query":"test","bounds":{"seqS":"9","seqE":"9"}}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"zero start", "Borrow", `{"requestId":"1","purpose":"test","query":"test","bounds":{"seqS":"0","seqE":"9"}}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"missing purpose", "Borrow", `{"requestId":"1","query":"test"}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"request ID byte limit", "Borrow", `{"requestId":"` + strings.Repeat("中", 86) + `","purpose":"test","query":"test"}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"purpose byte limit", "Borrow", `{"requestId":"1","purpose":"` + strings.Repeat("x", 8193) + `","query":"test"}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"query byte limit", "Borrow", `{"requestId":"1","purpose":"test","query":"` + strings.Repeat("x", 8193) + `"}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"empty get", "Get", `{}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"empty read", "Read", `{}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"empty release", "Release", `{}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"negative page", "Read", `{"viewId":"missing","pageSize":-1}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"page limit", "Read", `{"viewId":"missing","pageSize":101}`, "borrower-token", a.ExtensionURI, 400, "invalid_argument"},
		{"oversize", "Borrow", strings.Repeat(" ", a.MaxBodyBytes+1), "borrower-token", a.ExtensionURI, 429, "resource_exhausted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", a.ViewPath+"/"+tt.method, strings.NewReader(tt.body))
			if tt.token != "" {
				r.Header.Set("Authorization", "Bearer "+tt.token)
			}
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Connect-Protocol-Version", "1")
			r.Header.Set(a.ExtensionHeader, tt.extension)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if tt.code != "" {
				var envelope struct {
					Code    string            `json:"code"`
					Details []json.RawMessage `json:"details"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Code != tt.code {
					t.Fatalf("RPC %s: %s", envelope.Code, w.Body.String())
				}
				if tt.code == "invalid_argument" && tt.name != "parse" && tt.name != "batch" && len(envelope.Details) == 0 {
					t.Fatalf("missing interceptor validation details: %s", w.Body.String())
				}
			}
		})
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached policy %d times", calls)
	}
	r := httptest.NewRequest("POST", a.ViewPath+"/Borrow", strings.NewReader(`{"requestId":"ok","purpose":"test","query":"test","owner":"victim"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer borrower-token")
	r.Header.Add(a.ExtensionHeader, "urn:unknown, "+a.ExtensionURI)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get(a.ExtensionHeader) != a.ExtensionURI || calls != 1 || !strings.Contains(w.Body.String(), `"status":"DECISION_STATUS_DENIED"`) {
		t.Fatalf("negotiation = %s", w.Body.String())
	}
	for i, bounds := range [][2]string{{"9", "10"}, {"184467440737095516160000000001", "184467440737095516160000000002"}} {
		body := fmt.Sprintf(`{"requestId":"ordered-%d","purpose":"test","query":"test","bounds":{"seqS":"%s","seqE":"%s"}}`, i, bounds[0], bounds[1])
		r := httptest.NewRequest("POST", a.ViewPath+"/Borrow", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer borrower-token")
		r.Header.Set(a.ExtensionHeader, a.ExtensionURI)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || calls != i+2 {
			t.Fatalf("valid arbitrary-precision range rejected: %s", w.Body.String())
		}
	}
}

func TestClientAcceptsAdditionalResponseFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(a.ExtensionHeader, a.ExtensionURI)
		w.Header().Set("Content-Type", "application/proto")
		body, err := proto.Marshal(&pb.GetResponse{Decision: &pb.Decision{RequestId: "request-1", Status: pb.DecisionStatus_DECISION_STATUS_DENIED}})
		if err != nil {
			t.Error(err)
			return
		}
		body = protowire.AppendTag(body, 100, protowire.BytesType)
		body = protowire.AppendString(body, "future extension")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	card := &sdk.AgentCard{}
	if err := a.Advertise(card, server.URL+a.ViewPath); err != nil {
		t.Fatal(err)
	}
	client, err := a.NewClient(server.Client(), card)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := client.Get(context.Background(), "request-1")
	if err != nil || decision.Status != a.Denied {
		t.Fatalf("Get() = %#v, error %v", decision, err)
	}
}

func TestClientRejectsMissingAcknowledgementAndMismatchedProvenance(t *testing.T) {
	for _, mode := range []string{"inactive", "wrong provenance", "wrong view", "wrong snapshot", "missing entries", "out of range", "no progress", "repeated page"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode != "inactive" {
					w.Header().Set(a.ExtensionHeader, a.ExtensionURI)
				}
				w.Header().Set("Content-Type", "application/proto")
				now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
				view := &pb.View{ViewId: "view", Source: &pb.Source{AgentId: "peer", TapeId: "tape"}, SnapshotHead: "1", Ranges: []*pb.EntryRange{{SeqS: "1", SeqE: "2"}}, ExpiresAt: timestamppb.New(now)}
				result, err := proto.Marshal(&pb.BorrowResponse{Decision: &pb.Decision{RequestId: "request-1", Status: pb.DecisionStatus_DECISION_STATUS_GRANTED, View: view}})
				if err != nil {
					t.Error(err)
					return
				}
				if r.URL.Path == a.ViewPath+"/Read" {
					p := &pb.Page{ViewId: "view", Source: &pb.Source{AgentId: "peer", TapeId: "tape"}, SnapshotHead: "1"}
					reads++
					switch mode {
					case "wrong provenance":
						p.Entries = []*pb.ExportedEntry{{Seq: "1", Timestamp: timestamppb.New(now)}}
						p.Source.AgentId = "different"
					case "wrong view":
						p.Entries = []*pb.ExportedEntry{{Seq: "1", Timestamp: timestamppb.New(now)}}
						p.ViewId = "different"
					case "wrong snapshot":
						p.Entries = []*pb.ExportedEntry{{Seq: "1", Timestamp: timestamppb.New(now)}}
						p.SnapshotHead = "2"
					case "out of range":
						p.Entries = []*pb.ExportedEntry{{Seq: "2", Timestamp: timestamppb.New(now)}}
					case "no progress":
						p.NextCursor = "again"
					case "repeated page":
						p.Entries = []*pb.ExportedEntry{{Seq: "1", Timestamp: timestamppb.New(now)}}
						p.NextCursor = "again"
					}
					// Bound the experiment even when a progress check is ablated.
					if reads > 2 {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					result, err = proto.Marshal(&pb.ReadResponse{Page: p})
					if err != nil {
						t.Error(err)
						return
					}
				}
				_, _ = w.Write(result)
			}))
			defer server.Close()
			card := &sdk.AgentCard{}
			if err := a.Advertise(card, server.URL+a.ViewPath); err != nil {
				t.Fatal(err)
			}
			c, err := a.NewClient(server.Client(), card)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := protojson.Marshal(request())
			_, err = (a.BorrowTool{Client: c}).Execute(context.Background(), b)
			want := a.ErrInvalidParams
			if mode == "inactive" {
				want = a.ErrInactive
			}
			if !errors.Is(err, want) {
				t.Fatalf("Execute() error = %v, want %v", err, want)
			}
		})
	}
}
