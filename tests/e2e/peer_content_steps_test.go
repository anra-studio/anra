package e2e_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/anra-studio/anra/pkg/flow/borrow"

	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/model/anchor"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	peermodel "github.com/anra-studio/anra/pkg/model/peer"
	bv "github.com/anra-studio/anra/pkg/model/view"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const foreignContextAnchor = `{"schema":"example.org/context/v1","ownerEpoch":"foreign-schema-value","note":"approved context"}`

type peerContentScenario struct {
	s *scenario

	media []byte

	peer *pv.Peer

	response *rpcpb.ReadResponse

	ref *pb.Ref

	reads *readLog
}

func initializePeerContent(sc *godog.ScenarioContext, s *scenario) {
	p := &peerContentScenario{
		s: s,
	}

	sc.Step(`^节点存有大型多模态条目和两种锚点$`, p.setup)
	sc.Step(`^调用方为完整内容取得借阅 lease$`, s.borrow)
	sc.Step(`^授权 Reader 在消费前不读取正文且可取消$`, p.checkLazyReader)
	sc.Step(`^Connect 和 REST 接受超过默认消息大小的合法借阅请求$`, p.sendLargeBodies)
	sc.Step(`^两种 Peer 客户端的大读取请求到达服务端授权检查$`, p.sendLargeClientRequests)
	sc.Step(`^Connect、REST 和直接 Peer 客户端一次读取相同的完整多模态内容$`, p.readTransports)
	sc.Step(`^完整视图保留原生锚点公开内容并隐藏未知锚点内容$`, p.readAnchors)
	sc.Step(`^一次读取只访问授权条目且不重复读取$`, p.checkReadRanges)
	sc.Step(`^借出节点的存储和服务重建$`, p.restart)
	sc.Step(`^原 lease 仍可读取完整多模态条目$`, p.readRecovered)

	sc.Step(
		`^调用方释放该内容 lease$`,
		func() error {
			return s.closeGrant("released")
		},
	)

	sc.Step(`^该 lease 不再允许读取内容$`, s.unavailable)
}

func (p *peerContentScenario) setup() error {
	s := p.s
	var err error
	s.peerTape, err = s.openTape("large-context", "researcher-local")
	if err != nil {
		return err
	}

	turn, err := beginFixtureTurn(s.ctx, s.peerTape, "researcher-local")
	if err != nil {
		return err
	}
	// Valid raw PCM silence. Its JSON representation exceeds 4 MiB, so the test
	// also detects accidentally falling back to Connect's default response cap.
	p.media = make([]byte, (3<<20)+2)

	value, err := ae.FromEntry(te.NewEntry(te.WithEntryKind(te.EntryUser), te.WithEntryOwner("researcher-local"))).WithJSONExtension(
		ae.ADKEvent,
		session.Event{
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{
					Role: "user",
					Parts: []*genai.Part{{
						InlineData: &genai.Blob{
							MIMEType: "audio/L16;rate=16000;channels=1",
							Data:     p.media,
						},
						ThoughtSignature: []byte("PRIVATE-SIGNATURE"),

						PartMetadata: map[string]any{"private": "PRIVATE-METADATA"},
					}},
				},
			},

			Actions: session.EventActions{
				StateDelta: map[string]any{"private": "PRIVATE-STATE"},
			},
		},
	)
	if err != nil {
		return err
	}
	ctx := owner.WithOwnerId(s.ctx, "researcher-local")
	if _, err := s.peerTape.Store(ctx, value); err != nil {
		return err
	}

	first, err := finishFixtureTurn(s.ctx, s.peerTape, "researcher-local", turn)
	if err != nil {
		return err
	}

	next, err := beginFixtureTurn(s.ctx, s.peerTape, "researcher-local")
	if err != nil {
		return err
	}

	if _, err := s.peerTape.Store(ctx, te.NewAnchor(
		te.Seq{},
		"researcher-local",
		te.AnchorKindCustom,
		[]byte(foreignContextAnchor),
	)); err != nil {
		return err
	}

	if _, err := s.peerTape.Store(ctx, te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryOwner("researcher-local"), te.WithEntryContent("context complete"))); err != nil {
		return err
	}

	second, err := finishFixtureTurn(s.ctx, s.peerTape, "researcher-local", next)
	if err != nil {
		return err
	}

	p.ref = &pb.Ref{
		Source: first.Source,
		Scopes: []*pb.EntryRange{{
			SeqS: first.Scopes[0].SeqS,
			SeqE: te.MustParseSeq(first.Scopes[0].SeqE).Next().String(),
		}, second.Scopes[0]},
	}

	s.request = &pb.BorrowRequest{
		RequestId: "large-context",
		Purpose:   "review media and records",
		Query:     "complete context",
	}

	return p.start()
}

