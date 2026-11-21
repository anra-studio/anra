package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/flow/clbr"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	bv "github.com/anra-studio/anra/pkg/model/view"
	"github.com/scbizu/tape-go/pkg/runner"
	entry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"google.golang.org/protobuf/proto"
)

type peerScenario struct {
	root string
	ctx  context.Context

	peerTape  *bbolt.Bbolt
	coreTape  *bbolt.Bbolt
	service   *borrow.Service
	server    *httptest.Server
	card      *sdk.AgentCard
	client    *a2a.Client
	ordinary  *a2aclient.Client
	request   *pb.BorrowRequest
	decision  *pb.Decision
	firstRead *rpcpb.ReadResponse

	grants        *grantFixture
	jsonlSidecar  bool
	pendingWriter *borrow.Service
	pending       atomic.Bool

	wireCode     string
	findingCount atomic.Uint64
	clock        atomic.Int64
}

func (s *peerScenario) openTape(name, role string) (*bbolt.Bbolt, error) {
	return openFixtureTape(s.ctx, s.root, name, role)
}

func (s *peerScenario) setupPeer() error {
	s.request = &pb.BorrowRequest{
		RequestId: "research-request",
		Purpose:   "fix storage concurrency",
		Query:     "bbolt",
	}

	var err error
	s.peerTape, err = s.openTape("researcher", "researcher-local")
	if err != nil {
		return err
	}
	for _, finding := range []string{"bbolt requires a single writer", "concurrent updates need version checks"} {
		if err := s.appendFinding(finding); err != nil {
			return err
		}
	}
	return s.startPeer()
}

func (s *peerScenario) appendFinding(text string) error {
	turn, err := beginFixtureTurn(s.ctx, s.peerTape, "researcher-local")
	if err != nil {
		return err
	}

	_, err = s.peerTape.Store(owner.WithOwnerId(s.ctx, "researcher-local"), entry.NewEntry(entry.WithEntryKind(entry.EntryUser), entry.WithEntryOwner("researcher-local"), entry.WithEntryContent(text)))
	if err != nil {
		return err
	}

	if _, err = finishFixtureTurn(s.ctx, s.peerTape, "researcher-local", turn); err != nil {
		return err
	}

	s.findingCount.Add(1)

	return nil
}

func (s *peerScenario) selection() borrow.Selection {
	return borrow.Selection{
		Status: a2a.Granted,
		TTL:    time.Hour,
	}
}

func (s *peerScenario) startPeer() error {
	var err error

	sourceAgent, err := agent.New("researcher-local", s.peerTape)
	if err != nil {
		return err
	}

	source := sourceAgent.Source()
	if err := sourceAgent.Shutdown(s.ctx); err != nil {
		return err
	}
	s.grants, err = openGrantFixture(s.ctx, filepath.Join(s.root, "peer.lease.db"), source, "researcher-local", s.peerTape, func() time.Time {
		return time.Unix(0, s.clock.Load())
	}, s.jsonlSidecar)
	if err != nil {
		return err
	}

	s.service, err = s.grants.newService(
		borrow.Config{
			Source: source,
			Tape:   s.grants.tape,

			Views: recoveredViews{
				Tape:  s.peerTape,
				Owner: "researcher-local",
				All:   true,
			},
			Exporter: bv.TextExporter{},

			Review: func(_ context.Context, peer string, _ *pb.BorrowRequest) (borrow.Selection, error) {
				if peer != "borrower" {
					return borrow.Selection{
						Status: a2a.Denied,
					}, nil
				}
				if s.pending.Load() {
					return borrow.Selection{
						Status: a2a.Pending,
					}, nil
				}
				return s.selection(), nil
			},
		},
	)
	if err != nil {
		return err
	}

	s.coreTape, err = s.openTape("messaging", "researcher-core")
	if err != nil {
		return err
	}

	lender, err := agent.New("researcher-core", s.coreTape, agent.WithLLM(modelFunc(
		func(context.Context, runner.Request) (runner.Response, error) {
			return runner.Response{
				Output: "research response",
			}, nil
		},
	)))
	if err != nil {
		return err
	}
	executor, err := a2a.NewExecutor(lender, &clbr.Service{})
	if err != nil {
		return err
	}
	s.server = httptest.NewUnstartedServer(nil)
	base := "http://" + s.server.Listener.Addr().String()
	card := &sdk.AgentCard{
		Name:        "researcher",
		Description: "storage specialist",
		Version:     "0.1",

		SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)},

		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},

		Skills: []sdk.AgentSkill{{
			ID:          "storage",
			Name:        "Storage",
			Description: "bbolt storage",
			Tags:        []string{"bbolt"},
		}},
	}
	if err := a2a.Advertise(card, base+a2a.ViewPath); err != nil {
		return err
	}
	handler, err := trustedPeerHandler(card, executor, s.service)
	if err != nil {
		return err
	}
	s.server.Config.Handler = handler
	s.server.Start()
	s.card, err = a2a.Discover(s.ctx, s.server.Client(), base+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		return err
	}
	s.client, err = s.clientFor("borrower")
	return err
}

