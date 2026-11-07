package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	a2a "github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	bv "github.com/anra-studio/anra/pkg/model/view"
	"github.com/cucumber/godog"
	"github.com/scbizu/tape-go/pkg/runner"
	entry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Only the model and permission decision are deterministic fixtures. Agent,
// tape, journal, discovery, SDK, Connect, and HTTP all use real implementations.
type scenario struct {
	root string

	ctx context.Context

	cancel context.CancelFunc

	localTape *bbolt.Bbolt
	peerTape  *bbolt.Bbolt

	coreTape *bbolt.Bbolt

	service *bv.Service

	server *httptest.Server

	modelServer *httptest.Server

	providerControl *providerControl

	card *sdk.AgentCard

	client *a2a.Client

	ordinary *a2aclient.Client

	request *pb.BorrowRequest

	decision *pb.Decision

	firstPage *rpcpb.ReadResponse

	lastRequest runner.Request

	answer string

	lastError error

	wireCode string

	pending atomic.Bool

	findingCount atomic.Uint64

	clock atomic.Int64
}

func initializeScenario(sc *godog.ScenarioContext) {
	s := &scenario{}
	initializeCLI(sc, s)
	initializeRegistry(sc, s)
	initializeCollaboration(sc, s)

	sc.Before(
		func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			root, err := os.MkdirTemp("", "anra-e2e-")
			if err != nil {
				return ctx, err
			}

			*s = scenario{
				root: root,
			}

			s.providerControl = newProviderControl()

			s.modelServer = newModelServer(s.providerControl)

			s.ctx, s.cancel = context.WithTimeout(ctx, 20*time.Second)

			s.clock.Store(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).UnixNano())

			return s.ctx, nil
		},
	)

	sc.After(
		func(ctx context.Context, sc *godog.Scenario, scenarioErr error) (context.Context, error) {
			err := s.stopPeer()

			if s.modelServer != nil {

				s.providerControl.unblock()

				s.modelServer.Close()

			}

			if s.localTape != nil {
				err = errors.Join(err, s.localTape.Close())
			}

			if s.cancel != nil {
				s.cancel()
			}

			if failure := errors.Join(scenarioErr, err); failure != nil && os.Getenv("ANRA_E2E_ARTIFACTS") != "" {
				if saveErr := s.saveFailure(sc.Name, failure); saveErr != nil {
					return ctx, errors.Join(err, saveErr)
				}
			}

			return ctx, errors.Join(err, os.RemoveAll(s.root))
		},
	)

	sc.Step(`^an agent with its own tape$`, s.openLocalTape)

	sc.Step(
		`^I create an agent with an empty role$`,
		func() error {
			a, err := agent.New("", s.localTape)

			s.lastError = err

			if a != nil {
				return errors.Join(errors.New("empty role created an agent"), a.Shutdown(s.ctx))
			}

			return nil
		},
	)

	sc.Step(
		`^agent creation fails with "([^"]*)"$`,
		func(want string) error {
			if s.lastError == nil || s.lastError.Error() != want {
				return fmt.Errorf("agent creation error = %v, want %q", s.lastError, want)
			}

			return nil
		},
	)

	sc.Step(`^I ask "([^"]*)" and the model answers "([^"]*)"$`, s.ask)
	sc.Step(`^I close and reopen the agent's storage$`, s.reopenLocalTape)
	sc.Step(`^another member uses the same database for a private conversation$`, s.otherMember)
	sc.Step(`^the model sees the conversation in order$`, s.conversation)
	sc.Step(`^the answer is "([^"]*)"$`, s.answerIs)
	sc.Step(`^研究节点已提交两份发现并持久化授权记录$`, s.setupPeer)
	sc.Step(`^调用方发现研究节点并发送 "([^"]*)"$`, s.sendMessage)
	sc.Step(`^节点回答 "([^"]*)"$`, s.answerIs)
	sc.Step(`^节点也能流式回答 "([^"]*)"$`, s.streamMessage)
	sc.Step(`^研究者节点提供两条发现和持久化授权$`, s.setupPeer)
	sc.Step(`^本地角色借阅研究者上下文$`, s.borrowWithAgent)
	sc.Step(`^借阅回答为 "([^"]*)"$`, s.answerIs)
	sc.Step(`^模型仅在带来源凭据的借阅工具结果中收到上下文$`, s.checkBorrowedContext)
	sc.Step(`^借阅角色的存储重启$`, s.reopenLocalTape)
	sc.Step(`^模型仍以借阅工具结果回忆上下文$`, s.recallBorrowedContext)
	sc.Step(`^所有持久化条目仍归借阅角色所有$`, s.checkLocalOwnership)
	sc.Step(`^调用方请求借阅已提交视图$`, s.borrow)
	sc.Step(`^借阅决策为 "([^"]*)"$`, s.decisionIs)
	sc.Step(`^调用方读取第一条发现$`, s.readFirst)

	sc.Step(
		`^研究节点在授权后提交新的发现$`,
		func() error {
			return s.appendFinding("new finding outside the snapshot")
		},
	)

	sc.Step(`^研究节点重启存储和服务$`, s.restartPeer)
	sc.Step(`^重试同一请求仍返回原 lease$`, s.retryGrant)
	sc.Step(`^调用方分页读完原有授权视图$`, s.finishSnapshot)
	sc.Step(`^研究节点已提交 (\d+) 条发现$`, s.addFindings)
	sc.Step(`^调用方跨页读完全部 (\d+) 条发现$`, s.readAll)
	sc.Step(`^调用方保持请求 ID 但修改查询$`, s.conflictingBorrow)

	sc.Step(
		`^借阅请求返回冲突$`,
		func() error {
			return expectError(s.lastError, a2a.ErrConflict)
		},
	)

	sc.Step(`^其他调用方不能查询读取或释放该 lease$`, s.checkIntruder)
	sc.Step(`^其他调用方自己的请求被拒绝且不返回内容$`, s.checkDenied)
	sc.Step(`^授权终止方式为 (released|revoked|expired)$`, s.closeGrant)
	sc.Step(`^调用方不能继续读取该 lease$`, s.unavailable)
	sc.Step(`^记录的决策仍标识原 lease$`, s.recoverGrant)

	sc.Step(
		`^研究节点要求本地批准借阅$`,
		func() {
			s.pending.Store(true)
		},
	)

	sc.Step(`^恢复后的决策仍为 "([^"]*)"$`, s.recoverDecision)
	sc.Step(`^研究节点将请求处理为 "([^"]*)"$`, s.resolve)
	sc.Step(`^读取结果符合 "([^"]*)" 决策$`, s.checkAccess)
	sc.Step(`^节点提交包含 "([^"]*)" 的协议请求$`, s.wireRequest)

	sc.Step(
		`^返回协议错误 "([^"]*)"$`,
		func(want string) error {
			if s.wireCode != want {
				return fmt.Errorf("wire error = %q, want %q", s.wireCode, want)
			}

			return nil
		},
	)

	initializeMCP(sc, s)
	initializePeerContent(sc, s)
}

