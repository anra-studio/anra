package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/model/anchor"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	peermodel "github.com/anra-studio/anra/pkg/model/peer"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

const foreignContextAnchor = `{"schema":"example.org/context/v1","ownerEpoch":"foreign-schema-value","note":"approved context"}`

type peerContentScenario struct {
	s      *scenario
	media  []byte
	peer   *pv.Peer
	cursor string
}

func initializePeerContent(sc *godog.ScenarioContext, s *scenario) {
	p := &peerContentScenario{s: s}
	sc.Step(`^节点存有大型多模态条目和两种锚点$`, p.setup)
	sc.Step(`^调用方为完整内容取得借阅 lease$`, s.borrow)
	sc.Step(`^Connect、REST 和直接 Peer 客户端读取相同的完整多模态条目$`, p.readTransports)
	sc.Step(`^其余分页保留原生和第三方锚点的约定表示$`, p.readAnchors)
	sc.Step(`^其他调用方不能查询读取或释放该 lease$`, s.checkIntruder)
	sc.Step(`^借出节点的存储和服务重建$`, p.restart)
	sc.Step(`^原 lease 仍可读取完整多模态条目$`, p.readRecovered)
	sc.Step(`^调用方释放该内容 lease$`, func() error { return s.closeGrant("released") })
	sc.Step(`^该 lease 不再允许读取内容$`, s.unavailable)
}

func (p *peerContentScenario) setup() error {
	s := p.s
	var err error
	s.peerTape, err = s.openTape("large-context", "researcher-local")
	if err != nil {
		return err
	}
	// Valid raw PCM silence. Its JSON representation exceeds 4 MiB, so the test
	// also detects accidentally falling back to Connect's default response cap.
	p.media = make([]byte, (3<<20)+2)
	value, err := ae.FromTape(te.NewEntry(te.WithEntryKind(te.EntryUser), te.WithEntryOwner("researcher-local"))).WithJSONExtension(ae.ADKEvent, session.Event{
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "user", Parts: []*genai.Part{{
			InlineData: &genai.Blob{MIMEType: "audio/L16;rate=16000;channels=1", Data: p.media}, ThoughtSignature: []byte("PRIVATE-SIGNATURE"),
			PartMetadata: map[string]any{"private": "PRIVATE-METADATA"},
		}}}},
		Actions: session.EventActions{StateDelta: map[string]any{"private": "PRIVATE-STATE"}},
	})
	if err != nil {
		return err
	}
	ctx := owner.WithOwnerId(s.ctx, "researcher-local")
	if _, err := s.peerTape.Store(ctx, value); err != nil {
		return err
	}
	native, err := json.Marshal(anchor.Record{Schema: anchor.Schema, Type: anchor.TurnStarted, TurnID: "context-turn", OwnerEpoch: "PRIVATE-EPOCH"})
	if err != nil {
		return err
	}
	for _, data := range [][]byte{native, []byte(foreignContextAnchor)} {
		if _, err := s.peerTape.Store(ctx, te.NewAnchor(te.Seq{}, "researcher-local", te.AnchorKindCustom, data)); err != nil {
			return err
		}
	}
	if _, err := s.peerTape.Store(ctx, te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryOwner("researcher-local"), te.WithEntryContent("context complete"))); err != nil {
		return err
	}
	s.request = &pb.BorrowRequest{RequestId: "large-context", Purpose: "review media and records", Query: "complete context"}
	return p.start()
}

func (p *peerContentScenario) start() error {
	s := p.s
	var err error
	s.service, err = a2a.NewService(a2a.Config{
		Source: &pb.Source{AgentId: "researcher", TapeId: "large-context"}, Owner: "researcher-local", Tape: s.peerTape,
		JournalPath: filepath.Join(s.root, "large-grants.db"), Exporter: a2a.ContextExporter{},
		Now: func() time.Time { return time.Unix(0, s.clock.Load()) },
		Policy: a2a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a2a.Selection, error) {
			return a2a.Selection{Status: a2a.Granted, TTL: time.Hour, Range: &tv.EntryRange{SeqS: te.SeqFromUint64(1), SeqE: te.SeqFromUint64(5)}}, nil
		}),
	})
	if err != nil {
		return err
	}
	handler, err := a2a.NewViewHandler(s.service, a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	s.server = httptest.NewServer(handler)
	p.peer = &pv.Peer{Id: "researcher", Address: s.server.URL, Skill: []*pv.Skill{{Id: peermodel.BorrowContext, Name: "Borrow context"}}}
	s.card, err = a2a.Card(p.peer)
	if err != nil {
		return err
	}
	s.client, err = s.clientFor("borrower")
	return err
}

