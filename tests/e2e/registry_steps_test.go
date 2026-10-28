package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/cucumber/godog"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/gen/anra/registry/v1/registryv1connect"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/registry"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type registryScenario struct {
	drop              atomic.Bool
	session           *discovery.Registration
	reconciled        chan *pv.Peer
	s                 *scenario
	store             *registry.Registry
	server            *httptest.Server
	client            registryv1connect.RegistryServiceClient
	now               atomic.Int64
	expected          *pv.Peer
	current, original *rv.RegisterRequest
	update            *rv.RegisterRequest
	updateRevision    string
	forget            *rv.ForgetRequest
	lastError         error
}

func initializeRegistry(sc *godog.ScenarioContext, s *scenario) {
	r := &registryScenario{s: s}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*r = registryScenario{s: s}
		r.now.Store(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC).UnixNano())
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) { return ctx, r.close() })
	sc.Step(`^a durable Peer registry with a controlled clock$`, r.open)
	sc.Step(`^Alice has registered her research skills$`, func() error {
		r.expected = &pv.Peer{Id: "alice", Description: "Alice researches", Address: "http://127.0.0.1:8080", Skill: []*pv.Skill{{Id: "chat", Name: "Chat"}, {Id: "research", Name: "Research"}, {Id: "borrow-context", Name: "Borrow context"}}}
		r.current = &rv.RegisterRequest{Peer: proto.Clone(r.expected).(*pv.Peer), RuntimeId: "alice-first-runtime", Token: "private-alice-first-secret"}
		r.original = proto.Clone(r.current).(*rv.RegisterRequest)
		_, err := r.client.Register(s.ctx, r.current)
		return err
	})
	sc.Step(`^Alice is (online|offline|unknown) with her registered metadata$`, func(state string) error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		want := map[string]rv.PresenceState{"online": rv.PresenceState_PRESENCE_STATE_ONLINE, "offline": rv.PresenceState_PRESENCE_STATE_OFFLINE, "unknown": rv.PresenceState_PRESENCE_STATE_UNKNOWN}[state]
		if entry.Presence.State != want || !proto.Equal(entry.Peer, r.expected) {
			return fmt.Errorf("Peer/presence = %v, want %s and %v", entry, state, r.expected)
		}
		return nil
	})
	sc.Step(`^registry contact becomes stale$`, func() { r.now.Add(int64(2 * time.Second)) })
	sc.Step(`^Alice sends a heartbeat$`, func() {
		_, r.lastError = r.client.Heartbeat(s.ctx, &rv.HeartbeatRequest{Id: "alice", RuntimeId: r.current.RuntimeId, Token: r.current.Token})
	})
	sc.Step(`^Alice re-establishes her presence$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		request := proto.Clone(r.current).(*rv.RegisterRequest)
		request.ExpectedRevision = entry.Revision
		_, r.lastError = r.client.Register(s.ctx, request)
		if r.lastError == nil {
			r.current = request
		}
		return nil
	})
	sc.Step(`^Alice disconnects twice$`, func() error {
		for i := 0; i < 2; i++ {
			_, err := r.client.Disconnect(s.ctx, &rv.DisconnectRequest{Id: "alice", RuntimeId: r.current.RuntimeId, Token: r.current.Token})
			if err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the durable registry restarts$`, func() error {
		if err := r.close(); err != nil {
			return err
		}
		return r.open()
	})
	sc.Step(`^Alice has no fabricated last-contact time$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		if entry.Presence.LastSeen != nil {
			return errors.New("registry invented contact after restart")
		}
		return nil
	})
	sc.Step(`^Alice retries her original registration$`, func() { _, r.lastError = r.client.Register(s.ctx, r.original) })
	sc.Step(`^the registry rejects the operation with "([^"]*)"$`, func(code string) error {
		if r.lastError == nil || connect.CodeOf(r.lastError).String() != code {
			return fmt.Errorf("registry error = %v; want %s", r.lastError, code)
		}
		return nil
	})
	sc.Step(`^an unrelated runtime attempts "([^"]*)"$`, func(operation string) error {
		switch operation {
		case "register":
			entry, err := r.lookup("alice")
			if err != nil {
				return err
			}
			_, r.lastError = r.client.Register(s.ctx, &rv.RegisterRequest{Peer: r.expected, RuntimeId: "unrelated-runtime", Token: "unrelated-secret", ExpectedRevision: entry.Revision})
		case "heartbeat":
			_, r.lastError = r.client.Heartbeat(s.ctx, &rv.HeartbeatRequest{Id: "alice", RuntimeId: "unrelated-runtime", Token: "unrelated-secret"})
		case "disconnect":
			_, r.lastError = r.client.Disconnect(s.ctx, &rv.DisconnectRequest{Id: "alice", RuntimeId: "unrelated-runtime", Token: "unrelated-secret"})
		case "wrong key":
			request := proto.Clone(r.current).(*rv.RegisterRequest)
			request.Token = "wrong-secret"
			_, r.lastError = r.client.Register(s.ctx, request)
		default:
			return fmt.Errorf("unknown operation %s", operation)
		}
		return nil
	})
	sc.Step(`^a new Alice runtime registers a new address$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		value := proto.Clone(r.expected).(*pv.Peer)
		value.Address = "http://127.0.0.1:9091"
		req := &rv.RegisterRequest{Peer: value, RuntimeId: "alice-second-runtime", Token: "private-alice-second-secret", ExpectedRevision: entry.Revision}
		_, err = r.client.Register(s.ctx, req)
		if err != nil {
			return err
		}
		r.current = req
		r.expected = value
		return nil
	})
	sc.Step(`^the replacement becomes "(unknown|offline)"$`, func(state string) error {
		if state == "unknown" {
			r.now.Add(int64(2 * time.Second))
			return nil
		}
		_, err := r.client.Disconnect(s.ctx, &rv.DisconnectRequest{Id: "alice", RuntimeId: r.current.RuntimeId, Token: r.current.Token})
		return err
	})
	sc.Step(`^the old runtime cannot register, heartbeat or disconnect$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		old := proto.Clone(r.original).(*rv.RegisterRequest)
		old.ExpectedRevision = entry.Revision
		_, registerErr := r.client.Register(s.ctx, old)
		_, heartbeatErr := r.client.Heartbeat(s.ctx, &rv.HeartbeatRequest{Id: "alice", RuntimeId: old.RuntimeId, Token: old.Token})
		_, disconnectErr := r.client.Disconnect(s.ctx, &rv.DisconnectRequest{Id: "alice", RuntimeId: old.RuntimeId, Token: old.Token})
		for _, err := range []error{registerErr, heartbeatErr, disconnectErr} {
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				return fmt.Errorf("old runtime result: %v", err)
			}
		}
		return nil
	})
	sc.Step(`^Alice updates her description and skills$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		value := proto.Clone(r.expected).(*pv.Peer)
		value.Description = "Alice reviews research"
		value.Skill = append(value.Skill, &pv.Skill{Id: "review", Name: "Review"})
		req := &rv.RegisterRequest{Peer: value, RuntimeId: r.current.RuntimeId, Token: r.current.Token, ExpectedRevision: entry.Revision}
		response, err := r.client.Register(s.ctx, req)
		if err != nil {
			return err
		}
		r.expected = value
		r.update = req
		r.current = req
		r.updateRevision = response.Entry.Revision
		return nil
	})
	sc.Step(`^her accepted registration retry preserves its revision$`, func() error {
		response, err := r.client.Register(s.ctx, r.update)
		if err != nil {
			return err
		}
		if response.Entry.Revision != r.updateRevision {
			return errors.New("identical retry changed revision")
		}
		return nil
	})
	sc.Step(`^her Agent Card represents the updated metadata$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		card, err := a2a.Card(entry.Peer)
		if err != nil {
			return err
		}
		if card.Name != r.expected.Id || card.Description != r.expected.Description || len(card.Skills) != len(r.expected.Skill) || card.SupportedInterfaces[0].URL != r.expected.Address+a2a.CorePath {
			return fmt.Errorf("card lost canonical metadata: %v", card)
		}
		for i, skill := range card.Skills {
			if skill.ID != r.expected.Skill[i].Id || skill.Name != r.expected.Skill[i].Name {
				return fmt.Errorf("card changed skill %d", i)
			}
		}
		client, err := a2a.NewPeerClient(http.DefaultClient, entry.Peer)
		if err != nil || client == nil {
			return fmt.Errorf("typed borrow resolution failed: %v", err)
		}
		return nil
	})
	sc.Step(`^two owned updates use the same revision concurrently$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		type outcome struct {
			value *pv.Peer
			err   error
		}
		results := make(chan outcome, 2)
		for _, description := range []string{"first candidate", "second candidate"} {
			value := proto.Clone(r.expected).(*pv.Peer)
			value.Description = description
			go func() {
				_, err := r.client.Register(s.ctx, &rv.RegisterRequest{Peer: value, RuntimeId: r.current.RuntimeId, Token: r.current.Token, ExpectedRevision: entry.Revision})
				results <- outcome{value, err}
			}()
		}
		var successes, conflicts int
		for i := 0; i < 2; i++ {
			result := <-results
			if result.err == nil {
				successes++
				r.expected = result.value
			} else if connect.CodeOf(result.err) == connect.CodeAborted {
				conflicts++
			} else {
				return result.err
			}
		}
		if successes != 1 || conflicts != 1 {
			return fmt.Errorf("commits/conflicts = %d/%d", successes, conflicts)
		}
		return nil
	})
	sc.Step(`^exactly one update commits and the other reports a conflict$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		if !proto.Equal(entry.Peer, r.expected) {
			return errors.New("concurrent update lost the committed snapshot")
		}
		return nil
	})
	sc.Step(`^Bob is offline and Charlie is online with a different skill$`, func() error {
		for _, id := range []string{"bob", "charlie"} {
			_, err := r.client.Register(s.ctx, &rv.RegisterRequest{Peer: &pv.Peer{Id: id, Address: "http://127.0.0.1:8081", Skill: []*pv.Skill{{Id: "review", Name: "Review"}}}, RuntimeId: id + "-runtime", Token: "private-" + id + "-secret"})
			if err != nil {
				return err
			}
		}
		_, err := r.client.Disconnect(s.ctx, &rv.DisconnectRequest{Id: "bob", RuntimeId: "bob-runtime", Token: "private-bob-secret"})
		return err
	})
	sc.Step(`^Charlie re-establishes presence$`, func() error {
		entry, err := r.lookup("charlie")
		if err != nil {
			return err
		}
		_, err = r.client.Register(s.ctx, &rv.RegisterRequest{Peer: entry.Peer, RuntimeId: "charlie-runtime", Token: "private-charlie-secret", ExpectedRevision: entry.Revision})
		return err
	})
	sc.Step(`^listing preserves every Peer and filters by state and skill$`, func() error {
		cases := []struct {
			req *rv.ListRequest
			ids string
		}{
			{&rv.ListRequest{}, "alice,bob,charlie"},
			{&rv.ListRequest{State: rv.PresenceState_PRESENCE_STATE_ONLINE.Enum()}, "charlie"},
			{&rv.ListRequest{State: rv.PresenceState_PRESENCE_STATE_UNKNOWN.Enum(), Skill: "research"}, "alice"},
			{&rv.ListRequest{State: rv.PresenceState_PRESENCE_STATE_OFFLINE.Enum(), Skill: "review"}, "bob"},
			{&rv.ListRequest{Skill: "review"}, "bob,charlie"},
			{&rv.ListRequest{State: rv.PresenceState_PRESENCE_STATE_ONLINE.Enum(), Skill: "research"}, ""},
		}
		for _, test := range cases {
			response, err := r.client.List(s.ctx, test.req)
			if err != nil {
				return err
			}
			var ids []string
			for _, entry := range response.Entry {
				ids = append(ids, entry.Peer.Id)
			}
			if strings.Join(ids, ",") != test.ids {
				return fmt.Errorf("filter %v: %v; want %s", test.req, ids, test.ids)
			}
		}
		return nil
	})
	sc.Step(`^discovery responses contain no ownership credentials$`, func() error {
		response, err := r.client.List(s.ctx, &rv.ListRequest{})
		if err != nil {
			return err
		}
		entry, err := r.client.Lookup(s.ctx, &rv.LookupRequest{Id: "alice"})
		if err != nil {
			return err
		}
		for _, message := range []proto.Message{response, entry} {
			data, err := protojson.Marshal(message)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), "secret") || strings.Contains(string(data), "token") {
				return errors.New("discovery leaked ownership credentials")
			}
		}
		return nil
	})
	sc.Step(`^Alice submits metadata with "([^"]*)"$`, func(problem string) error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		request := proto.Clone(r.current).(*rv.RegisterRequest)
		request.ExpectedRevision = entry.Revision
		switch problem {
		case "invalid ID":
			request.Peer.Id = "../outside"
		case "relative address":
			request.Peer.Address = "127.0.0.1:8080"
		case "wildcard address":
			request.Peer.Address = "http://0.0.0.0:8080"
		case "zero port":
			request.Peer.Address = "http://127.0.0.1:0"
		case "credentials":
			request.Peer.Address = "http://user:password@example.test"
		case "address path":
			request.Peer.Address += "/api"
		case "address query":
			request.Peer.Address += "?query=x"
		case "address fragment":
			request.Peer.Address += "#fragment"
		case "duplicate skill":
			request.Peer.Skill = append(request.Peer.Skill, proto.Clone(request.Peer.Skill[0]).(*pv.Skill))
		case "empty skill ID":
			request.Peer.Skill[0].Id = ""
		case "blank skill name":
			request.Peer.Skill[0].Name = "   "
		default:
			return fmt.Errorf("unknown metadata problem %s", problem)
		}
		_, r.lastError = r.client.Register(s.ctx, request)
		return nil
	})
	sc.Step(`^Alice sends metadata exceeding the registry message limit$`, func() {
		req := proto.Clone(r.current).(*rv.RegisterRequest)
		req.Peer.Description = strings.Repeat("x", registry.MaxMessageBytes)
		_, r.lastError = r.client.Register(s.ctx, req)
	})
	sc.Step(`^a startup registration acknowledgement is lost$`, func() error {
		client, err := discovery.NewClient(r.server.URL)
		if err != nil {
			return err
		}
		r.drop.Store(true)
		session, err := client.Start(s.ctx, &pv.Peer{Id: "bob", Address: "http://127.0.0.1:8081", Skill: []*pv.Skill{{Id: "chat", Name: "Chat"}}})
		r.lastError = err
		if session != nil {
			session.Close()
			return errors.New("startup unexpectedly received its acknowledgement")
		}
		return nil
	})
	sc.Step(`^the failed startup leaves Bob known and offline$`, func() error {
		entry, err := r.lookup("bob")
		if err != nil {
			return err
		}
		if entry.Presence.State != rv.PresenceState_PRESENCE_STATE_OFFLINE {
			return fmt.Errorf("startup left presence %v", entry.Presence)
		}
		return nil
	})
	sc.Step(`^a runtime loses a metadata-update acknowledgement$`, func() error {
		client, err := discovery.NewClient(r.server.URL)
		if err != nil {
			return err
		}
		value := &pv.Peer{Id: "bob", Description: "old description", Address: "http://127.0.0.1:8081", Skill: []*pv.Skill{{Id: "chat", Name: "Chat"}}}
		r.reconciled = make(chan *pv.Peer, 1)
		r.session, err = client.Start(s.ctx, value, func(value *pv.Peer) {
			select {
			case r.reconciled <- value:
			default:
			}
		})
		if err != nil {
			return err
		}
		value.Description = "committed despite lost reply"
		r.drop.Store(true)
		r.lastError = r.session.Update(s.ctx, value)
		return nil
	})
	sc.Step(`^heartbeats reconcile the committed metadata without rolling it back$`, func() error {
		select {
		case value := <-r.reconciled:
			if value.Description != "committed despite lost reply" {
				return fmt.Errorf("reconciled incorrect metadata: %v", value)
			}
		case <-s.ctx.Done():
			return s.ctx.Err()
		}
		r.now.Add(int64(2 * time.Second))
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			entry, err := r.lookup("bob")
			if err != nil {
				return err
			}
			if entry.Peer.Description != "committed despite lost reply" {
				return errors.New("presence recovery rolled back committed metadata")
			}
			if entry.Presence.State == rv.PresenceState_PRESENCE_STATE_ONLINE {
				return nil
			}
			select {
			case <-ticker.C:
			case <-s.ctx.Done():
				return s.ctx.Err()
			}
		}
	})

	sc.Step(`^the operator attempts to forget online Alice$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		_, r.lastError = r.client.Forget(s.ctx, &rv.ForgetRequest{Id: "alice", ExpectedRevision: entry.Revision})
		return nil
	})
	sc.Step(`^the operator forgets Alice twice$`, func() error {
		entry, err := r.lookup("alice")
		if err != nil {
			return err
		}
		r.forget = &rv.ForgetRequest{Id: "alice", ExpectedRevision: entry.Revision}
		for i := 0; i < 2; i++ {
			if _, err := r.client.Forget(s.ctx, r.forget); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the operator retries the same retirement$`, func() error { _, err := r.client.Forget(s.ctx, r.forget); return err })
	sc.Step(`^Alice is absent from lookup and listing$`, func() error {
		_, err := r.lookup("alice")
		if connect.CodeOf(err) != connect.CodeNotFound {
			return fmt.Errorf("forgotten lookup = %v", err)
		}
		response, err := r.client.List(s.ctx, &rv.ListRequest{})
		if err != nil {
			return err
		}
		if len(response.Entry) != 0 {
			return errors.New("forgotten Peer is still listed")
		}
		return nil
	})
	sc.Step(`^the operator reads Alice before a metadata update$`, func() error {
		entry, err := r.lookup("alice")
		if err == nil {
			r.forget = &rv.ForgetRequest{Id: "alice", ExpectedRevision: entry.Revision}
		}
		return err
	})
	sc.Step(`^the operator attempts the earlier retirement$`, func() { _, r.lastError = r.client.Forget(s.ctx, r.forget) })
}
func (r *registryScenario) open() error {
	store, err := registry.Open(registry.Config{Path: filepath.Join(r.s.root, "registry-contract.db"), HeartbeatTimeout: time.Second, Now: func() time.Time { return time.Unix(0, r.now.Load()) }})
	if err != nil {
		return err
	}
	r.store = store
	base, err := registry.NewHandler(store)
	if err != nil {
		return err
	}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == registryv1connect.RegistryServiceRegisterProcedure && r.drop.CompareAndSwap(true, false) {
			recorder := httptest.NewRecorder()
			base.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				for key, values := range recorder.Header() {
					w.Header()[key] = values
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(recorder.Body.Bytes())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"unavailable","message":"fixture lost registration acknowledgement"}`))
			return
		}
		base.ServeHTTP(w, req)
	}))
	transport := connecthttp.NewTransport(http.DefaultClient, r.server.URL, connecthttp.WithSendMaxBytes(2<<20))
	r.client = registryv1connect.NewRegistryServiceClient(connect.NewClient(transport))
	return nil
}
func (r *registryScenario) close() error {
	if r.session != nil {
		r.session.Close()
		r.session = nil
	}
	if r.server != nil {
		r.server.Close()
		r.server = nil
	}
	if r.store != nil {
		err := r.store.Close()
		r.store = nil
		return err
	}
	return nil
}
func (r *registryScenario) lookup(id string) (*rv.Entry, error) {
	response, err := r.client.Lookup(r.s.ctx, &rv.LookupRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return response.Entry, nil
}