func (p *peerContentScenario) start() error {
	s := p.s
	var err error
	p.reads = &readLog{
		TapeStorage: s.peerTape,
	}

	s.grants, err = openGrantFixture(
		s.ctx,
		filepath.Join(s.root, "large.lease.db"),
		p.ref.Source,
		"researcher-local",
		p.reads,
		func() time.Time {
			return time.Unix(0, s.clock.Load())
		},
		false,
	)
	if err != nil {
		return err
	}
	s.service, err = s.grants.newService(
		borrow.Config{
			Source:   p.ref.Source,
			Tape:     s.grants.tape,
			Exporter: bv.ContextExporter{},

			Views: recoveredViews{
				Tape:     s.peerTape,
				Owner:    "researcher-local",
				Selected: p.ref,
			},
		},
	)
	if err != nil {
		return err
	}
	handler, err := a2a.NewViewHandler(s.service, a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	s.server = httptest.NewServer(handler)

	p.peer = &pv.Peer{
		Id:      "researcher",
		Address: s.server.URL,
		Skill: []*pv.Skill{{
			Id:   peermodel.BorrowContext,
			Name: "Borrow context",
		}},
	}

	s.card, err = a2a.Card(p.peer)
	if err != nil {
		return err
	}
	s.client, err = s.clientFor("borrower")
	return err
}

func (p *peerContentScenario) checkMedia(response *rpcpb.ReadResponse) error {
	view := p.s.decision.GetLease()
	quote := response.Quote

	if view == nil || quote.Provenance.LeaseId != view.Id || !proto.Equal(quote.Provenance.Ref, view.Ref) || len(quote.Entries) != 4 || quote.Entries[0].Seq != p.ref.Scopes[0].SeqS {
		return errors.New("large content lost grant provenance or complete scopes")
	}
	payload := quote.Entries[0].Payload
	if payload.GetMediaType() != a2a.ContextMediaType || len(payload.GetJsonData()) <= 4<<20 || bytes.Contains(payload.GetJsonData(), []byte("PRIVATE-")) || bytes.Contains(payload.GetJsonData(), []byte("thoughtSignature")) {
		return errors.New("large content was truncated, misidentified or exposed execution metadata")
	}
	var content genai.Content
	if err := json.Unmarshal(payload.JsonData, &content); err != nil {
		return err
	}
	if content.Role != "user" || len(content.Parts) != 1 || content.Parts[0].InlineData == nil || content.Parts[0].InlineData.MIMEType != "audio/L16;rate=16000;channels=1" || !bytes.Equal(content.Parts[0].InlineData.Data, p.media) {
		return errors.New("large multimodal body did not survive transport intact")
	}
	return nil
}

func (p *peerContentScenario) sendLargeBodies() error {
	s := p.s
	body, err := protojson.Marshal(s.request)
	if err != nil {
		return err
	}
	// Whitespace keeps the canonical request unchanged while making the actual
	// HTTP body larger than both the former limit and Connect's default limit.
	body = append(body, bytes.Repeat([]byte(" "), 5<<20)...)
	for _, path := range []string{a2a.ViewPath + "/Borrow", "/v1/tape/borrows"} {
		req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.server.URL+path, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Connect-Protocol-Version", "1")
		req.Header.Set(a2a.ExtensionHeader, a2a.ExtensionURI)
		response, err := s.httpClient("borrower").Do(req)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if response.StatusCode != http.StatusOK || response.Header.Get(a2a.ExtensionHeader) != a2a.ExtensionURI {
			return fmt.Errorf("large valid request to %s: HTTP %d %s", path, response.StatusCode, data)
		}

		var result rpcpb.BorrowResponse

		if err := protojson.Unmarshal(data, &result); err != nil {
			return err
		}
		if !proto.Equal(result.Decision, s.decision) {
			return errors.New("large transport body changed the idempotent grant")
		}
	}
	return nil
}

func (p *peerContentScenario) sendLargeClientRequests() error {
	s := p.s
	client, err := a2a.NewPeerClient(s.httpClient("borrower"), p.peer)
	if err != nil {
		return err
	}

	request := &rpcpb.ReadRequest{
		LeaseId: strings.Repeat("x", 5<<20),
	}

	for _, reader := range []*a2a.Client{s.client, client} {
		_, err := reader.Read(s.ctx, request)
		if err == nil {
			return errors.New("unknown large lease ID bypassed authorization")
		}
		if !errors.Is(err, a2a.ErrUnavailable) {
			return fmt.Errorf("large client request did not reach authorization: %w", err)
		}
		var remote *connect.Error
		if !errors.As(err, &remote) || remote.Code() != connect.CodeNotFound {
			return fmt.Errorf("large client request was rejected before reaching the server: %w", err)
		}
	}
	return nil
}

func (p *peerContentScenario) readTransports() error {
	s := p.s

	request := &rpcpb.ReadRequest{
		LeaseId: s.decision.Lease.Id,
	}

	quote, err := s.client.Read(s.ctx, request)
	if err != nil {
		return err
	}
	if err := p.checkMedia(quote); err != nil {
		return err
	}
	client, err := a2a.NewPeerClient(s.httpClient("borrower"), p.peer)
	if err != nil {
		return err
	}
	quote, err = client.Read(s.ctx, request)
	if err != nil {
		return err
	}
	if err := p.checkMedia(quote); err != nil {
		return err
	}

	var response rpcpb.ReadResponse

	rest := restClient{
		ctx:       s.ctx,
		base:      s.server.URL,
		identity:  "borrower",
		extension: true,
	}

	p.reads.reset()
	if err := rest.message(http.MethodGet, "/v1/tape/views/"+request.LeaseId+"/entries", nil, &response); err != nil {
		return err
	}

	wire, err := protojson.Marshal(&response)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire, &fields); err != nil {
		return err
	}
	var quoteFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["quote"], &quoteFields); err != nil {
		return err
	}
	if _, exists := quoteFields["nextCursor"]; exists {
		return errors.New("Quote must not carry a continuation")
	}
	if _, exists := fields["nextCursor"]; exists {
		return errors.New("ReadResponse must not carry a continuation")
	}
	if err := p.checkMedia(&response); err != nil {
		return err
	}
	p.response = &response
	return nil
}