func (p *peerContentScenario) checkMedia(page *pb.Page) error {
	view := p.s.decision.GetView()
	if view == nil || page.ViewId != view.ViewId || !proto.Equal(page.Source, view.Source) || page.SnapshotHead != view.SnapshotHead || len(page.Entries) != 1 || page.Entries[0].Seq != "1" || page.NextCursor == "" {
		return errors.New("large content lost grant provenance or pagination boundaries")
	}
	payload := page.Entries[0].Payload
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

func (p *peerContentScenario) readTransports() error {
	s := p.s
	request := &pb.ReadRequest{ViewId: s.decision.View.ViewId, PageSize: 1}
	page, err := s.client.Read(s.ctx, request)
	if err != nil {
		return err
	}
	if err := p.checkMedia(page); err != nil {
		return err
	}
	p.cursor = page.NextCursor
	client, err := a2a.NewPeerClient(s.httpClient("borrower"), p.peer)
	if err != nil {
		return err
	}
	page, err = client.Read(s.ctx, request)
	if err != nil {
		return err
	}
	if err := p.checkMedia(page); err != nil {
		return err
	}
	var response pb.ReadResponse
	rest := restClient{ctx: s.ctx, base: s.server.URL, identity: "borrower", extension: true}
	if err := rest.message(http.MethodGet, "/v1/tape/views/"+request.ViewId+"/entries?pageSize=1", nil, &response); err != nil {
		return err
	}
	return p.checkMedia(response.Page)
}

func (p *peerContentScenario) readAnchors() error {
	s := p.s
	for seq := 2; seq <= 4; seq++ {
		page, err := s.client.Read(s.ctx, &pb.ReadRequest{ViewId: s.decision.View.ViewId, Cursor: p.cursor, PageSize: 1})
		if err != nil {
			return err
		}
		if len(page.Entries) != 1 || page.Entries[0].Seq != fmt.Sprint(seq) || page.ViewId != s.decision.View.ViewId || page.SnapshotHead != s.decision.View.SnapshotHead || !proto.Equal(page.Source, s.decision.View.Source) {
			return errors.New("content pagination changed source, order or scope")
		}
		e := page.Entries[0]
		if seq == 2 {
			var record anchor.Record
			if err := json.Unmarshal(e.Payload.JsonData, &record); err != nil {
				return err
			}
			if e.Payload.MediaType != a2a.AnchorMediaType || record.Schema != anchor.Schema || record.Type != anchor.TurnStarted || record.TurnID != "context-turn" || bytes.Contains(e.Payload.JsonData, []byte("ownerEpoch")) {
				return errors.New("native anchor lost public fields or disclosed its ownership epoch")
			}
		}
		if seq == 3 {
			var body string
			if err := json.Unmarshal(e.Payload.JsonData, &body); err != nil {
				return err
			}
			if e.Payload.MediaType != a2a.AnchorMediaType || body != foreignContextAnchor {
				return errors.New("foreign anchor body was changed by native redaction")
			}
		}
		if seq == 4 && !exportedTextIs(e, "context complete") {
			return errors.New("paging lost the final model content")
		}
		p.cursor = page.NextCursor
		if (seq == 4) != (p.cursor == "") {
			return errors.New("content paging stopped early or escaped its grant")
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
	page, err := s.client.Read(s.ctx, &pb.ReadRequest{ViewId: decision.View.ViewId, PageSize: 1})
	if err != nil {
		return err
	}
	return p.checkMedia(page)
}