func (s *peerScenario) stopPeer() error {
	if s.coreTape != nil {
		if err := s.coreTape.Close(); err != nil {
			return err
		}
		s.coreTape = nil
	}
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	var err error
	s.service = nil
	if s.grants != nil {
		err = errors.Join(err, s.grants.close())
		s.grants = nil
	}
	if s.peerTape != nil {
		err = errors.Join(err, s.peerTape.Close())
		s.peerTape = nil
	}
	return err
}

func (s *peerScenario) restartPeer() error {
	if err := s.stopPeer(); err != nil {
		return err
	}
	var err error
	s.peerTape, err = s.openTape("researcher", "researcher-local")
	if err != nil {
		return err
	}
	return s.startPeer()
}

func (s *peerScenario) httpClient(peer string) *http.Client {
	client := a2a.TrustedHTTPClient(peer)
	client.Timeout = 5 * time.Second
	return client
}

func (s *peerScenario) clientFor(peer string) (*a2a.Client, error) {
	return a2a.NewClient(s.httpClient(peer), s.card)
}

func (s *scenario) sendMessage(text string) error {
	var err error
	s.peer.ordinary, err = newA2AClient(s.ctx, s.peer.httpClient("borrower"), s.peer.card)
	if err != nil {
		return err
	}

	result, err := s.peer.ordinary.SendMessage(
		s.ctx,
		&sdk.SendMessageRequest{
			Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart(text)),
		},
	)
	if err != nil {
		return err
	}
	message, ok := result.(*sdk.Message)
	if !ok || len(message.Parts) != 1 {
		return fmt.Errorf("unexpected peer message: %#v", result)
	}
	s.answer = message.Parts[0].Text()
	return nil
}

func (s *peerScenario) streamMessage(text string) error {
	var outputs []string

	for event, err := range s.ordinary.SendStreamingMessage(
		s.ctx,
		&sdk.SendMessageRequest{
			Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart(text)),
		},
	) {

		if err != nil {
			return err
		}
		if message, ok := event.(*sdk.Message); ok {
			for _, part := range message.Parts {
				outputs = append(outputs, part.Text())
			}
		}
	}
	if strings.Join(outputs, "") != "research response" {
		return fmt.Errorf("streamed output = %q", outputs)
	}
	return nil
}

func (s *peerScenario) borrow() error {
	var err error
	s.decision, err = s.client.Borrow(s.ctx, s.request)
	return err
}

func decisionStatus(name string) pb.DecisionStatus {
	switch name {
	case "granted":
		return a2a.Granted
	case "denied":
		return a2a.Denied
	case "pending":
		return a2a.Pending
	}
	return pb.DecisionStatus_DECISION_STATUS_UNSPECIFIED
}

func (s *peerScenario) decisionIs(want string) error {
	if s.decision == nil || s.decision.Status != decisionStatus(want) {
		return fmt.Errorf("decision = %v, want %s", s.decision, want)
	}

	if want != "granted" && s.decision.Lease != nil {
		return fmt.Errorf("%s decision disclosed a view", want)
	}
	return nil
}

// exportedTextIs checks the complete text payload; structured events must
// retain a single ordinary text part rather than a flattened derivative.
func exportedTextIs(e *pb.ExportedEntry, want string) bool {
	if e == nil || e.Payload == nil {
		return false
	}
	switch e.Payload.MediaType {
	case a2a.TextMediaType:
		var text string
		return json.Unmarshal(e.Payload.JsonData, &text) == nil && text == want
	case a2a.ContextMediaType:
		var content ae.Content
		if json.Unmarshal(e.Payload.JsonData, &content) != nil || len(content.Parts) != 1 || content.Parts[0] == nil {
			return false
		}
		part := content.Parts[0]
		return !part.Thought && part.Text == want && part.FunctionCall == nil && part.FunctionResponse == nil
	default:
		return false
	}
}

