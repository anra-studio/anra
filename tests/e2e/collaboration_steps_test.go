package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anra-studio/anra/pkg/flow/clbr"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	cp "github.com/anra-studio/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	"github.com/anra-studio/anra/pkg/discovery"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/anra-studio/anra/pkg/model/anchor"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	bv "github.com/anra-studio/anra/pkg/model/view"
	"github.com/anra-studio/anra/pkg/registry"
	"github.com/cucumber/godog"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"google.golang.org/adk/model"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type collaborationNode struct {
	registration *discovery.Registration

	a *agent.Anra

	tape   *bbolt.Bbolt
	grants *grantFixture

	server *httptest.Server

	card *sdk.AgentCard

	calls atomic.Int32
}
type heldResult struct {
	result clbr.Result

	err error
}
type collaborationScenario struct {
	s *scenario

	nodes map[string]*collaborationNode

	directory *httptest.Server

	registry *discovery.Client

	registryStore *registry.Registry

	gate *modelGate

	held chan heldResult

	heldCancel context.CancelFunc

	busy clbr.Result

	originalRef *pb.Ref

	decision *pb.Decision

	originalBytes []byte

	now atomic.Int64
}

func initializeCollaboration(sc *godog.ScenarioContext, s *scenario) {
	c := &collaborationScenario{
		s: s,
	}

	sc.Before(
		func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			*c = collaborationScenario{
				s:     s,
				nodes: map[string]*collaborationNode{},
			}

			c.now.Store(time.Now().UnixNano())

			return ctx, nil
		},
	)

	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		return ctx, c.close()
	})

	sc.Step(`^a collaboration agent$`, c.setup)
	sc.Step(`^generated collaboration metadata roundtrips through A2A$`, c.protobufRoundtrip)
	sc.Step(`^the discovered agent streams a standard A2A answer$`, c.streamAnswer)
	sc.Step(`^collaboration rejects request metadata with missing root before reasoning$`, c.rejectRequestMetadata)
	sc.Step(`^A2A rejects invalid caller identities before reasoning$`, c.rejectCallerIdentity)
	sc.Step(`^Alice has completed a shareable answer$`, c.publishFinding)
	sc.Step(`^Alice is held inside its model call$`, c.hold)
	sc.Step(`^Bob asks the occupied Alice for reasoning$`, c.askBusy)
	sc.Step(`^the response is BUSY without another turn or model call$`, c.checkBusyAdmission)
	sc.Step(`^BUSY reveals no view or private control fields$`, c.checkBusyPrivacy)
	sc.Step(`^Bob borrows the BUSY view while Alice remains occupied$`, c.borrowBusyView)
	sc.Step(`^the view grant contains exactly the completed context window$`, c.checkBorrowedWindow)
	sc.Step(`^Alice finishes and commits a newer context window$`, c.publishNewerWindow)
	sc.Step(`^Alice restarts with the same source and the grant remains readable$`, c.restartAndCheckGrant)
	sc.Step(`^another trusted requester gets its own grant but cannot read or release Bob's grant$`, c.checkOtherRequester)
	sc.Step(`^a guessed reference cannot obtain private or control entries$`, c.rejectGuessedRef)
	sc.Step(`^view requests reject a changed reference and incompatible bounds$`, c.rejectChangedView)
	sc.Step(`^Alice's cancelled turn closes and its partial history is excluded$`, c.cancelAndContinue)
	sc.Step(`^simultaneous callers admit exactly one turn$`, c.raceAdmission)
	sc.Step(`^cyclic peer reasoning completes using BUSY and borrowed findings$`, c.cycle)
	sc.Step(`^independent roots consult one another without blocking$`, c.independentRoots)
	sc.Step(`^shutdown drains active reasoning before storage closes$`, c.checkShutdown)
	sc.Step(`^an unresolved terminal append fences further reasoning$`, c.checkTerminalFailure)
	sc.Step(`^startup rejects a tape containing overlapping starts$`, c.rejectOverlappingTurns)
	sc.Step(`^generic A2A callers receive an unaccepted response with no hint$`, c.checkGenericBusy)
}

