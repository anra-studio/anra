package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	a2a "github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	bv "github.com/anra-studio/anra/pkg/model/view"
	"github.com/cucumber/godog"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Only the model and permission decision are deterministic fixtures. Agent,
// tape, journal, discovery, SDK, Connect, and HTTP all use real implementations.
type scenario struct {
	root   string
	ctx    context.Context
	cancel context.CancelFunc

	localTape   *bbolt.Bbolt
	lastRequest runner.Request
	answer      string

	modelServer     *httptest.Server
	providerControl *modelGate

	peer peerScenario
}

func initializeScenario(sc *godog.ScenarioContext) {
	s := &scenario{}
	initializeCLI(sc, s)
	initializeRegistry(sc, s)
	initializeCollaboration(sc, s)
	initializeBorrowRead(sc, &s.peer)

	sc.Before(
		func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			root, err := os.MkdirTemp("", "anra-e2e-")
			if err != nil {
				return ctx, err
			}

			*s = scenario{
				root: root,
			}

			s.providerControl = newModelGate(1)

			s.modelServer = newModelServer(s.providerControl)

			s.ctx, s.cancel = context.WithTimeout(ctx, 20*time.Second)

			s.peer = peerScenario{
				root: root,
				ctx:  s.ctx,
			}
			s.peer.clock.Store(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).UnixNano())

			return s.ctx, nil
		},
	)

	sc.After(
		func(ctx context.Context, sc *godog.Scenario, scenarioErr error) (context.Context, error) {
			err := s.peer.stopPeer()

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

	initializeGrantSidecar(sc, &s.peer)

	sc.Step(`^an agent with its own tape$`, s.openLocalTape)

	sc.Step(`^I ask "([^"]*)" and the model answers "([^"]*)"$`, s.ask)
	sc.Step(`^I close and reopen the agent's storage$`, s.reopenLocalTape)
	sc.Step(`^another member uses the same database for a private conversation$`, s.otherMember)
	sc.Step(`^the model sees the conversation in order$`, s.conversation)
	sc.Step(`^the answer is "([^"]*)"$`, s.answerIs)
	sc.Step(`^研究节点已提交两份发现并持久化授权记录$`, s.peer.setupPeer)
	sc.Step(`^研究者节点提供两条发现和持久化授权$`, s.peer.setupPeer)
	sc.Step(`^本地角色借阅研究者上下文$`, s.borrowWithAgent)
	sc.Step(`^借阅回答为 "([^"]*)"$`, s.answerIs)
	sc.Step(`^模型仅在带来源凭据的借阅工具结果中收到上下文$`, s.checkBorrowedContext)
	sc.Step(`^借阅角色的存储重启$`, s.reopenLocalTape)
	sc.Step(`^模型仍以借阅工具结果回忆上下文$`, s.recallBorrowedContext)
	sc.Step(`^所有持久化条目仍归借阅角色所有$`, s.checkLocalOwnership)
	sc.Step(`^调用方请求借阅已提交视图$`, s.peer.borrow)
	sc.Step(`^借阅决策为 "([^"]*)"$`, s.peer.decisionIs)
	sc.Step(`^调用方读取完整授权视图$`, s.peer.readFirst)

	sc.Step(
		`^研究节点在授权后提交新的发现$`,
		func() error {
			return s.peer.appendFinding("new finding outside the snapshot")
		},
	)

	sc.Step(`^研究节点重启存储和服务$`, s.peer.restartPeer)
	sc.Step(`^使用原请求 ID 修改用途、查询和范围仍返回原 lease$`, s.peer.retryChangedBorrow)
	sc.Step(`^调用方再次读取相同的原有授权视图$`, s.peer.finishSnapshot)

	sc.Step(`^其他调用方不能查询读取或释放该 lease$`, s.peer.checkIntruder)
	sc.Step(`^其他调用方自己的请求被拒绝且不返回内容$`, s.peer.checkDenied)
	sc.Step(`^授权终止方式为 (released|revoked|expired)$`, s.peer.closeGrant)
	sc.Step(`^调用方不能继续读取该 lease$`, s.peer.unavailable)
	sc.Step(`^记录的决策仍标识原 lease$`, s.peer.recoverGrant)

	sc.Step(
		`^研究节点要求本地批准借阅$`,
		func() {
			s.peer.pending.Store(true)
		},
	)

	sc.Step(`^恢复后的决策仍为 "([^"]*)"$`, s.peer.recoverDecision)
	sc.Step(`^研究节点将请求处理为 "([^"]*)"$`, s.peer.completePending)
	sc.Step(`^读取结果符合 "([^"]*)" 决策$`, s.peer.checkAccess)
	sc.Step(`^另一借阅服务读取当前待审批决策$`, s.peer.rememberPendingDecision)
	sc.Step(`^另一服务的审批和重复借阅均不能改写结果$`, s.peer.rejectStaleDecision)
	sc.Step(`^审批租期无效时原待审批决策不变$`, func() error {
		return s.peer.rejectApproval(0, borrow.ErrInvalidParams)
	})
	sc.Step(`^已完成的请求不能再次审批且原决策不变$`, func() error {
		return s.peer.rejectApproval(time.Hour, borrow.ErrConflict)
	})
	sc.Step(`^节点提交包含 "([^"]*)" 的协议请求$`, s.peer.wireRequest)

	sc.Step(
		`^返回协议错误 "([^"]*)"$`,
		func(want string) error {
			if s.peer.wireCode != want {
				return fmt.Errorf("wire error = %q, want %q", s.peer.wireCode, want)
			}

			return nil
		},
	)

	initializeMCP(sc, s)
	initializePeerContent(sc, &s.peer)
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
		"model_request": s.lastRequest, "borrow_request": s.peer.request,
		"decision": s.peer.decision, "read_response": s.peer.firstRead,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "failure.json"), data, 0o644)
}

func (s *scenario) openTape(name, role string) (*bbolt.Bbolt, error) {
	return openFixtureTape(s.ctx, s.root, name, role)
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

func (s *scenario) borrowWithAgent() error {
	if err := s.openLocalTape(); err != nil {
		return err
	}
	turns := 0

	a, err := agent.New("borrower-local", s.localTape, agent.WithBorrowClient(s.peer.client), agent.WithLLM(modelFunc(
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
	stored, _, err := readAllEntries(ctx, s.localTape)
	if err != nil {
		return err
	}
	conversation, err := inspectLifecycle(stored)
	if err != nil {
		return err
	}
	if len(conversation) != 4 {
		return fmt.Errorf("borrower has %d entries, want 4 conversation entries", len(stored))
	}
	for _, e := range stored {
		if e.GetOwner() != "borrower-local" {
			return fmt.Errorf("imported peer ownership: %s", e.GetOwner())
		}
	}
	return nil
}