func (p *peerContentScenario) readAnchors() error {
	s := p.s

	for index, seq := range []string{te.MustParseSeq(p.ref.Scopes[0].SeqS).Next().String(), p.ref.Scopes[1].SeqS, te.MustParseSeq(p.ref.Scopes[1].SeqS).Next().String()} {
		quote := p.response.Quote
		e := quote.Entries[index+1]
		if e.Seq != seq || quote.Provenance.LeaseId != s.decision.Lease.Id || !proto.Equal(quote.Provenance.Ref, s.decision.Lease.Ref) {
			return errors.New("complete content changed source, order or scope")
		}

		if index == 0 {

			var record anchor.BuiltIn
			if err := json.Unmarshal(e.Payload.JsonData, &record); err != nil {
				return err
			}

			if e.Payload.MediaType != a2a.AnchorMediaType || record.Schema != anchor.BuiltInSchema || record.Type != anchor.TurnClosed || record.Outcome != "completed" || record.TurnID == "" || bytes.Contains(e.Payload.JsonData, []byte("ownerEpoch")) {
				return errors.New("native anchor lost public fields or disclosed its ownership epoch")
			}

			stored, err := s.peerTape.Range(
				owner.WithOwnerId(s.ctx, "researcher-local"),
				tv.EntryRange{
					SeqS: te.MustParseSeq(seq),
					SeqE: te.MustParseSeq(seq).Next(),
				},
			)
			if err != nil {
				return err
			}
			if len(stored.Raw) != 1 {
				return errors.New("native anchor disappeared from storage")
			}
			value, err := ae.FromTape(stored.Raw[0])
			if err != nil {
				return err
			}
			native, err := anchor.Decode(value)
			if err != nil {
				return err
			}
			if native == nil {
				return errors.New("stored native anchor no longer decodes")
			}
			body := native.Export()
			encoded, err := json.Marshal(body)
			if err != nil {
				return err
			}
			if body.OwnerEpoch != "" || native.OwnerEpoch != "PRIVATE-EPOCH" || !bytes.Equal(encoded, e.Payload.JsonData) {
				return errors.New("native export changed the source record or differs from the peer export")
			}
		}

		if index == 1 {
			if e.Payload.MediaType != a2a.AnchorMediaType || string(e.Payload.JsonData) != "null" {
				return errors.New("unknown anchor content must be empty")
			}
		}

		if index == 2 && !exportedTextIs(e, "context complete") {
			return errors.New("complete read lost the final model content")
		}
	}
	return nil
}

func (p *peerContentScenario) restart() error {
	if err := p.s.stopPeer(); err != nil {
		return err
	}
	var err error
	p.s.peerTape, err = p.s.openTape("large-context", "researcher-local")
	if err != nil {
		return err
	}
	return p.start()
}

func (p *peerContentScenario) readRecovered() error {
	s := p.s
	decision, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if !proto.Equal(decision, s.decision) {
		return errors.New("storage restart changed the existing lease")
	}

	quote, err := s.client.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId: decision.Lease.Id,
		},
	)
	if err != nil {
		return err
	}
	return p.checkMedia(quote)
}