func (c *collaborationScenario) close() error {
	if c.heldCancel != nil {
		c.heldCancel()
	}
	if c.gate != nil {
		c.gate.unblock()
	}
	var err error
	for _, n := range c.nodes {
		err = errors.Join(err, n.close())
	}
	if c.directory != nil {
		c.directory.Close()
	}
	if c.registryStore != nil {
		err = errors.Join(err, c.registryStore.Close())
	}
	return err
}

// stopRuntime leaves the Tape open for scenarios that inject persisted history.
func (n *collaborationNode) stopRuntime() error {
	if n.registration != nil {
		n.registration.Close()
		n.registration = nil
	}
	if n.server != nil {
		n.server.Close()
		n.server = nil
	}
	if n.a != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := n.a.Shutdown(ctx); err != nil {
			return err
		}
		n.a = nil
	}
	if n.grants != nil {
		err := n.grants.close()
		n.grants = nil
		return err
	}
	return nil
}

func (n *collaborationNode) close() error {
	if err := n.stopRuntime(); err != nil {
		return err
	}
	if n.tape != nil {
		err := n.tape.Close()
		n.tape = nil
		return err
	}
	return nil
}

func (c *collaborationScenario) publishFinding() error {
	result, err := c.send("bob", "alice", "publish")
	if err != nil {
		return err
	}

	if result.Status != cp.Status_STATUS_COMPLETED || result.Answer != "published finding" {
		return fmt.Errorf("unexpected view answer: %+v", result)
	}

	ref, err := c.nodes["alice"].a.View(c.s.ctx, "bob", nil)

	if err != nil || ref == nil {
		return fmt.Errorf("missing view: %v", err)
	}

	c.originalRef = ref

	return nil
}

func (c *collaborationScenario) askBusy() error {
	var err error
	c.busy, err = c.send("bob", "alice", "callback")
	return err
}

func (c *collaborationScenario) checkBusyAdmission() error {
	n := c.nodes["alice"]

	before, err := c.readEntries(n)
	if err != nil {
		return err
	}

	if c.busy.Status != cp.Status_STATUS_BUSY || c.busy.Answer != "" {
		return fmt.Errorf("not a BUSY outcome: %+v", c.busy)
	}

	result, err := c.send("bob", "alice", "different callback")

	if err != nil || result.Status != cp.Status_STATUS_BUSY {
		return fmt.Errorf("repeat busy: %+v %v", result, err)
	}

	after, err := c.readEntries(n)
	if err != nil {
		return err
	}

	expected := int32(1)

	if c.originalRef != nil {
		expected++
	}

	if len(before) != len(after) || n.calls.Load() != expected {
		return fmt.Errorf("busy request changed tape/model: %d/%d calls=%d", len(before), len(after), n.calls.Load())
	}

	users := 0

	for _, e := range after {
		if e.GetKind() == entry.EntryUser {
			users++
		}
	}

	if users != int(expected) {
		return fmt.Errorf("rejected request appended a user entry: %d", users)
	}

	return nil
}

func (c *collaborationScenario) checkBusyPrivacy() error {
	if c.busy.View != nil {
		return errors.New("view hint disclosed before any answer completed")
	}

	raw, _ := json.Marshal(c.busy)

	if strings.Contains(string(raw), "epoch") || strings.Contains(string(raw), "turnId") {
		return errors.New("private control data disclosed")
	}

	return nil
}

func (c *collaborationScenario) borrowBusyView() error {
	if c.busy.View == nil {
		return errors.New("missing busy view")
	}

	client, err := c.viewClient("bob")
	if err != nil {
		return err
	}

	req := &pb.BorrowRequest{
		RequestId: "busy-borrow",
		Purpose:   "complete research",
		Query:     "finding",
		View:      c.busy.View,
	}

	c.decision, err = c.borrowGrantedView(client, req)
	if err != nil {
		return err
	}

	if err := c.checkView(client, c.decision.Lease); err != nil {
		return err
	}

	retry, err := client.Borrow(c.s.ctx, req)

	if err != nil || !proto.Equal(retry, c.decision) {
		return fmt.Errorf("unstable borrow retry: %v %v", retry, err)
	}

	select {
	case result := <-c.held:
		return fmt.Errorf("owner finished before borrowing: %+v", result)
	default:
	}

	return nil
}