func (s *peerScenario) readFirst() error {
	var err error
	s.firstRead, err = s.client.Read(s.ctx, &rpcpb.ReadRequest{
		LeaseId: s.decision.Lease.Id,
	})
	if err != nil {
		return err
	}
	entries := s.firstRead.Quote.Entries
	if len(entries) != 2 || !exportedTextIs(entries[0], "bbolt requires a single writer") || !exportedTextIs(entries[1], "concurrent updates need version checks") {
		return fmt.Errorf("unexpected authorized content: %v", s.firstRead)
	}
	return nil
}

func (s *peerScenario) retryGrant() error {
	got, err := s.client.Borrow(s.ctx, s.request)
	if err != nil {
		return err
	}
	if !proto.Equal(got, s.decision) {
		return fmt.Errorf("retry changed decision: got %v, want %v", got, s.decision)
	}
	return nil
}

func (s *peerScenario) finishSnapshot() error {
	response, err := s.client.Read(s.ctx, &rpcpb.ReadRequest{
		LeaseId: s.decision.Lease.Id,
	})
	if err != nil {
		return err
	}
	if !proto.Equal(response.Quote, s.firstRead.Quote) {
		return fmt.Errorf("original snapshot was not preserved: %v", response)
	}
	return nil
}

func (s *peerScenario) addFindings(count int) error {
	for int(s.findingCount.Load()) < count {
		if err := s.appendFinding(fmt.Sprintf("finding %d", s.findingCount.Load()+1)); err != nil {
			return err
		}
	}
	return nil
}

func (s *peerScenario) readAll(count int) error {
	quote, err := s.client.ReadView(s.ctx, s.decision.Lease)
	if err != nil {
		return err
	}
	if len(quote.Entries) != count || quote.Provenance.LeaseId != s.decision.Lease.Id || !proto.Equal(quote.Provenance.Ref, s.decision.Lease.Ref) {
		return fmt.Errorf("authorized Quote lost entries or provenance: %v", quote)
	}
	for i, e := range quote.Entries {
		if e.Seq != s.decision.Lease.Ref.Scopes[i].SeqS {
			return fmt.Errorf("entry %d has sequence %s", i, e.Seq)
		}
	}
	return nil
}

func (s *peerScenario) retryChangedBorrow() error {
	changed := proto.Clone(s.request).(*pb.BorrowRequest)
	changed.Purpose = "a different purpose"
	changed.Query = "a different query"
	changed.Bounds = &pb.EntryRange{
		SeqS: "1",
		SeqE: "2",
	}
	got, err := s.client.Borrow(s.ctx, changed)
	if err != nil {
		return err
	}
	if !proto.Equal(got, s.decision) {
		return fmt.Errorf("retry changed decision: got %v, want %v", got, s.decision)
	}
	return nil
}

func expectError(got, want error) error {
	if !errors.Is(got, want) {
		return fmt.Errorf("error = %v, want %v", got, want)
	}
	return nil
}

func (s *peerScenario) checkIntruder() error {
	intruder, err := s.clientFor("intruder")
	if err != nil {
		return err
	}
	_, err = intruder.Get(s.ctx, s.request.RequestId)
	if err := expectError(err, a2a.ErrUnavailable); err != nil {
		return fmt.Errorf("lookup: %w", err)
	}

	_, err = intruder.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId: s.decision.Lease.Id,
		},
	)

	if err := expectError(err, a2a.ErrUnavailable); err != nil {
		return fmt.Errorf("read: %w", err)
	}

	if err := expectError(intruder.Release(s.ctx, s.decision.Lease.Id), a2a.ErrUnavailable); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	// Failed release must leave the authorized borrower's access intact.

	_, err = s.client.ReadView(s.ctx, s.decision.Lease)

	return err
}

func (s *peerScenario) checkDenied() error {
	intruder, err := s.clientFor("intruder")
	if err != nil {
		return err
	}
	d, err := intruder.Borrow(s.ctx, s.request)
	if err != nil {
		return err
	}

	if d.Status != a2a.Denied || d.Lease != nil {
		return fmt.Errorf("unauthorized decision = %v", d)
	}
	return nil
}

func (s *peerScenario) closeGrant(closure string) error {
	switch closure {
	case "released":
		if err := s.client.Release(s.ctx, s.decision.Lease.Id); err != nil {
			return err
		}
		return s.client.Release(s.ctx, s.decision.Lease.Id)

	case "revoked":
		return s.service.Revoke(s.ctx, "borrower", s.decision.Lease.Id)

	case "expired":
		s.clock.Add(int64(2 * time.Hour))
		return nil
	}
	return fmt.Errorf("unknown closure %s", closure)
}

func (s *peerScenario) unavailable() error {
	_, err := s.client.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId: s.decision.Lease.Id,
		},
	)

	return expectError(err, a2a.ErrUnavailable)
}

