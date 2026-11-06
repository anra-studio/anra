package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"connectrpc.com/connect/v2"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

func initializeSharedView(sc *godog.ScenarioContext, m *mcpScenario) {
	sc.Step(
		`^已提交包含空 Parts 条目的 MCP 交接并签发凭证$`,
		func() error {
			return m.setup("empty parts")
		},
	)
	sc.Step(
		`^Quote 保留该空 Parts 条目的角色和序号$`,
		func() error {
			if err := m.checkWindowResponse(); err != nil {
				return err
			}
			var quote mcpWindow
			if err := json.Unmarshal(m.lastResult, &quote); err != nil {
				return err
			}
			if len(quote.Entries) != 1 || quote.Entries[0].Seq != m.lease.Ref.Scopes[0].SeqS || quote.Entries[0].Kind != "assistant" {
				return errors.New("empty Parts entry was removed or changed")
			}
			var content genai.Content
			if err := json.Unmarshal(quote.Entries[0].Content, &content); err != nil {
				return err
			}
			if content.Role != "model" || len(content.Parts) != 0 {
				return errors.New("empty Parts content changed")
			}
			return nil
		},
	)

	sc.Step(
		`^已提交两段非相邻内容并签发共享 lease 凭证$`,
		func() error {
			return m.setup("multi-scope")
		},
	)
	sc.Step(`^Quote 完整保留两段内容且不包含间隙$`, m.checkMultiScopeQuote)
	sc.Step(`^其他调用方均不能通过 MCP 或 peer 读取该 lease$`, m.checkSharedIntruder)
	sc.Step(`^peer 拒绝改写完整 Scopes 的借阅请求$`, m.checkAlteredScopes)
	sc.Step(`^通过 "([^"]+)" 终止共享 lease$`, m.endSharedLease)
	sc.Step(`^peer 也拒绝读取该共享 lease$`, m.checkSharedUnavailable)
	sc.Step(
		`^角色读取仍遵守独立授权$`,
		func() error {
			_, err := m.success(anramcp.ReadRole)
			return err
		},
	)
}

func (m *mcpScenario) checkMultiScopeQuote() error {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var quote mcpWindow
	if err := json.Unmarshal(m.lastResult, &quote); err != nil {
		return err
	}
	if len(quote.Provenance.Ref.Scopes) != 2 || len(quote.Entries) != 4 {
		return errors.New("multi-scope quote lost an authorized segment")
	}
	index := 0
	for _, scope := range m.lease.Ref.Scopes {
		for next, end := te.MustParseSeq(scope.SeqS), te.MustParseSeq(scope.SeqE); next.Cmp(end) < 0; next = next.Next() {
			if quote.Entries[index].Seq != next.String() {
				return fmt.Errorf("unexpected sequence in quote: %s", quote.Entries[index].Seq)
			}
			index++
		}
	}
	return nil
}

func (m *mcpScenario) peerREST() (restClient, func(), error) {
	handler, err := a2a.NewViewHandler(m.views, a2a.TrustedPeerIdentity)
	if err != nil {
		return restClient{}, nil, err
	}
	server := httptest.NewServer(handler)
	return restClient{
		ctx:       m.s.ctx,
		base:      server.URL,
		identity:  m.grant.Caller,
		extension: true,
	}, server.Close, nil
}

func (m *mcpScenario) checkSharedIntruder() error {
	grant := m.grant
	grant.Caller = "intruder"
	if err := m.call(anramcp.ReadTapeView, m.authorize(grant), grant.AnchorSeq, nil); err != nil {
		return err
	}
	if err := m.rejected("permission_denied"); err != nil {
		return err
	}
	client, closeServer, err := m.peerREST()
	if err != nil {
		return err
	}
	defer closeServer()
	client.identity = "intruder"
	status, body, err := client.call(http.MethodGet, "/v1/tape/views/"+m.lease.Id+"/entries", nil)
	if err != nil {
		return err
	}
	return checkRESTError(status, body, http.StatusNotFound, connect.CodeNotFound)
}

func (m *mcpScenario) checkAlteredScopes() error {
	client, closeServer, err := m.peerREST()
	if err != nil {
		return err
	}
	defer closeServer()
	ref := proto.Clone(m.lease.Ref).(*pb.Ref)
	ref.Scopes[0], ref.Scopes[1] = ref.Scopes[1], ref.Scopes[0]
	var response rpcpb.BorrowResponse
	if err := client.message(
		http.MethodPost,
		"/v1/tape/borrows",
		&pb.BorrowRequest{
			RequestId: "altered-scopes",
			Purpose:   "changed scopes",
			Query:     "must be denied",
			View:      ref,
		},
		&response,
	); err != nil {
		return err
	}
	if response.Decision.Status != a2a.Denied || response.Decision.Lease != nil {
		return errors.New("peer authorized reordered scopes")
	}
	return nil
}

func (m *mcpScenario) endSharedLease(method string) error {
	switch method {
	case "peer释放":
		client, closeServer, err := m.peerREST()
		if err != nil {
			return err
		}
		defer closeServer()
		var response rpcpb.ReleaseResponse
		if err := client.message(
			http.MethodPost,
			"/v1/tape/views/"+m.lease.Id+":release",
			&rpcpb.ReleaseRequest{},
			&response,
		); err != nil {
			return err
		}
		if !response.Released {
			return errors.New("peer release was not acknowledged")
		}
		return nil
	case "本地撤销":
		return m.views.Revoke(m.s.ctx, m.grant.Caller, m.lease.Id)
	case "lease到期":
		m.clock.Add(int64(2 * time.Hour))
		return nil
	}
	return fmt.Errorf("unknown closure %q", method)
}

func (m *mcpScenario) checkSharedUnavailable() error {
	client, closeServer, err := m.peerREST()
	if err != nil {
		return err
	}
	defer closeServer()
	status, body, err := client.call(http.MethodGet, "/v1/tape/views/"+m.lease.Id+"/entries", nil)
	if err != nil {
		return err
	}
	return checkRESTError(status, body, http.StatusNotFound, connect.CodeNotFound)
}