func (c *collaborationScenario) checkBorrowedWindow() error {
	client, err := c.viewClient("bob")
	if err != nil {
		return err
	}

	return c.checkView(client, c.decision.Lease)
}

func (c *collaborationScenario) publishNewerWindow() error {
	if err := c.finishHold(); err != nil {
		return err
	}

	result, err := c.send("bob", "alice", "new answer")

	if err != nil || result.Status != cp.Status_STATUS_COMPLETED {
		return fmt.Errorf("new answer: %+v %v", result, err)
	}

	return nil
}

func (c *collaborationScenario) restartAndCheckGrant() error {
	source := c.nodes["alice"].a.Source()
	if err := c.replaceAlice(c.model()); err != nil {
		return err
	}

	if !proto.Equal(source, c.nodes["alice"].a.Source()) {
		return errors.New("restart changed source")
	}

	return c.checkBorrowedWindow()
}

func (c *collaborationScenario) checkOtherRequester() error {
	client, err := c.viewClient("mallory")
	if err != nil {
		return err
	}

	own, err := c.borrowGrantedView(
		client,
		&pb.BorrowRequest{
			RequestId: "copied-hint",
			Purpose:   "request own view grant",
			Query:     "finding",
			View:      c.originalRef,
		},
	)

	if err != nil || own.Lease.Id == c.decision.Lease.Id {
		return fmt.Errorf("missing separate requester-bound grant: %v %v", own, err)
	}

	quote, err := client.ReadView(c.s.ctx, own.Lease)
	if err != nil {
		return err
	}

	bob, err := c.viewClient("bob")
	if err != nil {
		return err
	}

	original, err := bob.ReadView(c.s.ctx, c.decision.Lease)
	if err != nil {
		return err
	}

	if len(quote.Entries) != 2 || len(original.Entries) != 2 || !proto.Equal(quote.Entries[0], original.Entries[0]) || !proto.Equal(quote.Entries[1], original.Entries[1]) {
		return errors.New("separate grants did not return the same immutable view")
	}

	if _, err = client.ReadView(c.s.ctx, c.decision.Lease); !errors.Is(err, a2a.ErrUnavailable) {
		return fmt.Errorf("other requester read: %v", err)
	}

	if err = client.Release(c.s.ctx, c.decision.Lease.Id); !errors.Is(err, a2a.ErrUnavailable) {
		return fmt.Errorf("other requester release: %v", err)
	}

	return nil
}

func (c *collaborationScenario) rejectGuessedRef() error {
	client, err := c.viewClient("mallory")
	if err != nil {
		return err
	}

	source := c.nodes["alice"].a.Source()

	for _, seq := range []string{"2", "3", "999"} {
		d, err := client.Borrow(
			c.s.ctx,
			&pb.BorrowRequest{
				RequestId: "guess-" + seq,
				Purpose:   "guess",
				Query:     "private",
				View: &pb.Ref{
					Source: source,
					Scopes: []*pb.EntryRange{{
						SeqS: "1",
						SeqE: seq,
					}},
				},
			},
		)
		if err != nil {
			return err
		}

		if d.Status != a2a.Denied || d.Lease != nil {
			return errors.New("guessed reference granted")
		}
	}

	return nil
}