func (s *peerScenario) recoverGrant() error {
	d, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if !proto.Equal(d, s.decision) {
		return fmt.Errorf("recorded grant changed: %v", d)
	}
	return s.retryGrant()
}

func (s *peerScenario) recoverDecision(want string) error {
	var err error
	s.decision, err = s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if err := s.decisionIs(want); err != nil {
		return err
	}
	return s.retryGrant()
}

func (s *peerScenario) completePending(outcome string) error {
	if outcome == "granted" {
		_, err := s.service.Approve(s.ctx, "borrower", s.request.RequestId, time.Hour)
		return err
	}
	_, err := s.service.Reject(s.ctx, "borrower", s.request.RequestId)
	return err
}

func (s *peerScenario) rejectApproval(ttl time.Duration, want error) error {
	before, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if _, err := s.service.Approve(s.ctx, "borrower", s.request.RequestId, ttl); !errors.Is(err, want) {
		return fmt.Errorf("Approve returned %v, want %v", err, want)
	}
	after, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if !proto.Equal(before, after) {
		return errors.New("failed approval changed the request outcome")
	}
	return nil
}

func (s *peerScenario) rememberPendingDecision() error {
	var err error
	s.pendingWriter, err = s.grants.newService(
		borrow.Config{
			Source: s.grants.source,
			Tape:   s.grants.tape,
			Views: recoveredViews{
				Tape:  s.peerTape,
				Owner: "researcher-local",
				All:   true,
			},
		},
	)
	if err != nil {
		return err
	}
	d, err := s.pendingWriter.Get(s.ctx, "borrower", s.request.RequestId)
	if err != nil {
		return err
	}
	if d.Status != borrow.Pending {
		return errors.New("expected a persisted pending decision")
	}
	return nil
}

func (s *peerScenario) rejectStaleDecision() error {
	before, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if s.pendingWriter == nil {
		return errors.New("missing competing borrowing service")
	}
	_, head, err := s.grants.decisions(s.ctx)
	if err != nil {
		return err
	}
	_, err = s.pendingWriter.Reject(s.ctx, "borrower", s.request.RequestId)
	if !errors.Is(err, borrow.ErrConflict) {
		return fmt.Errorf("competing resolution returned %v, want conflict", err)
	}
	replay, err := s.pendingWriter.Borrow(s.ctx, "borrower", s.request)
	if err != nil {
		return err
	}
	if !proto.Equal(replay, before) {
		return errors.New("duplicate borrowing changed the completed decision")
	}
	_, afterHead, err := s.grants.decisions(s.ctx)
	if err != nil {
		return err
	}
	if afterHead != head {
		return errors.New("rejected and duplicate operations appended sidecar records")
	}
	after, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if !proto.Equal(before, after) {
		return fmt.Errorf("conflicting writes changed decision: %v -> %v", before, after)
	}
	return nil
}

func (s *peerScenario) checkAccess(outcome string) error {
	if outcome == "denied" {
		return s.decisionIs(outcome)
	}

	quote, err := s.client.ReadView(s.ctx, s.decision.Lease)
	if err != nil {
		return err
	}
	if len(quote.Entries) != 2 {
		return fmt.Errorf("approved context missing: %v", quote)
	}
	return nil
}

func (s *peerScenario) wireRequest(problem string) error {
	body := `{"requestId":"wire-request","purpose":"research","query":"bbolt"}`
	method, identity, extension := "Borrow", "borrower", a2a.ExtensionURI
	switch problem {
	case "no identity":
		identity = ""
	case "no extension":
		extension = ""
	case "missing purpose":
		body = `{"requestId":"wire-request","query":"bbolt"}`
	case "reversed bounds":
		body = `{"requestId":"wire-request","purpose":"research","query":"bbolt","bounds":{"seqS":"9","seqE":"2"}}`
	case "空 lease ID":
		method, body = "Read", `{}`
	default:
		return fmt.Errorf("unknown wire problem %s", problem)
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.server.URL+a2a.ViewPath+"/"+method, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	if identity != "" {
		req.Header.Set(a2a.PeerIdentityHeader, identity)
	}
	if extension != "" {
		req.Header.Set(a2a.ExtensionHeader, extension)
	}
	res, err := s.server.Client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	var failure struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &failure); err != nil {
		return fmt.Errorf("HTTP %d: %s: %w", res.StatusCode, data, err)
	}
	if res.StatusCode < 400 || failure.Code == "" {
		return fmt.Errorf("request unexpectedly accepted: HTTP %d: %s", res.StatusCode, data)
	}
	s.wireCode = failure.Code
	return nil
}
