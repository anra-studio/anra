package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/cucumber/godog"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	rv "github.com/anra-studio/anra/gen/anra/registry/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/discovery"
	"github.com/anra-studio/anra/pkg/registry"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// REST calls use the public HTTP routes on application-hosted servers: the real
// lifecycle, validation, storage, and grants are shared with their Connect APIs.
func initializeHTTP(sc *godog.ScenarioContext, a *applicationScenario) {
	var grant *pb.Decision
	var endpoint string
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		grant, endpoint = nil, ""
		return ctx, nil
	})
	sc.Step(`^REST and Connect share the registry lifecycle$`, func() error {
		rest := restClient{ctx: a.cli.s.ctx, base: a.directory}
		request := &rv.RegisterRequest{Peer: &pv.Peer{Id: "rest-peer", Address: "http://127.0.0.1:8080", Description: "REST researcher", Skill: []*pv.Skill{{Id: "research", Name: "Research"}}}, RuntimeId: "rest-runtime", Token: "rest-owner-secret"}
		var registered rv.RegisterResponse
		if err := rest.message(http.MethodPost, "/v1/registry/peers:register", request, &registered); err != nil {
			return err
		}
		directory, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		record, err := directory.Lookup(a.cli.s.ctx, request.Peer.Id)
		if err != nil {
			return err
		}
		var lookup rv.LookupResponse
		if err := rest.message(http.MethodGet, "/v1/registry/peers/rest-peer", nil, &lookup); err != nil {
			return err
		}
		if !proto.Equal(record, registered.Entry) || !proto.Equal(lookup.Entry, record) {
			return errors.New("REST registration and Connect discovery disagree")
		}
		var list rv.ListResponse
		if err := rest.message(http.MethodGet, "/v1/registry/peers?state=PRESENCE_STATE_ONLINE&skill=research", nil, &list); err != nil {
			return err
		}
		if len(list.Entry) != 1 || !proto.Equal(list.Entry[0], record) {
			return fmt.Errorf("REST filters lost metadata: %v", &list)
		}
		wire, _ := protojson.Marshal(&list)
		if strings.Contains(string(wire), request.Token) || strings.Contains(string(wire), `"token"`) {
			return errors.New("REST discovery leaked ownership credentials")
		}
		var heartbeat rv.HeartbeatResponse
		if err := rest.message(http.MethodPost, "/v1/registry/peers/rest-peer:heartbeat", &rv.HeartbeatRequest{RuntimeId: request.RuntimeId, Token: request.Token}, &heartbeat); err != nil {
			return err
		}
		if !proto.Equal(heartbeat.Entry.Peer, request.Peer) {
			return errors.New("REST heartbeat changed metadata")
		}
		if err := rest.message(http.MethodPost, "/v1/registry/peers/rest-peer:disconnect", &rv.DisconnectRequest{RuntimeId: request.RuntimeId, Token: request.Token}, &rv.DisconnectResponse{}); err != nil {
			return err
		}
		record, err = directory.Lookup(a.cli.s.ctx, request.Peer.Id)
		if err != nil || record.Presence.State != rv.PresenceState_PRESENCE_STATE_OFFLINE {
			return fmt.Errorf("REST disconnect was not visible over Connect: %v %v", record, err)
		}
		if err := rest.message(http.MethodDelete, "/v1/registry/peers/rest-peer?expectedRevision="+url.QueryEscape(record.Revision), nil, &rv.ForgetResponse{}); err != nil {
			return err
		}
		if _, err := directory.Lookup(a.cli.s.ctx, request.Peer.Id); connect.CodeOf(err) != connect.CodeNotFound {
			return fmt.Errorf("REST retirement missing from Connect: %v", err)
		}
		status, body, err := rest.call(http.MethodGet, "/v1/registry/peers/rest-peer", nil)
		if err != nil {
			return err
		}
		return checkRESTError(status, body, http.StatusNotFound, connect.CodeNotFound)
	})
	sc.Step(`^the registry rejects invalid REST input "([^"]*)"$`, func(problem string) error {
		rest := restClient{ctx: a.cli.s.ctx, base: a.directory}
		method, path := http.MethodPost, "/v1/registry/peers:register"
		data := []byte(`{"peer":{"id":"rest-peer","address":"http://127.0.0.1:8080"},"runtimeId":"rest-runtime","token":"rest-owner-secret"}`)
		status, code := http.StatusBadRequest, connect.CodeInvalidArgument
		switch problem {
		case "missing runtime":
			data = bytes.ReplaceAll(data, []byte(`"rest-runtime"`), []byte(`""`))
		case "wrong JSON type":
			data = bytes.ReplaceAll(data, []byte(`"rest-peer"`), []byte(`123`))
		case "unknown JSON field":
			data = []byte(`{"unknownField":true}`)
		case "unknown query":
			method, path, data = http.MethodGet, "/v1/registry/peers?unknownField=true", nil
		case "invalid enum":
			method, path, data = http.MethodGet, "/v1/registry/peers?state=99", nil
		case "oversized body":
			data = bytes.ReplaceAll(data, []byte(`"rest-peer"`), []byte(`"`+strings.Repeat("x", registry.MaxMessageBytes)+`"`))
			status, code = http.StatusTooManyRequests, connect.CodeResourceExhausted
		case "wrong method":
			method, status, code = http.MethodPut, http.StatusMethodNotAllowed, connect.CodeUnimplemented
		default:
			return fmt.Errorf("unknown REST problem %q", problem)
		}
		got, body, err := rest.call(method, path, data)
		if err != nil {
			return err
		}
		if err := checkRESTError(got, body, status, code); err != nil {
			return err
		}
		directory, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		entries, err := directory.List(a.cli.s.ctx)
		if err != nil {
			return err
		}
		if len(entries) != 0 {
			return fmt.Errorf("invalid REST request changed the registry: %v", entries)
		}
		return nil
	})
	sc.Step(`^Bob borrows Alice's committed TapeView through REST$`, func() error {
		directory, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		record, err := directory.Lookup(a.cli.s.ctx, "alice")
		if err != nil {
			return err
		}
		endpoint = record.Peer.Address
		rest := restClient{ctx: a.cli.s.ctx, base: endpoint, identity: "bob", extension: true}
		var response pb.BorrowResponse
		if err := rest.message(http.MethodPost, "/v1/tape/borrows", &pb.BorrowRequest{RequestId: "rest-borrow", Purpose: "review findings", Query: "hello"}, &response); err != nil {
			return err
		}
		grant = response.Decision
		if grant == nil || grant.Status != a2a.Granted || grant.View == nil {
			return fmt.Errorf("REST borrow was not granted: %v", &response)
		}
		var current pb.GetResponse
		if err := rest.message(http.MethodGet, "/v1/tape/borrows/rest-borrow", nil, &current); err != nil {
			return err
		}
		if !proto.Equal(current.Decision, grant) {
			return errors.New("REST Get changed the grant")
		}
		client, err := a2a.NewPeerClient(a2a.TrustedHTTPClient("bob"), record.Peer)
		if err != nil {
			return err
		}
		decision, err := client.Get(a.cli.s.ctx, "rest-borrow")
		if err != nil || !proto.Equal(decision, grant) {
			return fmt.Errorf("Connect Get disagrees with REST: %v %v", decision, err)
		}
		pages, err := client.ReadView(a.cli.s.ctx, grant.View)
		if err != nil {
			return err
		}
		if len(pages) != 1 || len(pages[0].Entries) != 2 {
			return fmt.Errorf("unexpected committed conversation: %v", pages)
		}
		cursor := ""
		for i, expected := range []string{"hello", "alice: hello"} {
			var response pb.ReadResponse
			path := "/v1/tape/views/" + grant.View.ViewId + "/entries?pageSize=1&cursor=" + url.QueryEscape(cursor)
			if err := rest.message(http.MethodGet, path, nil, &response); err != nil {
				return err
			}
			page := response.Page
			if page == nil || page.ViewId != grant.View.ViewId || page.SnapshotHead != grant.View.SnapshotHead || !proto.Equal(page.Source, grant.View.Source) || len(page.Entries) != 1 || page.Entries[0].Summary != expected || !proto.Equal(page.Entries[0], pages[0].Entries[i]) {
				return fmt.Errorf("REST pagination disagrees with granted context: %v", page)
			}
			cursor = page.NextCursor
			if (i == 0) != (cursor != "") {
				return errors.New("incorrect REST page cursor")
			}
		}
		return nil
	})
	sc.Step(`^TapeView REST enforces identity, extension negotiation, and requester-bound grants$`, func() error {
		path := "/v1/tape/views/" + grant.View.ViewId + "/entries"
		for _, attempt := range []struct {
			identity  string
			extension bool
			status    int
			code      connect.Code
		}{
			{"", true, http.StatusUnauthorized, connect.CodeUnauthenticated},
			{"../mallory", true, http.StatusUnauthorized, connect.CodeUnauthenticated},
			{"bob", false, http.StatusBadRequest, connect.CodeFailedPrecondition},
			{"mallory", true, http.StatusNotFound, connect.CodeNotFound},
		} {
			rest := restClient{ctx: a.cli.s.ctx, base: endpoint, identity: attempt.identity, extension: attempt.extension}
			status, body, err := rest.call(http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			if err := checkRESTError(status, body, attempt.status, attempt.code); err != nil {
				return err
			}
		}
		rest := restClient{ctx: a.cli.s.ctx, base: endpoint, identity: "mallory", extension: true}
		status, body, err := rest.call(http.MethodPost, "/v1/tape/views/"+grant.View.ViewId+":release", []byte(`{}`))
		if err != nil {
			return err
		}
		if err := checkRESTError(status, body, http.StatusNotFound, connect.CodeNotFound); err != nil {
			return err
		}
		rest.identity = "bob"
		status, body, err = rest.call(http.MethodGet, path+"?pageSize=101", nil)
		if err != nil {
			return err
		}
		return checkRESTError(status, body, http.StatusBadRequest, connect.CodeInvalidArgument)
	})
	sc.Step(`^Bob releases his REST grant and both transports deny further reads$`, func() error {
		rest := restClient{ctx: a.cli.s.ctx, base: endpoint, identity: "bob", extension: true}
		var response pb.ReleaseResponse
		if err := rest.message(http.MethodPost, "/v1/tape/views/"+grant.View.ViewId+":release", &pb.ReleaseRequest{}, &response); err != nil {
			return err
		}
		if !response.Released {
			return errors.New("REST release was not acknowledged")
		}
		status, body, err := rest.call(http.MethodGet, "/v1/tape/views/"+grant.View.ViewId+"/entries", nil)
		if err != nil {
			return err
		}
		if err := checkRESTError(status, body, http.StatusNotFound, connect.CodeNotFound); err != nil {
			return err
		}
		directory, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		record, err := directory.Lookup(a.cli.s.ctx, "alice")
		if err != nil {
			return err
		}
		client, err := a2a.NewPeerClient(a2a.TrustedHTTPClient("bob"), record.Peer)
		if err != nil {
			return err
		}
		_, err = client.ReadView(a.cli.s.ctx, grant.View)
		if !errors.Is(err, a2a.ErrUnavailable) {
			return fmt.Errorf("Connect read released REST grant: %v", err)
		}
		return nil
	})
}