func (c *collaborationScenario) rejectChangedView() error {
	client, err := c.viewClient("bob")
	if err != nil {
		return err
	}

	changed := anchor.CloneRef(c.busy.View)

	changed.Scopes[0].SeqS = "1"

	if _, err := client.Borrow(
		c.s.ctx,
		&pb.BorrowRequest{
			RequestId: "busy-borrow",
			Purpose:   "complete research",
			Query:     "finding",
			View:      changed,
		},
	); !errors.Is(err, a2a.ErrInvalidParams) {
		return fmt.Errorf("changed reference: %v", err)
	}

	req := &pb.BorrowRequest{
		RequestId: "wrong-source",
		Purpose:   "research",
		Query:     "finding",
		View:      anchor.CloneRef(c.busy.View),
	}

	req.View.Source.TapeId = "other-incarnation"

	d, err := client.Borrow(c.s.ctx, req)

	if err != nil || d.Status != a2a.Denied {
		return fmt.Errorf("wrong source granted: %v %v", d, err)
	}

	req.RequestId = "expanded-scope"

	req.View = anchor.CloneRef(c.busy.View)

	req.View.Scopes[0].SeqS = "1"

	d, err = client.Borrow(c.s.ctx, req)

	if err != nil || d.Status != a2a.Denied {
		return fmt.Errorf("expanded context scope granted: %v %v", d, err)
	}

	req.RequestId = "wrong-bounds"

	req.View = c.busy.View

	req.Bounds = &pb.EntryRange{
		SeqS: "1",
		SeqE: "2",
	}

	d, err = client.Borrow(c.s.ctx, req)

	if err != nil || d.Status != a2a.Denied {
		return fmt.Errorf("incompatible bounds granted: %v %v", d, err)
	}

	req.RequestId = "malformed"

	req.Bounds = nil

	req.View = anchor.CloneRef(c.busy.View)

	req.View.Scopes[0].SeqE = "01"

	if _, err := client.Borrow(c.s.ctx, req); !errors.Is(err, a2a.ErrInvalidParams) {
		return fmt.Errorf("malformed ref: %v", err)
	}

	return nil
}

func (c *collaborationScenario) checkShutdown() error {
	if err := c.hold(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(c.s.ctx, 5*time.Second)

	defer cancel()

	n := c.nodes["alice"]

	if err := n.a.Shutdown(ctx); err != nil {
		return err
	}

	if err := n.a.Shutdown(ctx); err != nil {
		return err
	}

	if _, err := n.a.Run(c.s.ctx, "too late"); !errors.Is(err, agent.ErrClosing) {
		return fmt.Errorf("shutdown admitted work: %v", err)
	}

	entries, err := c.readEntries(n)
	if err != nil {
		return err
	}

	value, err := ae.FromTape(entries[len(entries)-1])
	if err != nil {
		return err
	}
	last, err := anchor.Decode(value)
	if err != nil {
		return err
	}

	if last == nil || last.Type != anchor.TurnClosed || last.Outcome != "cancelled" {
		return fmt.Errorf("shutdown left an open turn: %+v", last)
	}

	return n.tape.Close()
}

func (c *collaborationScenario) checkTerminalFailure() error {
	if err := c.hold(); err != nil {
		return err
	}

	n := c.nodes["alice"]

	if err := n.tape.Close(); err != nil {
		return err
	}

	c.gate.unblock()

	select {
	case result := <-c.held:
		if result.err == nil {
			return errors.New("storage failure acknowledged success")
		}
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
	}

	before := n.calls.Load()

	_, err := n.a.Run(c.s.ctx, "must remain fenced")

	if !errors.Is(err, agent.ErrFenced) || n.calls.Load() != before {
		return fmt.Errorf("unresolved close reopened admission: %v", err)
	}

	return nil
}

func (c *collaborationScenario) rejectOverlappingTurns() error {
	n := c.nodes["alice"]

	if err := n.stopRuntime(); err != nil {
		return err
	}

	payloads := []string{
		`{"schema":"anra.dev/anchor/v1","type":"turn.started","turnId":"one","ownerEpoch":"old"}`,
		`{"schema":"anra.dev/anchor/v1","type":"turn.started","turnId":"two","ownerEpoch":"old"}`,
	}

	for _, raw := range payloads {
		if _, err := n.tape.Store(owner.WithOwnerId(c.s.ctx, "alice"), entry.NewAnchor(
			entry.Seq{},
			"alice",
			entry.AnchorKindCustom,
			[]byte(raw),
		)); err != nil {
			return err
		}
	}

	if err := n.tape.Close(); err != nil {
		return err
	}
	n.tape = nil

	err := c.startNode("alice", c.model())
	if err == nil || !strings.Contains(err.Error(), "agent: replay") {
		return fmt.Errorf("startup did not reject the corrupt history during replay: %v", err)
	}
	if c.nodes["alice"].server != nil {
		return errors.New("a node with corrupt history started serving")
	}

	return nil
}

func (c *collaborationScenario) checkGenericBusy() error {
	client, err := c.a2aClient("bob", "alice")
	if err != nil {
		return err
	}

	response, err := client.SendMessage(
		c.s.ctx,
		&sdk.SendMessageRequest{
			Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("generic callback")),
		},
	)
	if err != nil {
		return err
	}

	message, ok := response.(*sdk.Message)

	if !ok || len(message.Metadata) != 0 || len(message.Extensions) != 0 || len(message.Parts) != 1 || !strings.Contains(message.Parts[0].Text(), "request was not accepted") {
		return fmt.Errorf("generic caller received private/ambiguous BUSY: %+v", response)
	}

	return nil
}