// Called after closing the databases so the saved files can be reopened safely.
func (s *scenario) saveFailure(name string, failure error) error {
	parent := os.Getenv("ANRA_E2E_ARTIFACTS")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("save failure artifacts (original files at %s): %w", s.root, err)
	}
	dir, err := os.MkdirTemp(parent, "scenario-")
	if err == nil {
		err = os.CopyFS(dir, os.DirFS(s.root))
	}
	if err != nil {
		return fmt.Errorf("save failure artifacts (original files at %s): %w", s.root, err)
	}
	data, err := json.MarshalIndent(map[string]any{
		"scenario": name, "error": failure.Error(), "answer": s.answer,
		"model_request": s.lastRequest, "borrow_request": s.request,
		"decision": s.decision, "first_page": s.firstPage,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "failure.json"), data, 0o644)
}

type modelFunc func(context.Context, runner.Request) (runner.Response, error)

func (s *scenario) openTape(name, role string) (*bbolt.Bbolt, error) {
	tape, err := bbolt.NewBboltStorage("session", filepath.Join(s.root, name+".db"))
	if err != nil {
		return nil, err
	}
	if err := tape.Init(owner.WithOwnerId(s.ctx, role)); err != nil {
		return nil, errors.Join(err, tape.Close())
	}
	return tape, nil
}

