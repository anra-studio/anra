package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/cucumber/godog"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model/anchor"
	"github.com/scbizu/anra/pkg/registry"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type collaborationNode struct {
	registration *discovery.Registration
	a            *agent.Anra
	tape         *bbolt.Bbolt
	service      *a2a.Service
	server       *httptest.Server
	card         *sdk.AgentCard
	calls        atomic.Int32
	requests     atomic.Int32
}
type heldResult struct {
	result a2a.CollaborationResult
	err    error
}
type collaborationScenario struct {
	s              *scenario
	nodes          map[string]*collaborationNode
	directory      *httptest.Server
	registry       *discovery.Client
	registryStore  *registry.Registry
	entered        chan struct{}
	release        chan struct{}
	held           chan heldResult
	heldCancel     context.CancelFunc
	busy           a2a.CollaborationResult
	originalSource *pb.Source
	originalRef    *pb.AnchorRef
	decision       *pb.Decision
	originalBytes  []byte
	now            atomic.Int64
}

func initializeCollaboration(sc *godog.ScenarioContext, s *scenario) {
	c := &collaborationScenario{s: s}
	initializeDelegator(sc, c)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*c = collaborationScenario{s: s, nodes: map[string]*collaborationNode{}}
		c.now.Store(time.Now().UnixNano())
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if c.heldCancel != nil {
			c.heldCancel()
		}
		if c.release != nil {
			select {
			case <-c.release:
			default:
				close(c.release)
			}
		}
		var err error
		for _, n := range c.nodes {
			if n.server != nil {
				if n.registration != nil {
					n.registration.Close()
				}
				n.server.Close()
			}
			if n.a != nil {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				drainErr := n.a.Shutdown(cleanup)
				cancel()
				if drainErr != nil {
					err = errors.Join(err, drainErr)
					continue
				}
			}
			if n.service != nil {
				err = errors.Join(err, n.service.Close())
			}
			if n.tape != nil {
				err = errors.Join(err, n.tape.Close())
			}
		}
		if c.directory != nil {
			c.directory.Close()
		}
		if c.registryStore != nil {
			err = errors.Join(err, c.registryStore.Close())
		}
		return ctx, err
	})
	sc.Step(`^a collaboration agent$`, c.setup)
	sc.Step(`^generated collaboration metadata roundtrips through A2A$`, c.protobufRoundtrip)
	sc.Step(`^collaboration rejects request metadata with (.+) before reasoning$`, c.rejectRequestMetadata)
	sc.Step(`^the client rejects response metadata with (.+)$`, c.rejectResponseMetadata)
	sc.Step(`^Alice has completed a shareable answer$`, func() error {
		result, err := c.send("bob", "alice", "publish")
		if err != nil {
			return err
		}
		if result.Status != cp.Status_STATUS_COMPLETED || result.Answer != "published finding" {
			return fmt.Errorf("unexpected publication answer: %+v", result)
		}
		ref, err := c.nodes["alice"].a.Publication(s.ctx, "bob", nil)
		if err != nil || ref == nil {
			return fmt.Errorf("missing publication: %v", err)
		}
		c.originalRef = ref
		return nil
	})
	sc.Step(`^Alice is held inside its model call$`, c.hold)
	sc.Step(`^Bob asks the occupied Alice for reasoning$`, func() error { var err error; c.busy, err = c.send("bob", "alice", "callback"); return err })
	sc.Step(`^the response is BUSY without another invocation or model call$`, func() error {
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
	})
	sc.Step(`^BUSY reveals no publication or private control fields$`, func() error {
		if c.busy.Publication != nil {
			return errors.New("publication hint disclosed before any answer completed")
		}
		raw, _ := json.Marshal(c.busy)
		if strings.Contains(string(raw), "epoch") || strings.Contains(string(raw), "invocationId") {
			return errors.New("private control data disclosed")
		}
		return nil
	})
	sc.Step(`^Bob borrows the BUSY publication while Alice remains occupied$`, func() error {
		if c.busy.Publication == nil {
			return errors.New("missing busy publication")
		}
		client, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		req := &pb.BorrowRequest{RequestId: "busy-borrow", Purpose: "complete research", Query: "finding", Publication: c.busy.Publication}
		c.decision, err = client.Borrow(s.ctx, req)
		if err != nil {
			return err
		}
		if c.decision.Status != a2a.Granted {
			return fmt.Errorf("publication denied: %v", c.decision)
		}
		if err := c.checkPublication(client, c.decision.View); err != nil {
			return err
		}
		retry, err := client.Borrow(s.ctx, req)
		if err != nil || !proto.Equal(retry, c.decision) {
			return fmt.Errorf("unstable borrow retry: %v %v", retry, err)
		}
		select {
		case result := <-c.held:
			return fmt.Errorf("owner finished before borrowing: %+v", result)
		default:
		}
		return nil
	})
	sc.Step(`^the publication grant contains exactly the immutable anchor projection$`, func() error {
		client, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		return c.checkPublication(client, c.decision.View)
	})
	sc.Step(`^Alice finishes and publishes a newer answer$`, func() error {
		if err := c.finishHold(); err != nil {
			return err
		}
		result, err := c.send("bob", "alice", "new answer")
		if err != nil || result.Status != cp.Status_STATUS_COMPLETED {
			return fmt.Errorf("new answer: %+v %v", result, err)
		}
		return nil
	})
	sc.Step(`^Alice restarts with the same source and the grant remains readable$`, func() error {
		n := c.nodes["alice"]
		source := n.a.Source()
		if n.registration != nil {
			n.registration.Close()
		}
		n.server.Close()
		if err := n.service.Close(); err != nil {
			return err
		}
		n.service = nil
		if err := n.tape.Close(); err != nil {
			return err
		}
		n.tape = nil
		if err := c.startNode("alice", c.model()); err != nil {
			return err
		}
		if !proto.Equal(source, c.nodes["alice"].a.Source()) {
			return errors.New("restart changed source")
		}
		client, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		return c.checkPublication(client, c.decision.View)
	})
	sc.Step(`^another trusted requester gets its own grant but cannot read or release Bob's grant$`, func() error {
		client, err := c.viewClient("mallory")
		if err != nil {
			return err
		}
		own, err := client.Borrow(s.ctx, &pb.BorrowRequest{RequestId: "copied-hint", Purpose: "request own publication grant", Query: "finding", Publication: c.originalRef})
		if err != nil || own.Status != a2a.Granted || own.View == nil || own.View.ViewId == c.decision.View.ViewId {
			return fmt.Errorf("missing separate requester-bound grant: %v %v", own, err)
		}
		pages, err := client.ReadView(s.ctx, own.View)
		if err != nil {
			return err
		}
		bob, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		original, err := bob.ReadView(s.ctx, c.decision.View)
		if err != nil {
			return err
		}
		if len(pages) != 1 || len(original) != 1 || len(pages[0].Entries) != 1 || len(original[0].Entries) != 1 || !proto.Equal(pages[0].Entries[0], original[0].Entries[0]) {
			return errors.New("separate grants did not return the same immutable publication")
		}

		if _, err = client.ReadView(s.ctx, c.decision.View); !errors.Is(err, a2a.ErrUnavailable) {
			return fmt.Errorf("other requester read: %v", err)
		}
		if err = client.Release(s.ctx, c.decision.View.ViewId); !errors.Is(err, a2a.ErrUnavailable) {
			return fmt.Errorf("other requester release: %v", err)
		}
		return nil
	})
	sc.Step(`^a guessed reference cannot obtain private or control entries$`, func() error {
		client, err := c.viewClient("mallory")
		if err != nil {
			return err
		}
		source := c.nodes["alice"].a.Source()
		for _, seq := range []string{"1", "2", "999"} {
			d, err := client.Borrow(s.ctx, &pb.BorrowRequest{RequestId: "guess-" + seq, Purpose: "guess", Query: "private", Publication: &pb.AnchorRef{Source: source, Seq: seq}})
			if err != nil {
				return err
			}
			if d.Status != a2a.Denied || d.View != nil {
				return errors.New("guessed reference granted")
			}
		}
		return nil
	})
	sc.Step(`^the publication grant is ended by (release|revocation|expiry)$`, func(how string) error {
		client, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		switch how {
		case "release":
			err = client.Release(s.ctx, c.decision.View.ViewId)
		case "revocation":
			err = c.nodes["alice"].service.Revoke(s.ctx, "bob", c.decision.View.ViewId)
		case "expiry":
			c.now.Add(int64(2 * time.Hour))
		}
		if err != nil {
			return err
		}
		_, err = client.ReadView(s.ctx, c.decision.View)
		if !errors.Is(err, a2a.ErrUnavailable) {
			return fmt.Errorf("ended grant readable: %v", err)
		}
		return nil
	})
	sc.Step(`^publication requests reject a changed reference and incompatible bounds$`, func() error {
		client, err := c.viewClient("bob")
		if err != nil {
			return err
		}
		changed := anchor.CloneRef(c.busy.Publication)
		changed.Seq = "1"
		if _, err := client.Borrow(s.ctx, &pb.BorrowRequest{RequestId: "busy-borrow", Purpose: "complete research", Query: "finding", Publication: changed}); !errors.Is(err, a2a.ErrConflict) {
			return fmt.Errorf("changed reference: %v", err)
		}
		req := &pb.BorrowRequest{RequestId: "wrong-source", Purpose: "research", Query: "finding", Publication: anchor.CloneRef(c.busy.Publication)}
		req.Publication.Source.TapeId = "other-incarnation"
		d, err := client.Borrow(s.ctx, req)
		if err != nil || d.Status != a2a.Denied {
			return fmt.Errorf("wrong source granted: %v %v", d, err)
		}
		req.RequestId = "wrong-bounds"
		req.Publication = c.busy.Publication
		req.Bounds = &pb.EntryRange{SeqS: "1", SeqE: "2"}
		d, err = client.Borrow(s.ctx, req)
		if err != nil || d.Status != a2a.Denied {
			return fmt.Errorf("incompatible bounds granted: %v %v", d, err)
		}
		req.RequestId = "malformed"
		req.Bounds = nil
		req.Publication = anchor.CloneRef(c.busy.Publication)
		req.Publication.Seq = "01"
		if _, err := client.Borrow(s.ctx, req); !errors.Is(err, a2a.ErrInvalidParams) {
			return fmt.Errorf("malformed ref: %v", err)
		}
		return nil
	})
	sc.Step(`^Alice's cancelled invocation closes and its partial history is excluded$`, c.cancelAndContinue)
	sc.Step(`^simultaneous callers admit exactly one invocation$`, c.raceAdmission)
	sc.Step(`^cyclic peer reasoning completes using BUSY and borrowed findings$`, c.cycle)
	sc.Step(`^independent roots consult one another without blocking$`, c.independentRoots)
	sc.Step(`^repeated BUSY delegation is suppressed only within each invocation$`, c.suppression)
	sc.Step(`^Bob resumes reasoning only after borrowing newer lender-selected findings$`, c.lenderSelection)
	sc.Step(`^an expired delegation deadline rejects reasoning before model or peer calls$`, c.expiredDeadline)
	sc.Step(`^Bob borrows supplied context without reentering Alice$`, c.suppliedContext)
	sc.Step(`^a client rejects a publication grant with mismatched (source|range|head)$`, c.rejectMismatchedGrant)
	sc.Step(`^shutdown drains active reasoning before storage closes$`, func() error {
		if err := c.hold(); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		n := c.nodes["alice"]
		if err := n.a.Shutdown(ctx); err != nil {
			return err
		}
		if err := n.a.Shutdown(ctx); err != nil {
			return err
		}
		if _, err := n.a.Run(s.ctx, "too late"); !errors.Is(err, agent.ErrClosing) {
			return fmt.Errorf("shutdown admitted work: %v", err)
		}
		entries, err := c.readEntries(n)
		if err != nil {
			return err
		}
		last, err := anchor.Decode(entries[len(entries)-1])
		if err != nil {
			return err
		}
		if last == nil || last.Type != "invocation.closed" || last.Outcome != "cancelled" {
			return fmt.Errorf("shutdown left an open invocation: %+v", last)
		}
		return n.tape.Close()
	})
	sc.Step(`^an unresolved terminal append fences further reasoning$`, func() error {
		if err := c.hold(); err != nil {
			return err
		}
		n := c.nodes["alice"]
		if err := n.tape.Close(); err != nil {
			return err
		}
		close(c.release)
		select {
		case result := <-c.held:
			if result.err == nil {
				return errors.New("storage failure acknowledged success")
			}
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
		before := n.calls.Load()
		_, err := n.a.Run(s.ctx, "must remain fenced")
		if !errors.Is(err, agent.ErrFenced) || n.calls.Load() != before {
			return fmt.Errorf("unresolved close reopened admission: %v", err)
		}
		return nil
	})
	sc.Step(`^startup rejects a tape containing (unsupported schema|malformed record|overlapping starts|unmatched close|conflicting source)$`, func(problem string) error {
		n := c.nodes["alice"]
		if n.registration != nil {
			n.registration.Close()
		}
		n.server.Close()
		if err := n.service.Close(); err != nil {
			return err
		}
		n.service = nil
		payloads := []string{}
		switch problem {
		case "unsupported schema":
			payloads = []string{`{"schema":"anra.dev/anchor/v2","type":"invocation.started"}`}
		case "malformed record":
			payloads = []string{`{"schema":"anra.dev/anchor/v1","type":`}
		case "overlapping starts":
			payloads = []string{`{"schema":"anra.dev/anchor/v1","type":"invocation.started","invocationId":"one","ownerEpoch":"old"}`, `{"schema":"anra.dev/anchor/v1","type":"invocation.started","invocationId":"two","ownerEpoch":"old"}`}
		case "unmatched close":
			payloads = []string{`{"schema":"anra.dev/anchor/v1","type":"invocation.closed","invocationId":"unknown","startSeq":"1","outcome":"completed"}`}
		case "conflicting source":
			payloads = []string{`{"schema":"anra.dev/anchor/v1","type":"tape.initialized","source":{"agentId":"alice","tapeId":"replacement"}}`}
		}
		for _, raw := range payloads {
			if _, err := n.tape.Store(owner.WithOwnerId(s.ctx, "alice"), entry.NewAnchor(entry.Seq{}, "alice", entry.AnchorKindCustom, []byte(raw))); err != nil {
				return err
			}
		}
		if _, err := agent.New("alice", n.tape); err == nil {
			return errors.New("corrupt control history was interpreted as healthy")
		}
		return nil
	})
	sc.Step(`^generic A2A callers receive an unaccepted response with no hint$`, func() error {
		client, err := a2aclient.NewFromCard(s.ctx, c.nodes["alice"].card, a2aclient.WithJSONRPCTransport(a2a.TrustedHTTPClient("bob")))
		if err != nil {
			return err
		}
		response, err := client.SendMessage(s.ctx, &sdk.SendMessageRequest{Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("generic callback"))})
		if err != nil {
			return err
		}
		message, ok := response.(*sdk.Message)
		if !ok || len(message.Metadata) != 0 || len(message.Extensions) != 0 || len(message.Parts) != 1 || !strings.Contains(message.Parts[0].Text(), "request was not accepted") {
			return fmt.Errorf("generic caller received private/ambiguous BUSY: %+v", response)
		}
		return nil
	})
	sc.Step(`^legacy lenders cannot silently accept publication references$`, func() error {
		card := *c.nodes["alice"].card
		card.Capabilities.Extensions = append([]sdk.AgentExtension(nil), card.Capabilities.Extensions...)
		for i := range card.Capabilities.Extensions {
			if card.Capabilities.Extensions[i].URI == a2a.ExtensionURI {
				ext := card.Capabilities.Extensions[i]
				ext.Params = map[string]any{"endpoint": c.nodes["alice"].server.URL + a2a.ViewPath}
				card.Capabilities.Extensions[i] = ext
			}
		}
		client, err := a2a.NewClient(a2a.TrustedHTTPClient("bob"), &card)
		if err != nil {
			return err
		}
		_, err = client.Borrow(s.ctx, &pb.BorrowRequest{RequestId: "legacy", Purpose: "test", Query: "test", Publication: c.originalRef})
		if !errors.Is(err, a2a.ErrInactive) {
			return fmt.Errorf("legacy publication fallback: %v", err)
		}
		return nil
	})
}
func (c *collaborationScenario) setup() error {
	store, err := registry.Open(registry.Config{Path: filepath.Join(c.s.root, "collaboration-registry.db"), HeartbeatTimeout: time.Minute})
	if err != nil {
		return err
	}
	c.registryStore = store
	c.directory = httptest.NewServer(registry.NewHandler(store))
	c.registry, err = discovery.NewClient(c.directory.URL)
	if err != nil {
		return err
	}
	return c.startNode("alice", c.model())
}
func (c *collaborationScenario) model() modelFunc {
	return modelFunc(func(ctx context.Context, r runner.Request) (runner.Response, error) {
		input := r.Messages[len(r.Messages)-1].Text
		if input == "hold" {
			select {
			case c.entered <- struct{}{}:
			case <-ctx.Done():
				return runner.Response{}, ctx.Err()
			}
			select {
			case <-c.release:
			case <-ctx.Done():
				return runner.Response{}, ctx.Err()
			}
		}
		if input == "publish" {
			return runner.Response{Output: "published finding"}, nil
		}
		return runner.Response{Output: "answer: " + input}, nil
	})
}
func (c *collaborationScenario) startNode(name string, m modelFunc) error {
	n := &collaborationNode{}
	c.nodes[name] = n
	var err error
	n.tape, err = bbolt.NewBboltStorage("default", filepath.Join(c.s.root, "collab-"+name+".db"))
	if err != nil {
		return err
	}
	if err = n.tape.Init(owner.WithOwnerId(c.s.ctx, name)); err != nil {
		return err
	}
	opts := []agent.Option{agent.WithLLM(modelFunc(func(ctx context.Context, r runner.Request) (runner.Response, error) { n.calls.Add(1); return m(ctx, r) })), agent.WithRegistry(c.registry, a2a.TrustedHTTPClient(name))}
	n.a, err = agent.New(name, n.tape, opts...)
	if err != nil {
		return err
	}
	n.service, err = a2a.NewService(a2a.Config{Source: n.a.Source(), Owner: name, Tape: n.tape, JournalPath: filepath.Join(c.s.root, "collab-"+name+".grants.db"), Publications: n.a, Exporter: a2a.PublicationExporter{}, Now: func() time.Time { return time.Unix(0, c.now.Load()) }, Policy: a2a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, entry.Seq) (a2a.Selection, error) {
		return a2a.Selection{Status: a2a.Denied}, nil
	})})
	if err != nil {
		return err
	}
	n.server = httptest.NewUnstartedServer(nil)
	base := "http://" + n.server.Listener.Addr().String()
	n.card = &sdk.AgentCard{Name: name, Version: "1", Description: "collaboration fixture", SupportedInterfaces: []*sdk.AgentInterface{sdk.NewAgentInterface(base+a2a.CorePath, sdk.TransportProtocolJSONRPC)}, DefaultInputModes: []string{"text/plain"}, DefaultOutputModes: []string{"text/plain"}}
	a2a.AdvertiseCollaboration(n.card)
	if err = a2a.AdvertisePublications(n.card, base+a2a.ViewPath); err != nil {
		return err
	}
	exec, err := a2a.NewExecutor(n.a)
	if err != nil {
		return err
	}
	handler, err := a2a.NewPeerHandler(n.card, a2asrv.NewHandler(exec), n.service, a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	n.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2a.CorePath {
			n.requests.Add(1)
		}
		handler.ServeHTTP(w, r)
	})
	n.server.Start()
	n.registration, err = c.registry.Start(c.s.ctx, &pv.Peer{Id: name, Address: base, Description: "Collaboration fixture", Skill: []*pv.Skill{{Id: "chat", Name: "Chat"}, {Id: "borrow-context", Name: "Borrow context"}}})
	return err
}
func (c *collaborationScenario) send(from, to, input string) (a2a.CollaborationResult, error) {
	return c.sendCtx(c.s.ctx, from, to, input)
}
func (c *collaborationScenario) sendCtx(ctx context.Context, from, to, input string) (a2a.CollaborationResult, error) {
	client, err := messageClient(ctx, a2a.TrustedHTTPClient(from), c.nodes[to].server.URL)
	if err != nil {
		return a2a.CollaborationResult{}, err
	}
	return client.Send(ctx, input)
}
func (c *collaborationScenario) viewClient(peer string) (*a2a.Client, error) {
	return a2a.NewClient(a2a.TrustedHTTPClient(peer), c.nodes["alice"].card)
}
func (c *collaborationScenario) hold() error {
	c.entered = make(chan struct{}, 1)
	c.release = make(chan struct{})
	c.held = make(chan heldResult, 1)
	ctx, cancel := context.WithCancel(c.s.ctx)
	c.heldCancel = cancel
	go func() { r, e := c.sendCtx(ctx, "bob", "alice", "hold"); c.held <- heldResult{r, e} }()
	select {
	case <-c.entered:
		return nil
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
	}
}
func (c *collaborationScenario) finishHold() error {
	close(c.release)
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
	h, err := n.tape.Get(ctx)
	if err != nil {
		return nil, err
	}
	r, err := n.tape.Range(ctx, view.EntryRange{SeqS: entry.SeqFromUint64(1), SeqE: h.Scope.SeqE.Next()})
	return r.Raw, err
}
func (c *collaborationScenario) checkPublication(client *a2a.Client, v *pb.View) error {
	ref := c.originalRef
	if ref == nil || v.SnapshotHead != ref.Seq || !proto.Equal(v.Source, ref.Source) || len(v.Ranges) != 1 || v.Ranges[0].SeqS != ref.Seq || v.Ranges[0].SeqE != entry.MustParseSeq(ref.Seq).Next().String() {
		return fmt.Errorf("incorrect publication range: %v", v)
	}
	pages, err := client.ReadView(c.s.ctx, v)
	if err != nil {
		return err
	}
	if len(pages) != 1 || len(pages[0].Entries) != 1 {
		return errors.New("publication did not yield exactly one anchor")
	}
	e := pages[0].Entries[0]
	if e.Kind != "anchor:custom" || e.Summary != "published finding" || e.Payload.MediaType != anchor.MediaType {
		return fmt.Errorf("wrong projection: %v", e)
	}
	var p anchor.Publication
	if err := json.Unmarshal(e.Payload.JsonData, &p); err != nil {
		return err
	}
	if len(p.Covers) != 1 || p.Summary != "published finding" || entry.MustParseSeq(p.SourceHead).Cmp(entry.MustParseSeq(ref.Seq)) >= 0 {
		return errors.New("invalid public coverage")
	}
	raw, _ := json.Marshal(pages[0])
	if strings.Contains(string(raw), "ownerEpoch") || strings.Contains(string(raw), "invocationId") || strings.Contains(string(raw), "adk:event") {
		return errors.New("private execution metadata leaked")
	}
	if c.originalBytes == nil {
		c.originalBytes = raw
	} else if string(raw) != string(c.originalBytes) {
		return errors.New("publication bytes changed after writes/restart")
	}
	return nil
}