func (c *collaborationScenario) setup() error {
	store, err := registry.Open(
		registry.Config{
			Path:             filepath.Join(c.s.root, "collaboration-registry.db"),
			HeartbeatTimeout: time.Minute,
		},
	)
	if err != nil {
		return err
	}
	c.registryStore = store
	handler, err := registry.NewHandler(store)
	if err != nil {
		return err
	}
	c.directory = httptest.NewServer(handler)
	c.registry, err = discovery.NewClient(c.directory.URL)
	if err != nil {
		return err
	}
	return c.startNode("alice", c.model())
}

func (c *collaborationScenario) model() modelFunc {
	return modelFunc(
		func(ctx context.Context, r runner.Request) (runner.Response, error) {
			input := r.Messages[len(r.Messages)-1].Text

			if input == "hold" {
				if err := c.gate.block(ctx); err != nil {
					return runner.Response{}, err
				}
			}
			if input == "publish" {
				return runner.Response{
					Output: "published finding",
				}, nil
			}

			return runner.Response{
				Output: "answer: " + input,
			}, nil
		},
	)
}

func (c *collaborationScenario) startNode(name string, m model.LLM) error {
	n := &collaborationNode{}
	c.nodes[name] = n
	var err error
	n.tape, err = openBboltTape(owner.WithOwnerId(c.s.ctx, name), "default", filepath.Join(c.s.root, "collab-"+name+".db"))
	if err != nil {
		return err
	}

	opts := []agent.Option{agent.WithLLM(
		countedModel{
			LLM:   m,
			calls: &n.calls,
		},
	), agent.WithRegistry(c.registry, a2a.TrustedHTTPClient(name))}

	n.a, err = agent.New(name, n.tape, opts...)
	if err != nil {
		return err
	}

	n.grants, err = openGrantFixture(
		c.s.ctx,
		filepath.Join(c.s.root, "collab-"+name+".lease.db"),
		n.a.Source(),
		name,
		n.tape,
		func() time.Time {
			return time.Unix(0, c.now.Load())
		},
		false,
	)
	if err != nil {
		return err
	}
	borrowing, err := n.grants.newService(
		borrow.Config{
			Source:   n.a.Source(),
			Tape:     n.grants.tape,
			Views:    n.a,
			Exporter: bv.ContextExporter{},
		},
	)
	if err != nil {
		return err
	}
	n.server = httptest.NewUnstartedServer(nil)
	base := "http://" + n.server.Listener.Addr().String()

	n.card = &sdk.AgentCard{
		Name:                name,
		Version:             "1",
		Description:         "collaboration fixture",
		SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"text/plain"},
		DefaultOutputModes:  []string{"text/plain"},
	}

	a2a.AdvertiseCollaboration(n.card)
	if err = a2a.AdvertiseViews(n.card, base+a2a.ViewPath); err != nil {
		return err
	}
	exec, err := a2a.NewExecutor(n.a, &clbr.Service{})
	if err != nil {
		return err
	}
	handler, err := trustedPeerHandler(n.card, exec, borrowing)
	if err != nil {
		return err
	}

	n.server.Config.Handler = handler

	n.server.Start()

	n.registration, err = c.registry.Start(
		c.s.ctx,
		&pv.Peer{
			Id:          name,
			Address:     base,
			Description: "Collaboration fixture",
			Skill: []*pv.Skill{{
				Id:   "chat",
				Name: "Chat",
			}, {
				Id:   "borrow-context",
				Name: "Borrow context",
			}},
		},
	)

	return err
}

func (c *collaborationScenario) a2aClient(from, to string) (*a2aclient.Client, error) {
	return newA2AClient(c.s.ctx, a2a.TrustedHTTPClient(from), c.nodes[to].card)
}