func (s *scenario) openLocalTape() error {
	var err error
	s.localTape, err = s.openTape("borrower", "borrower-local")
	return err
}

func (s *scenario) reopenLocalTape() error {
	if err := s.localTape.Close(); err != nil {
		return err
	}
	s.localTape = nil
	return s.openLocalTape()
}

func (s *scenario) ask(question, answer string) error {
	a, err := agent.New("borrower-local", s.localTape, agent.WithLLM(modelFunc(
		func(_ context.Context, req runner.Request) (runner.Response, error) {
			s.lastRequest = req

			return runner.Response{
				Output: answer,
			}, nil
		},
	)))
	if err != nil {
		return err
	}
	s.answer, err = a.Run(s.ctx, question)
	return err
}

func (s *scenario) otherMember() error {
	if err := s.localTape.Init(owner.WithOwnerId(s.ctx, "other-local")); err != nil {
		return err
	}

	a, err := agent.New("other-local", s.localTape, agent.WithLLM(modelFunc(
		func(_ context.Context, req runner.Request) (runner.Response, error) {
			if len(req.Messages) != 1 || req.Messages[0].Text != "private question" {
				return runner.Response{}, fmt.Errorf("another member received private history: %+v", req.Messages)
			}

			return runner.Response{
				Output: "private answer",
			}, nil
		},
	)))
	if err != nil {
		return err
	}
	_, err = a.Run(s.ctx, "private question")
	return err
}

func (s *scenario) conversation(table *godog.Table) error {
	rows := table.Rows[1:]
	if len(rows) != len(s.lastRequest.Messages) {
		return fmt.Errorf("conversation has %d messages, want %d", len(s.lastRequest.Messages), len(rows))
	}
	for i, row := range rows {
		got := s.lastRequest.Messages[i]
		if string(got.Role) != row.Cells[0].Value || got.Text != row.Cells[1].Value {
			return fmt.Errorf("message %d = %+v, want %s: %s", i, got, row.Cells[0].Value, row.Cells[1].Value)
		}
	}
	return nil
}

func (s *scenario) answerIs(want string) error {
	if s.answer != want {
		return fmt.Errorf("answer = %q, want %q", s.answer, want)
	}
	return nil
}