func (c *collaborationScenario) raceAdmission() error {
	c.entered = make(chan struct{}, 1)
	c.release = make(chan struct{})
	const count = 12
	start := make(chan struct{})
	results := make(chan heldResult, count)
	for i := 0; i < count; i++ {
		go func() { <-start; r, e := c.send("bob", "alice", "hold"); results <- heldResult{r, e} }()
	}
	close(start)
	select {
	case <-c.entered:
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
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
	close(c.release)
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
	m := modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
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
		return runner.Response{Output: "clean next answer"}, nil
	})
	if err := c.startNode("bob", m); err != nil {
		return err
	}
	_, err := c.send("alice", "bob", "cancel this work")
	if err == nil {
		return errors.New("cancelled model reported success")
	}
	result, err := c.send("alice", "bob", "next")
	if err != nil || result.Answer != "clean next answer" {
		return fmt.Errorf("next invocation: %+v %v", result, err)
	}
	entries, err := c.readEntries(c.nodes["bob"])
	if err != nil {
		return err
	}
	cancelled, completed, calls := 0, 0, 0
	for _, e := range entries {
		r, err := anchor.Decode(e)
		if err != nil {
			return err
		}
		if r != nil && r.Type == "invocation.closed" {
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
	return runner.Response{ToolCalls: []runner.ToolCall{{ID: "call-" + name, Name: name, Arguments: data}}}
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
func (c *collaborationScenario) replaceAlice(m modelFunc) error {
	n := c.nodes["alice"]
	if n.registration != nil {
		n.registration.Close()
	}
	n.server.Close()
	if err := n.service.Close(); err != nil {
		return err
	}
	n.service = nil
	if err := n.tape.Close(); err != nil {
		return err
	}
	n.tape = nil
	return c.startNode("alice", m)
}
func (c *collaborationScenario) cycle() error {
	if err := c.replaceAlice(modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		name, result := lastTool(r)
		if name == "" {
			return toolCall("send_peer_message", map[string]any{"peer": "bob", "message": "help alice"}), nil
		}
		if result["status"] != "completed" || result["answer"] != "bob used published finding" {
			return runner.Response{}, fmt.Errorf("unexpected Bob result: %v", result)
		}
		return runner.Response{Output: "alice combined bob's findings"}, nil
	})); err != nil {
		return err
	}
	if err := c.startNode("bob", modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		name, result := lastTool(r)
		switch name {
		case "":
			return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "clarify findings"}), nil
		case "send_peer_message":
			if result["status"] != "busy" || result["publication"] == nil {
				return runner.Response{}, fmt.Errorf("callback did not return BUSY/publication: %v", result)
			}
			return toolCall("borrow_peer_context", map[string]any{"peer": "alice", "requestId": "cycle-borrow", "purpose": "help alice", "query": "findings"}), nil
		case "borrow_peer_context":
			encoded, _ := json.Marshal(result)
			if !strings.Contains(string(encoded), "published finding") || !strings.Contains(string(encoded), "snapshotHead") {
				return runner.Response{}, fmt.Errorf("borrowed provenance missing: %s", encoded)
			}
			return runner.Response{Output: "bob used published finding"}, nil
		}
		return runner.Response{}, fmt.Errorf("unexpected tool %s", name)
	})); err != nil {
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
	entered := make(chan struct{}, 2)
	proceed := make(chan struct{})
	received := make(chan struct{}, 2)
	finish := make(chan struct{})
	defer func() {
		select {
		case <-proceed:
		default:
			close(proceed)
		}
		select {
		case <-finish:
		default:
			close(finish)
		}
	}()
	fixture := func(peer string) modelFunc {
		return modelFunc(func(ctx context.Context, r runner.Request) (runner.Response, error) {
			name, result := lastTool(r)
			if name == "" {
				entered <- struct{}{}
				select {
				case <-proceed:
				case <-ctx.Done():
					return runner.Response{}, ctx.Err()
				}
				return toolCall("send_peer_message", map[string]any{"peer": peer, "message": "help"}), nil
			}
			if result["status"] != "busy" {
				return runner.Response{}, fmt.Errorf("independent root callback wasn't busy: %v", result)
			}
			received <- struct{}{}
			select {
			case <-finish:
			case <-ctx.Done():
				return runner.Response{}, ctx.Err()
			}
			return runner.Response{Output: "continued after busy"}, nil
		})
	}
	if err := c.replaceAlice(fixture("bob")); err != nil {
		return err
	}
	if err := c.startNode("bob", fixture("alice")); err != nil {
		return err
	}
	results := make(chan heldResult, 2)
	for _, name := range []string{"alice", "bob"} {
		go func(name string) { r, e := c.send("root", name, "independent root"); results <- heldResult{r, e} }(name)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
	}
	close(proceed)
	for i := 0; i < 2; i++ {
		select {
		case <-received:
		case r := <-results:
			return fmt.Errorf("root finished before BUSY barrier: %+v", r)
		case <-c.s.ctx.Done():
			return c.s.ctx.Err()
		}
	}
	close(finish)
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
func (c *collaborationScenario) suppression() error {
	if err := c.hold(); err != nil {
		return err
	}
	if err := c.startNode("bob", modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		name, result := lastTool(r)
		if name == "" {
			return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "first callback"}), nil
		}
		if result["status"] == "busy" {
			return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "a different prompt"}), nil
		}
		if result["status"] != "no_new_context" {
			return runner.Response{}, fmt.Errorf("repeat wasn't suppressed: %v", result)
		}
		return runner.Response{Output: "continued without polling"}, nil
	})); err != nil {
		return err
	}
	before := c.nodes["alice"].requests.Load()
	for i := int32(1); i <= 2; i++ {
		result, err := c.send("root", "bob", "consult alice twice")
		if err != nil || result.Answer != "continued without polling" {
			return fmt.Errorf("suppression in invocation %d: %+v %v", i, result, err)
		}
		if c.nodes["alice"].requests.Load() != before+i {
			return errors.New("BUSY suppression leaked between invocations or permitted polling")
		}
	}
	return c.finishHold()
}
func (c *collaborationScenario) lenderSelection() error {
	turn := 0
	var requests int32
	var selected *pb.AnchorRef
	if err := c.startNode("bob", modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		turn++
		found := false
		for _, spec := range r.Tools {
			if spec.Name != "borrow_peer_context" {
				continue
			}
			found = true
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
				return runner.Response{}, err
			}
			if len(schema.Properties) != 4 || schema.Properties["publication"] != nil {
				return runner.Response{}, fmt.Errorf("borrow tool exposes context selection: %s", spec.InputSchema)
			}
		}
		if !found {
			return runner.Response{}, errors.New("missing named peer borrow tool")
		}
		_, result := lastTool(r)
		switch turn {
		case 1:
			return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "help with findings"}), nil
		case 2:
			if result["status"] != "busy" {
				return runner.Response{}, fmt.Errorf("expected initial BUSY: %v", result)
			}
			return toolCall("borrow_peer_context", map[string]any{"peer": "alice", "requestId": "unchanged", "purpose": "continue research", "query": "findings"}), nil
		case 3, 5:
			raw, _ := json.Marshal(result["decision"])
			var decision pb.Decision
			if err := protojson.Unmarshal(raw, &decision); err != nil {
				return runner.Response{}, err
			}
			expected := c.originalRef
			if turn == 5 {
				expected = selected
			}
			if decision.Status != a2a.Granted || decision.View == nil || !proto.Equal(decision.View.Source, expected.Source) || decision.View.SnapshotHead != expected.Seq {
				return runner.Response{}, fmt.Errorf("lender did not select expected publication: %v", &decision)
			}
			requests = c.nodes["alice"].requests.Load()
			return toolCall("send_peer_message", map[string]any{"peer": "alice", "message": "reason using borrowed findings"}), nil
		case 4:
			if result["status"] != "no_new_context" || c.nodes["alice"].requests.Load() != requests {
				return runner.Response{}, fmt.Errorf("unchanged borrowed findings permitted polling: %v", result)
			}
			if err := c.finishHold(); err != nil {
				return runner.Response{}, err
			}
			if _, err := c.send("root", "alice", "new findings"); err != nil {
				return runner.Response{}, err
			}
			var err error
			selected, err = c.nodes["alice"].a.Publication(c.s.ctx, "bob", nil)
			if err != nil || selected == nil || proto.Equal(selected, c.originalRef) {
				return runner.Response{}, fmt.Errorf("lender has no newer publication: %v", err)
			}
			if err := c.hold(); err != nil {
				return runner.Response{}, err
			}
			return toolCall("borrow_peer_context", map[string]any{"peer": "alice", "requestId": "newer", "purpose": "continue research", "query": "findings"}), nil
		case 6:
			if result["status"] != "busy" || c.nodes["alice"].requests.Load() != requests+1 {
				return runner.Response{}, fmt.Errorf("new lender-selected context did not permit one attempt: %v", result)
			}
			return runner.Response{Output: "continued with lender-selected findings"}, nil
		}
		return runner.Response{}, fmt.Errorf("unexpected model turn %d", turn)
	})); err != nil {
		return err
	}
	result, err := c.send("root", "bob", "consult alice")
	if err != nil || result.Answer != "continued with lender-selected findings" {
		return fmt.Errorf("lender selection failed: %+v %v", result, err)
	}
	return c.finishHold()
}