func (c *collaborationScenario) send(from, to, input string) (clbr.Result, error) {
	return sendPeerMessage(c.s.ctx, from, c.nodes[to].server.URL, input)
}

func sendPeerMessage(ctx context.Context, caller, endpoint, input string) (clbr.Result, error) {
	client, err := messageClient(ctx, caller, endpoint)
	if err != nil {
		return clbr.Result{}, err
	}
	return client.Send(ctx, input)
}

func (c *collaborationScenario) viewClient(peer string) (*a2a.Client, error) {
	return trustedViewClient(peer, c.nodes["alice"].card)
}

func (c *collaborationScenario) borrowGrantedView(client *a2a.Client, req *pb.BorrowRequest) (*pb.Decision, error) {
	decision, err := client.Borrow(c.s.ctx, req)
	if err != nil {
		return nil, err
	}
	if decision.GetStatus() != a2a.Granted || decision.GetLease() == nil {
		return decision, fmt.Errorf("borrow %q did not grant a lease: %v", req.RequestId, decision)
	}
	return decision, nil
}

func (c *collaborationScenario) hold() error {
	c.gate = newModelGate(1)
	results := make(chan heldResult, 1)
	c.held = results
	ctx, cancel := context.WithCancel(c.s.ctx)
	c.heldCancel = cancel
	endpoint := c.nodes["alice"].server.URL

	go func() {
		result, err := sendPeerMessage(ctx, "bob", endpoint, "hold")
		results <- heldResult{
			result: result,
			err:    err,
		}
	}()

	return c.gate.awaitEntry(c.s.ctx)
}

func (c *collaborationScenario) finishHold() error {
	c.gate.unblock()
	select {
	case r := <-c.held:
		if r.err != nil {
			return r.err
		}
		if r.result.Status != cp.Status_STATUS_COMPLETED {
			return fmt.Errorf("held run: %+v", r.result)
		}
		return nil
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
	}
}

func (c *collaborationScenario) readEntries(n *collaborationNode) ([]entry.EntryLike, error) {
	ctx := owner.WithOwnerId(c.s.ctx, n.a.Source().AgentId)
	entries, _, err := readAllEntries(ctx, n.tape)
	return entries, err
}

func (c *collaborationScenario) checkView(client *a2a.Client, v *pb.Lease) error {
	ref := c.originalRef

	if ref == nil || !proto.Equal(v.Ref, ref) {
		return fmt.Errorf("incorrect context window: %v", v)
	}
	quote, err := client.ReadView(c.s.ctx, v)
	if err != nil {
		return err
	}
	if len(quote.Entries) != 2 {
		return errors.New("view did not yield the completed user/assistant exchange")
	}

	for i, expected := range []struct {
		kind string
		text string
	}{{"user", "publish"}, {"assistant", "published finding"}} {

		e := quote.Entries[i]
		if e.Kind != expected.kind || !exportedTextIs(e, expected.text) || e.Payload.MediaType != a2a.ContextMediaType {
			return fmt.Errorf("wrong context entry: %v", e)
		}
	}
	raw, _ := json.Marshal(quote)
	if strings.Contains(string(raw), "ownerEpoch") || strings.Contains(string(raw), "turnId") || strings.Contains(string(raw), "adk:event") {
		return errors.New("private execution metadata leaked")
	}
	if c.originalBytes == nil {
		c.originalBytes = raw
	} else if string(raw) != string(c.originalBytes) {
		return errors.New("view bytes changed after writes/restart")
	}
	return nil
}