func (s *scenario) setupPeer() error {
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

func (s *scenario) appendFinding(text string) error {
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

func (s *scenario) selection() bv.Selection {
	return bv.Selection{
		Status: a2a.Granted,
		TTL:    time.Hour,
	}
}

func (s *scenario) startPeer() error {
	var err error

	sourceAgent, err := agent.New("researcher-local", s.peerTape)
	if err != nil {
		return err
	}

	source := sourceAgent.Source()
	if err := sourceAgent.Shutdown(s.ctx); err != nil {
		return err
	}

	s.service, err = bv.NewService(
		bv.Config{
			Source:     source,
			Owner:      "researcher-local",
			Tape:       s.peerTape,
			RecordPath: filepath.Join(s.root, "grants.db"),

			Views: recoveredViews{
				Tape:  s.peerTape,
				Owner: "researcher-local",
				All:   true,
			},
			Exporter: bv.TextExporter{},

			Now: func() time.Time {
				return time.Unix(0, s.clock.Load())
			},

			Approve: func(_ context.Context, peer string, _ *pb.BorrowRequest) (bv.Selection, error) {
				if peer != "borrower" {
					return bv.Selection{
						Status: a2a.Denied,
					}, nil
				}
				if s.pending.Load() {
					return bv.Selection{
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
	executor, err := a2a.NewExecutor(lender)
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
	handler, err := a2a.NewPeerHandler(card, a2asrv.NewHandler(executor), s.service, a2a.TrustedPeerIdentity)
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

func (s *scenario) stopPeer() error {
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
	if s.service != nil {
		err = s.service.Close()
		s.service = nil
	}
	if s.peerTape != nil {
		err = errors.Join(err, s.peerTape.Close())
		s.peerTape = nil
	}
	return err
}

func (s *scenario) restartPeer() error {
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

func (s *scenario) httpClient(peer string) *http.Client {
	client := a2a.TrustedHTTPClient(peer)
	client.Timeout = 5 * time.Second
	return client
}

func (s *scenario) clientFor(peer string) (*a2a.Client, error) {
	return a2a.NewClient(s.httpClient(peer), s.card)
}

func (s *scenario) sendMessage(text string) error {
	var err error
	s.ordinary, err = a2aclient.NewFromCard(s.ctx, s.card, a2aclient.WithJSONRPCTransport(s.httpClient("borrower")))
	if err != nil {
		return err
	}

	result, err := s.ordinary.SendMessage(
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

func (s *scenario) streamMessage(text string) error {
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

func (s *scenario) borrow() error {
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

func (s *scenario) decisionIs(want string) error {
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

func (s *scenario) readFirst() error {
	var err error

	s.firstPage, err = s.client.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId:  s.decision.Lease.Id,
			PageSize: 1,
		},
	)
	if err != nil {
		return err
	}
	if len(s.firstPage.Quote.Entries) != 1 || !exportedTextIs(s.firstPage.Quote.Entries[0], "bbolt requires a single writer") || s.firstPage.NextCursor == "" {
		return fmt.Errorf("unexpected first page: %v", s.firstPage)
	}
	return nil
}

func (s *scenario) retryGrant() error {
	got, err := s.client.Borrow(s.ctx, s.request)
	if err != nil {
		return err
	}
	if !proto.Equal(got, s.decision) {
		return fmt.Errorf("retry changed decision: got %v, want %v", got, s.decision)
	}
	return nil
}

func (s *scenario) finishSnapshot() error {
	page, err := s.client.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId:  s.decision.Lease.Id,
			Cursor:   s.firstPage.NextCursor,
			PageSize: 1,
		},
	)
	if err != nil {
		return err
	}

	if len(page.Quote.Entries) != 1 || !exportedTextIs(page.Quote.Entries[0], "concurrent updates need version checks") || page.Quote.Entries[0].Seq != s.decision.Lease.Ref.Scopes[1].SeqS || page.NextCursor != "" || !proto.Equal(page.Quote.Provenance, s.firstPage.Quote.Provenance) {
		return fmt.Errorf("original snapshot was not preserved: %v", page)
	}
	return nil
}

func (s *scenario) addFindings(count int) error {
	for int(s.findingCount.Load()) < count {
		if err := s.appendFinding(fmt.Sprintf("finding %d", s.findingCount.Load()+1)); err != nil {
			return err
		}
	}
	return nil
}

func (s *scenario) readAll(count int) error {
	pages, err := s.client.ReadView(s.ctx, s.decision.Lease)
	if err != nil {
		return err
	}
	if len(pages) < 2 {
		return fmt.Errorf("expected multiple pages, got %d", len(pages))
	}
	seen := 0
	for _, page := range pages {

		if page.Provenance.LeaseId != s.decision.Lease.Id || !proto.Equal(page.Provenance.Ref, s.decision.Lease.Ref) {
			return fmt.Errorf("page lost provenance: %v", page)
		}
		for _, e := range page.Entries {
			seen++
			if e.Seq != s.decision.Lease.Ref.Scopes[seen-1].SeqS {
				return fmt.Errorf("entry %d has sequence %s", seen, e.Seq)
			}
		}
	}
	if seen != count {
		return fmt.Errorf("received %d findings, want %d", seen, count)
	}
	return nil
}

func (s *scenario) conflictingBorrow() {
	changed := proto.Clone(s.request).(*pb.BorrowRequest)
	changed.Query = "a different query"
	_, s.lastError = s.client.Borrow(s.ctx, changed)
}

func expectError(got, want error) error {
	if !errors.Is(got, want) {
		return fmt.Errorf("error = %v, want %v", got, want)
	}
	return nil
}

func (s *scenario) checkIntruder() error {
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

func (s *scenario) checkDenied() error {
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

func (s *scenario) closeGrant(closure string) error {
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

func (s *scenario) unavailable() error {
	_, err := s.client.Read(
		s.ctx,
		&rpcpb.ReadRequest{
			LeaseId: s.decision.Lease.Id,
		},
	)

	return expectError(err, a2a.ErrUnavailable)
}

func (s *scenario) recoverGrant() error {
	d, err := s.client.Get(s.ctx, s.request.RequestId)
	if err != nil {
		return err
	}
	if !proto.Equal(d, s.decision) {
		return fmt.Errorf("recorded grant changed: %v", d)
	}
	return s.retryGrant()
}

func (s *scenario) recoverDecision(want string) error {
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

func (s *scenario) resolve(outcome string) error {
	sel := bv.Selection{
		Status: a2a.Denied,
	}

	if outcome == "granted" {
		sel = s.selection()
	}
	_, err := s.service.Resolve(s.ctx, "borrower", s.request.RequestId, sel)
	return err
}

func (s *scenario) checkAccess(outcome string) error {
	if outcome == "denied" {
		return s.decisionIs(outcome)
	}

	pages, err := s.client.ReadView(s.ctx, s.decision.Lease)
	if err != nil {
		return err
	}
	if len(pages) != 1 || len(pages[0].Entries) != 2 {
		return fmt.Errorf("approved context missing: %v", pages)
	}
	return nil
}

func (s *scenario) borrowWithAgent() error {
	if err := s.openLocalTape(); err != nil {
		return err
	}
	turns := 0

	a, err := agent.New("borrower-local", s.localTape, agent.WithBorrowClient(s.client), agent.WithLLM(modelFunc(
		func(_ context.Context, req runner.Request) (runner.Response, error) {
			turns++

			if turns == 1 {
				return runner.Response{
					ToolCalls: []runner.ToolCall{{
						ID:        "borrow-call",
						Name:      "borrow_peer_context",
						Arguments: json.RawMessage(`{"requestId":"agent-borrow","purpose":"fix storage concurrency","query":"bbolt"}`),
					}},
				}, nil
			}

			s.lastRequest = req

			if err := s.checkBorrowedContext(); err != nil {
				return runner.Response{}, err
			}

			return runner.Response{
				Output: "Use version checks for competing writes.",
			}, nil
		},
	)))
	if err != nil {
		return err
	}
	s.answer, err = a.Run(s.ctx, "help with storage concurrency")
	return err
}

func (s *scenario) checkBorrowedContext() error {
	for _, message := range s.lastRequest.Messages {
		if message.Role != runner.RoleTool && (strings.Contains(message.Text, "bbolt requires a single writer") || strings.Contains(message.Text, "concurrent updates need version checks")) {
			return errors.New("borrowed findings were promoted into native messages")
		}

		if message.Role != runner.RoleTool {
			continue
		}
		for _, result := range message.ToolResults {
			var envelope struct {
				Decision json.RawMessage `json:"decision"`
				Borrowed struct {
					Channel bv.Channel `json:"channel"`

					Provenance json.RawMessage `json:"provenance"`

					Entries []struct {
						Seq string `json:"seq"`

						MediaType string `json:"mediaType"`

						Content string `json:"content"`
					} `json:"entries"`
				} `json:"borrowed_context"`
			}
			if err := json.Unmarshal(result.Result, &envelope); err != nil {
				return err
			}
			var decision pb.Decision
			if err := protojson.Unmarshal(envelope.Decision, &decision); err != nil {
				return err
			}

			var provenance pb.Provenance

			if err := protojson.Unmarshal(envelope.Borrowed.Provenance, &provenance); err != nil {
				return err
			}

			if decision.Status != a2a.Granted || decision.Lease == nil || len(envelope.Borrowed.Entries) != 2 {
				return fmt.Errorf("borrowed result lacks full findings: %s", result.Result)
			}

			if envelope.Borrowed.Channel != bv.Borrowed || provenance.LeaseId != decision.Lease.Id || provenance.Caller != decision.Lease.Caller || !proto.Equal(provenance.Ref, decision.Lease.Ref) {
				return errors.New("model tool result lost authenticated provenance")
			}

			if provenance.Ref.Source.AgentId != "researcher-local" || len(provenance.Ref.Scopes) != 2 {
				return errors.New("borrowed result changed the committed source or scopes")
			}

			var fields struct {
				Borrowed struct {
					Entries []map[string]json.RawMessage `json:"entries"`
				} `json:"borrowed_context"`
			}

			if err := json.Unmarshal(result.Result, &fields); err != nil {
				return err
			}

			for _, entry := range fields.Borrowed.Entries {
				if _, exists := entry["summary"]; exists {
					return errors.New("borrowed result contains flattened summary")
				}
			}

			for i, text := range []string{"bbolt requires a single writer", "concurrent updates need version checks"} {

				e := envelope.Borrowed.Entries[i]

				if e.Seq != decision.Lease.Ref.Scopes[i].SeqS || e.MediaType != a2a.TextMediaType || e.Content != strconv.Quote(text) {
					return fmt.Errorf("unexpected borrowed finding: %v", e)
				}

			}

			return nil
		}
	}
	return errors.New("borrowed context missing from tool results")
}

func (s *scenario) recallBorrowedContext() error {
	a, err := agent.New("borrower-local", s.localTape, agent.WithLLM(modelFunc(
		func(_ context.Context, req runner.Request) (runner.Response, error) {
			s.lastRequest = req

			if err := s.checkBorrowedContext(); err != nil {
				return runner.Response{}, err
			}

			return runner.Response{
				Output: "memory retained",
			}, nil
		},
	)))
	if err != nil {
		return err
	}
	s.answer, err = a.Run(s.ctx, "recall the researcher's findings")
	if err != nil {
		return err
	}
	return s.answerIs("memory retained")
}

func (s *scenario) checkLocalOwnership() error {
	ctx := owner.WithOwnerId(s.ctx, "borrower-local")
	head, err := s.localTape.Get(ctx)
	if err != nil {
		return err
	}

	stored, err := s.localTape.Range(
		ctx,
		view.EntryRange{
			SeqS: entry.SeqFromUint64(1),
			SeqE: head.Scope.SeqE.Next(),
		},
	)
	if err != nil {
		return err
	}
	conversation, err := inspectLifecycle(stored.Raw)
	if err != nil {
		return err
	}
	if len(conversation) != 4 {
		return fmt.Errorf("borrower has %d entries, want 4 conversation entries", len(stored.Raw))
	}
	for _, e := range stored.Raw {
		if e.GetOwner() != "borrower-local" {
			return fmt.Errorf("imported peer ownership: %s", e.GetOwner())
		}
	}
	return nil
}

func (s *scenario) wireRequest(problem string) error {
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
	case "excessive page":
		method, body = "Read", `{"viewId":"unknown","pageSize":101}`
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

// messageClient exercises the external A2A boundary for fixtures holding a URL.
func messageClient(ctx context.Context, h *http.Client, base string) (*a2a.MessageClient, error) {
	card, err := a2a.Discover(ctx, h, strings.TrimRight(base, "/")+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		return nil, err
	}
	return a2a.NewMessageClientFromCard(ctx, h, card)
}