func (c *collaborationScenario) expiredDeadline() error {
	n := c.nodes["alice"]
	before, err := c.readEntries(n)
	if err != nil {
		return err
	}
	client, err := a2aclient.NewFromCard(c.s.ctx, n.card, a2aclient.WithJSONRPCTransport(a2a.TrustedHTTPClient("root")))
	if err != nil {
		return err
	}
	message := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("deadline has expired"))
	message.Extensions = []string{a2a.CollaborationURI}
	metadata, err := metadataObject(&cp.DelegationMetadata{RootRequestId: "expired-root", Deadline: timestamppb.New(time.Now().Add(-time.Second))})
	if err != nil {
		return err
	}
	message.Metadata = map[string]any{a2a.CollaborationURI: metadata}
	_, err = client.SendMessage(c.s.ctx, &sdk.SendMessageRequest{Message: message})
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		return fmt.Errorf("expired delegation did not fail with a deadline error: %v", err)
	}
	after, err := c.readEntries(n)
	if err != nil {
		return err
	}
	if len(after) != len(before) || n.calls.Load() != 0 || n.requests.Load() != 1 {
		return errors.New("expired deadline entered reasoning, changed tape, or called a peer")
	}
	return nil
}

func (c *collaborationScenario) suppliedContext() error {
	if err := c.startNode("bob", modelFunc(func(_ context.Context, r runner.Request) (runner.Response, error) {
		name, result := lastTool(r)
		switch name {
		case "":
			return toolCall("discover_peers", map[string]any{}), nil
		case "discover_peers":
			refs, ok := result["context"].([]any)
			if !ok || len(refs) != 1 {
				return runner.Response{}, fmt.Errorf("incoming references not discoverable: %v", result)
			}
			return toolCall("borrow_peer_context", map[string]any{"peer": "alice", "requestId": "supplied", "purpose": "answer from supplied findings", "query": "finding"}), nil
		case "borrow_peer_context":
			raw, _ := json.Marshal(result)
			if !strings.Contains(string(raw), "published finding") {
				return runner.Response{}, fmt.Errorf("supplied context not borrowed: %s", raw)
			}
			return runner.Response{Output: "used supplied findings"}, nil
		}
		return runner.Response{}, errors.New("unexpected tool")
	})); err != nil {
		return err
	}
	client, err := a2aclient.NewFromCard(c.s.ctx, c.nodes["bob"].card, a2aclient.WithJSONRPCTransport(a2a.TrustedHTTPClient("alice")))
	if err != nil {
		return err
	}
	message := sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart("use supplied findings"))
	message.Extensions = []string{a2a.CollaborationURI}
	metadata, err := metadataObject(&cp.DelegationMetadata{RootRequestId: "supplied-root", Deadline: timestamppb.New(time.Now().Add(5 * time.Second)), Ancestors: []string{"alice"}, Context: []*pb.AnchorRef{c.originalRef}})
	if err != nil {
		return err
	}
	message.Metadata = map[string]any{a2a.CollaborationURI: metadata}
	before := c.nodes["alice"].requests.Load()
	response, err := client.SendMessage(c.s.ctx, &sdk.SendMessageRequest{Message: message})
	if err != nil {
		return err
	}
	answer, ok := response.(*sdk.Message)
	if !ok || len(answer.Parts) != 1 || answer.Parts[0].Text() != "used supplied findings" || c.nodes["alice"].requests.Load() != before {
		return fmt.Errorf("supplied context caused reasoning callback: %+v", response)
	}
	select {
	case <-c.held:
		return errors.New("owner unexpectedly finished before context borrowing")
	default:
	}
	return nil
}
func (c *collaborationScenario) rejectMismatchedGrant(problem string) error {
	ref := c.originalRef
	seq := entry.MustParseSeq(ref.Seq)
	v := &pb.View{ViewId: "mismatched-grant", Source: proto.Clone(ref.Source).(*pb.Source), SnapshotHead: ref.Seq, Ranges: []*pb.EntryRange{{SeqS: ref.Seq, SeqE: seq.Next().String()}}, ExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}
	switch problem {
	case "source":
		v.Source.TapeId = "other-source"
	case "range":
		v.Ranges[0] = &pb.EntryRange{SeqS: seq.Next().String(), SeqE: seq.Next().Next().String()}
	case "head":
		v.SnapshotHead = seq.Next().String()
	}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/proto")
		w.Header().Set(a2a.ExtensionHeader, a2a.ExtensionURI)
		if strings.HasSuffix(r.URL.Path, "/Read") {
			reads.Add(1)
			http.Error(w, "must not read mismatched view", 500)
			return
		}
		encoded, _ := proto.Marshal(&pb.BorrowResponse{Decision: &pb.Decision{RequestId: "mismatch", Status: a2a.Granted, View: v}})
		_, _ = w.Write(encoded)
	}))
	defer server.Close()
	card := &sdk.AgentCard{Capabilities: sdk.AgentCapabilities{Extensions: []sdk.AgentExtension{{URI: a2a.ExtensionURI, Params: map[string]any{"endpoint": server.URL + a2a.ViewPath, "publicationBorrowVersion": 1}}}}}
	client, err := a2a.NewClient(a2a.TrustedHTTPClient("bob"), card)
	if err != nil {
		return err
	}
	req := &pb.BorrowRequest{RequestId: "mismatch", Purpose: "test mismatched provenance", Query: "finding", Publication: ref}
	args, _ := protojson.Marshal(req)
	_, err = (a2a.BorrowTool{Client: client}).Execute(c.s.ctx, args)
	if !errors.Is(err, a2a.ErrInvalidParams) || reads.Load() != 0 {
		return fmt.Errorf("mismatched publication consumed: %v reads=%d", err, reads.Load())
	}
	return nil
}