func (c *collaborationScenario) raceAdmission() error {
	c.gate = newModelGate(1)
	const count = 12
	start := make(chan struct{})
	results := make(chan heldResult, count)
	ctx := c.s.ctx
	endpoint := c.nodes["alice"].server.URL
	for range count {
		go func() {
			<-start
			result, err := sendPeerMessage(ctx, "bob", endpoint, "hold")
			results <- heldResult{
				result: result,
				err:    err,
			}
		}()
	}
	close(start)
	if err := c.gate.awaitEntry(c.s.ctx); err != nil {
		return err
	}
	for i := 0; i < count-1; i++ {
		select {
		case r := <-results:
			if r.err != nil || r.result.Status != cp.Status_STATUS_BUSY {
				return fmt.Errorf("losing admission: %+v", r)
			}
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
	}
	if c.nodes["alice"].calls.Load() != 1 {
		return errors.New("more than one model admitted")
	}
	c.gate.unblock()
	select {
	case r := <-results:
		if r.err != nil || r.result.Status != cp.Status_STATUS_COMPLETED {
			return fmt.Errorf("winning admission: %+v", r)
		}
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
	}
	entries, err := c.readEntries(c.nodes["alice"])
	if err != nil {
		return err
	}
	if len(entries) != 5 {
		return fmt.Errorf("admission produced %d entries, expected source/start/user/answer/close", len(entries))
	}
	_, err = inspectLifecycle(entries)
	return err
}

func (c *collaborationScenario) cancelAndContinue() error {
	var turn int

	m := modelFunc(
		func(_ context.Context, r runner.Request) (runner.Response, error) {
			turn++

			if turn == 1 {
				return toolCall("bash", map[string]any{"command": "printf unfinished"}), nil
			}

			if turn == 2 {
				return runner.Response{}, context.Canceled
			}

			if len(r.Messages) != 1 || r.Messages[0].Text != "next" {
				return runner.Response{}, fmt.Errorf("aborted history reached next request: %+v", r.Messages)
			}

			return runner.Response{
				Output: "clean next answer",
			}, nil
		},
	)

	if err := c.startNode("bob", m); err != nil {
		return err
	}
	_, err := c.send("alice", "bob", "cancel this work")
	if err == nil {
		return errors.New("cancelled model reported success")
	}
	result, err := c.send("alice", "bob", "next")
	if err != nil || result.Answer != "clean next answer" {
		return fmt.Errorf("next turn: %+v %v", result, err)
	}
	entries, err := c.readEntries(c.nodes["bob"])
	if err != nil {
		return err
	}
	cancelled, completed, calls := 0, 0, 0
	for _, e := range entries {
		value, err := ae.FromTape(e)
		if err != nil {
			return err
		}
		r, err := anchor.Decode(value)
		if err != nil {
			return err
		}
		if r != nil && r.Type == anchor.TurnClosed {
			if r.Outcome == "cancelled" {
				cancelled++
			}
			if r.Outcome == "completed" {
				completed++
			}
		}
		if e.GetKind() == entry.EntryToolCall {
			calls++
		}
	}
	if cancelled != 1 || completed != 1 || calls != 1 {
		return fmt.Errorf("missing durable cancelled work: cancelled=%d complete=%d calls=%d", cancelled, completed, calls)
	}
	return nil
}

func toolCall(name string, args map[string]any) runner.Response {
	data, _ := json.Marshal(args)

	return runner.Response{
		ToolCalls: []runner.ToolCall{{
			ID:        "call-" + name,
			Name:      name,
			Arguments: data,
		}},
	}
}

func lastTool(r runner.Request) (string, map[string]any) {
	last := r.Messages[len(r.Messages)-1]
	if len(last.ToolResults) == 0 {
		return "", nil
	}
	result := last.ToolResults[len(last.ToolResults)-1]
	var data map[string]any
	_ = json.Unmarshal(result.Result, &data)
	return result.Name, data
}

func (c *collaborationScenario) replaceAlice(m model.LLM) error {
	if err := c.nodes["alice"].close(); err != nil {
		return err
	}
	return c.startNode("alice", m)
}

func (c *collaborationScenario) cycle() error {
	if err := c.replaceAlice(modelFunc(
		func(_ context.Context, r runner.Request) (runner.Response, error) {
			name, result := lastTool(r)

			if name == "" {
				return toolCall("send_peer_message", map[string]any{"peer": "bob", "message": "help alice"}), nil
			}

			if result["status"] != "completed" || result["answer"] != "bob used published finding" {
				return runner.Response{}, fmt.Errorf("unexpected Bob result: %v", result)
			}

			return runner.Response{
				Output: "alice combined bob's findings",
			}, nil
		},
	)); err != nil {
		return err
	}

	if err := c.startNode("bob", modelFunc(
		func(_ context.Context, r runner.Request) (runner.Response, error) {
			name, result := lastTool(r)

			switch name {

			case "":
				return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "clarify findings"}), nil

			case "send_peer_message":
				if result["status"] != "busy" || result["view"] == nil {
					return runner.Response{}, fmt.Errorf("callback did not return BUSY/view: %v", result)
				}
				return toolCall("borrow_peer_context", map[string]any{"peer": "alice", "requestId": "cycle-borrow", "purpose": "help alice", "query": "findings"}), nil

			case "borrow_peer_context":
				encoded, _ := json.Marshal(result)
				var observed struct {
					Decision json.RawMessage `json:"decision"`
					Borrowed struct {
						Channel string `json:"channel"`

						Provenance json.RawMessage `json:"provenance"`
					} `json:"borrowed_context"`
				}
				var d pb.Decision
				var p pb.Provenance
				if json.Unmarshal(encoded, &observed) != nil || protojson.Unmarshal(observed.Decision, &d) != nil || protojson.Unmarshal(observed.Borrowed.Provenance, &p) != nil || d.Lease == nil || observed.Borrowed.Channel != "borrowed" || p.Caller != "bob" || p.LeaseId != d.Lease.Id || !proto.Equal(p.Ref, d.Lease.Ref) || p.Ref.Source.AgentId != "alice" || !strings.Contains(string(encoded), "published finding") {
					return runner.Response{}, fmt.Errorf("borrowed provenance missing: %s", encoded)
				}
				return runner.Response{
					Output: "bob used published finding",
				}, nil

			}

			return runner.Response{}, fmt.Errorf("unexpected tool %s", name)
		},
	)); err != nil {
		return err
	}
	result, err := c.send("bob", "alice", "start cycle")
	if err != nil || result.Answer != "alice combined bob's findings" {
		return fmt.Errorf("cycle did not complete: %+v %v", result, err)
	}
	if c.nodes["alice"].calls.Load() != 2 || c.nodes["bob"].calls.Load() != 3 {
		return errors.New("cycle invoked an extra model")
	}
	for _, n := range c.nodes {
		entries, err := c.readEntries(n)
		if err != nil {
			return err
		}
		if _, err = inspectLifecycle(entries); err != nil {
			return err
		}
	}
	return nil
}