type restClient struct {
	ctx            context.Context
	base, identity string
	extension      bool
}

func (c restClient) call(method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.identity != "" {
		req.Header.Set(a2a.PeerIdentityHeader, c.identity)
	}
	if c.extension {
		req.Header.Set(a2a.ExtensionHeader, a2a.ExtensionURI)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}
	if c.extension && resp.StatusCode == http.StatusOK && resp.Header.Get(a2a.ExtensionHeader) != a2a.ExtensionURI {
		return 0, nil, errors.New("REST response omitted negotiated TapeView extension")
	}
	return resp.StatusCode, data, nil
}

func (c restClient) message(method, path string, input, output proto.Message) error {
	var body []byte
	var err error
	if input != nil {
		body, err = protojson.Marshal(input)
	}
	if err != nil {
		return err
	}
	status, data, err := c.call(method, path, body)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("REST %s %s: HTTP %d %s", method, path, status, data)
	}
	return protojson.Unmarshal(data, output)
}

func checkRESTError(status int, body []byte, want int, code connect.Code) error {
	var response struct {
		Code    uint32 `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("REST HTTP %d: invalid status JSON %s: %w", status, body, err)
	}
	if status != want || response.Code != uint32(code) || response.Message == "" {
		return fmt.Errorf("REST error HTTP %d %s; want HTTP %d %s", status, body, want, code)
	}
	return nil
}
