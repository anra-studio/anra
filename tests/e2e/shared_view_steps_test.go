package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect/v2"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func initializeSharedView(sc *godog.ScenarioContext, m *mcpScenario) {
	sc.Step(
		`^已提交两段非相邻内容并签发共享 lease 凭证$`,
		func() error {
			return m.setup("multi-scope")
		},
	)
	sc.Step(
		`^原生多段 View 只持久化一条完整授权记录$`,
		func() error {
			records, _, err := m.grants.decisions(m.s.ctx)
			if err != nil {
				return err
			}
			grants := 0
			for _, record := range records {
				var stored struct {
					Decision json.RawMessage `json:"decision"`
				}
				if err := json.Unmarshal([]byte(record.GetSummary()), &stored); err != nil {
					return err
				}
				var decision pb.Decision
				if err := protojson.Unmarshal(stored.Decision, &decision); err != nil {
					return err
				}
				if decision.GetLease().GetId() != m.lease.Id {
					continue
				}
				grants++
				if !proto.Equal(decision.Lease.Ref, m.lease.Ref) || len(decision.Lease.Ref.Scopes) != 2 {
					return errors.New("native multi-view grant lost part of the committed Ref")
				}
			}
			if grants != 1 {
				return fmt.Errorf("native multi-view operation persisted %d grants, want 1", grants)
			}
			return nil
		},
	)
	sc.Step(`^Quote 完整保留两段内容且不包含间隙$`, m.checkMultiScopeQuote)
	sc.Step(`^其他调用方均不能通过 MCP 或 peer 读取该 lease$`, m.checkSharedIntruder)
	sc.Step(`^peer 拒绝改写完整 Scopes 的借阅请求$`, m.checkAlteredScopes)
	sc.Step(`^通过 peer 释放共享 lease$`, m.releaseSharedLease)
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
	handler, err := a2a.NewViewHandler(m.borrowing, a2a.TrustedPeerIdentity)
	if err != nil {
		return restClient{}, nil, err
	}
	server := httptest.NewServer(handler)
	return restClient{
		ctx:       m.s.ctx,
		base:      server.URL,
		identity:  m.credential.Caller,
		extension: true,
	}, server.Close, nil
}

func (m *mcpScenario) checkSharedIntruder() error {
	grant := m.credential
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

func (m *mcpScenario) releaseSharedLease() error {
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