func (c *collaborationScenario) independentRoots() error {
	start := newModelGate(2)
	finish := newModelGate(2)
	defer start.unblock()
	defer finish.unblock()

	fixture := func(peer string) modelFunc {
		return modelFunc(
			func(ctx context.Context, r runner.Request) (runner.Response, error) {
				name, result := lastTool(r)

				if name == "" {

					if err := start.block(ctx); err != nil {
						return runner.Response{}, err
					}
					return toolCall("send_peer_message", map[string]any{"peer": peer, "message": "help"}), nil

				}

				if result["status"] != "busy" {
					return runner.Response{}, fmt.Errorf("independent root callback wasn't busy: %v", result)
				}

				if err := finish.block(ctx); err != nil {
					return runner.Response{}, err
				}
				return runner.Response{
					Output: "continued after busy",
				}, nil
			},
		)
	}
	if err := c.replaceAlice(fixture("bob")); err != nil {
		return err
	}
	if err := c.startNode("bob", fixture("alice")); err != nil {
		return err
	}
	results := make(chan heldResult, 2)
	ctx := c.s.ctx
	for _, name := range []string{"alice", "bob"} {
		endpoint := c.nodes[name].server.URL
		go func() {
			result, err := sendPeerMessage(ctx, "root", endpoint, "independent root")
			results <- heldResult{
				result: result,
				err:    err,
			}
		}()
	}
	for range 2 {
		if err := start.awaitEntry(c.s.ctx); err != nil {
			return err
		}
	}
	start.unblock()
	for i := 0; i < 2; i++ {
		select {
		case <-finish.entered:
		case r := <-results:
			return fmt.Errorf("root finished before BUSY barrier: %+v", r)
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
	}
	finish.unblock()
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil || r.result.Answer != "continued after busy" {
				return fmt.Errorf("root result: %+v", r)
			}
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
	}
	if c.nodes["alice"].calls.Load() != 2 || c.nodes["bob"].calls.Load() != 2 {
		return errors.New("independent roots reentered reasoning")
	}
	return nil
}
