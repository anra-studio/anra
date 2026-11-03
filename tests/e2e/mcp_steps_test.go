package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/agent"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/cucumber/godog"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/scbizu/tape-go/pkg/runner"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

type mcpScenario struct {
	s           *scenario
	tape        *bbolt.Bbolt
	handoffTape storage.TapeStorage
	agent       *agent.Anra
	service     *anramcp.Service
	providers   *mcpProviders
	server      *httptest.Server
	client      *sdk.ClientSession
	tools       *sdk.ListToolsResult
	role        anramcp.RoleDefinition
	handoff     anramcp.Handoff
	grant       anramcp.Grant
	token       string
	clock       atomic.Int64
	lastStatus  int
	lastBody    []byte
	lastResult  json.RawMessage
	original    map[string]json.RawMessage
	entered     chan struct{}
	release     chan struct{}
	finished    chan error
}

func initializeMCP(sc *godog.ScenarioContext, s *scenario) {
	m := &mcpScenario{s: s}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*m = mcpScenario{s: s, original: map[string]json.RawMessage{}}
		m.clock.Store(time.Now().UnixNano())
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if m.release != nil {
			if err := m.finish(); err != nil {
				return ctx, errors.Join(err, m.close())
			}
		}
		return ctx, m.close()
	})
	sc.Step(`^已提交一份普通 MCP 交接并签发凭证$`, func() error { return m.setup("normal") })
	sc.Step(`^已提交包含完整工具响应流的 MCP 交接并签发凭证$`, func() error { return m.setup("complete stream") })
	sc.Step(`^已提交包含未结束工具响应流的 MCP 交接并签发凭证$`, func() error { return m.setup("unfinished stream") })
	sc.Step(`^官方 MCP 客户端使用交接凭证连接$`, m.connect)
	sc.Step(`^客户端仅发现两个只读上下文工具$`, m.discover)
	sc.Step(`^两个工具返回交接绑定的角色版本和完整窗口$`, m.checkBoth)
	sc.Step(`^MCP 服务重建和重复读取均不新增文件或改变 Tape$`, m.readOnly)
	sc.Step(`^运行时将交接和窗口存入 JSONL Tape$`, m.useJSONLTape)
	sc.Step(`^控制器向 Tape 提交另一份交接$`, m.commitTapeOnly)
	sc.Step(`^使用原凭证读取新交接$`, m.readWindow)
	sc.Step(`^控制器为新交接签发凭证$`, func() { m.token = m.authorize(m.grant) })
	sc.Step(`^未携带凭证读取上下文$`, func() error {
		return m.call(anramcp.ReadContextWindow, "", m.grant.AnchorSeq, nil)
	})
	sc.Step(`^携带以下 Authorization 请求头读取上下文$`, func(table *godog.Table) error {
		headers := make(http.Header)
		for _, row := range table.Rows {
			headers.Add("Authorization", strings.ReplaceAll(row.Cells[0].Value, "{token}", m.token))
		}
		args, err := json.Marshal(map[string]string{"anchorSeq": m.grant.AnchorSeq})
		if err != nil {
			return err
		}
		return m.rawCall(anramcp.ReadContextWindow, "", args, headers)
	})
	sc.Step(`^使用绑定不匹配的凭证读取交接$`, m.readMismatchedBinding)
	sc.Step(`^使用当前凭证访问另一角色的服务$`, m.readOtherRole)
	sc.Step(`^连接仅获准读取角色$`, m.roleOnly)
	sc.Step(`^角色工具成功且窗口工具返回 permission_denied$`, m.checkRoleOnly)
	sc.Step(`^控制器撤销当前凭证$`, func() { m.providers.revoke(m.token) })
	sc.Step(`^Tape 和 MCP 服务重启$`, m.restart)
	sc.Step(`^记录两个工具的原始结果$`, m.remember)
	sc.Step(`^角色开始一轮等待模型返回的推理$`, m.hold)
	sc.Step(`^两个工具的结果逐字节保持不变$`, m.unchanged)
	sc.Step(`^当前推理正常结束$`, m.finish)
	sc.Step(`^角色版本更新并继续写入 Tape$`, m.grow)
	sc.Step(`^读取 MCP 上下文窗口$`, m.readWindow)
	sc.Step(`^读取 MCP 角色$`, func() error {
		return m.call(anramcp.ReadRole, m.token, m.grant.AnchorSeq, nil)
	})
	sc.Step(`^窗口完整且保留工具标识和所有有序响应分块$`, m.checkStream)
	sc.Step(`^A2A 与 MCP 返回相同的公开条目及存储标识$`, m.checkA2AProjection)
	sc.Step(`^离线删除交接窗口中的一条持久化记录并重启服务$`, m.removeWindowEntry)
	sc.Step(`^MCP 返回 "([^"]+)" 且不泄露上下文或私有信息$`, m.rejected)
	sc.Step(`^MCP 返回 HTTP (401|403) 且不泄露上下文或私有信息$`, func(status int) error {
		if m.lastStatus != status {
			return fmt.Errorf("want HTTP %d, got %d: %s", status, m.lastStatus, m.lastBody)
		}
		return m.rejected("permission_denied")
	})
	sc.Step(`^携带凭证从 Origin "([^"]+)" 读取上下文$`, m.originRequest)
	sc.Step(`^从 Origin "([^"]+)" 不带凭证读取上下文$`, func(origin string) error {
		return m.call(anramcp.ReadContextWindow, "", m.grant.AnchorSeq, map[string]string{"Origin": origin})
	})
	sc.Step(`^运行时仅信任 Origin "([^"]+)"$`, func(origin string) error {
		return m.configureTrustedOrigins([]string{origin})
	})
	sc.Step(`^MCP 响应包含交接绑定的完整窗口$`, m.checkWindowResponse)
}

func (m *mcpScenario) readWindow() error {
	return m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, nil)
}

func (m *mcpScenario) tapePath() string             { return filepath.Join(m.s.root, "mcp-tape.db") }
func (m *mcpScenario) tapeContext() context.Context { return owner.WithOwnerId(m.s.ctx, "vesta") }

func (m *mcpScenario) open() error {
	var err error
	if m.providers == nil {
		m.providers = &mcpProviders{roles: make(map[anramcp.RoleRef]anramcp.RoleDefinition), grants: make(map[string]anramcp.Grant)}
	}
	m.tape, err = bbolt.NewBboltStorage("mcp-session", m.tapePath())
	if err != nil {
		return err
	}
	if err = m.tape.Init(m.tapeContext()); err != nil {
		return err
	}
	m.agent, err = agent.New("vesta", m.tape, agent.WithLLM(modelFunc(func(ctx context.Context, req runner.Request) (runner.Response, error) {
		if len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Text == "hold MCP turn" {
			entered, release := m.entered, m.release
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return runner.Response{}, ctx.Err()
			}
		}
		return runner.Response{Output: "fixed answer"}, nil
	})))
	if err != nil {
		return err
	}
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.providers,
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	return err
}

func (m *mcpScenario) serve() { m.server = httptest.NewServer(m.service.Handler()) }

func (m *mcpScenario) close() error {
	var err error
	if m.client != nil {
		err = errors.Join(err, m.client.Close())
		m.client = nil
	}
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
	if m.agent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = errors.Join(err, m.agent.Shutdown(ctx))
		cancel()
		m.agent = nil
	}
	m.service = nil
	if m.tape != nil {
		err = errors.Join(err, m.tape.Close())
		m.tape = nil
	}
	return err
}

func (m *mcpScenario) setup(kind string) error {
	if err := m.open(); err != nil {
		return err
	}
	if _, err := m.agent.Run(m.s.ctx, "fixed request"); err != nil {
		return err
	}
	ref, err := m.agent.View(m.s.ctx, "controller", nil)
	if err != nil {
		return err
	}
	if kind != "normal" {
		ref, err = m.streamWindow(kind == "complete stream")
		if err != nil {
			return err
		}
	}
	m.role = anramcp.RoleDefinition{RoleRef: anramcp.RoleRef{ID: "vesta", Version: "revision-42"}, Definition: json.RawMessage(`{"instructions":"Help maintain Anra","tools":["read_files"]}`)}
	m.providers.setRole(m.role)
	m.handoff = anramcp.Handoff{
		Schema: anramcp.HandoffSchema, Type: "integration.handoff", Role: m.role.RoleRef,
		Target: anramcp.Target{Name: "lody", BindingID: "vesta-lody-private"}, Task: "implement MCP",
		Context: anramcp.WindowRef{View: ref}, Origin: &anramcp.Origin{Name: "raft", BindingID: "vesta-raft", SessionID: "origin-session", EventID: "origin-event"},
	}
	seq, err := m.commitHandoff(m.handoff)
	if err != nil {
		return err
	}
	m.grant = anramcp.Grant{RoleID: "vesta", BindingID: m.handoff.Target.BindingID, AnchorSeq: seq, Window: anramcp.WindowRef{View: proto.Clone(ref).(*pb.TapeView)}, Permissions: []string{anramcp.ReadRole, anramcp.ReadContextWindow}, ExpiresAt: time.Unix(0, m.clock.Load()).Add(time.Hour)}
	m.token = m.authorize(m.grant)
	m.serve()
	return nil
}

func (m *mcpScenario) append(kind te.EntryKind, text string) (te.EntryLike, error) {
	return m.tape.Store(m.tapeContext(), te.NewEntry(te.WithEntryKind(kind), te.WithEntryContent(text), te.WithEntryOwner("vesta")))
}

func (m *mcpScenario) rawEvent(kind te.EntryKind, content string) error {
	_, err := m.tape.Store(m.tapeContext(), te.CustomEntry{
		Entry:      te.NewEntry(te.WithEntryKind(kind), te.WithEntryOwner("vesta"), te.WithEntryContent("PRIVATE-SUMMARY")),
		Extensions: map[string]any{"adk:event": `{"content":` + content + `,"stateDelta":{"private":"PRIVATE-STATE"}}`},
	})
	return err
}

// A single call with continuing chunks exercises persistence and transport;
// protocol field filtering and number lexemes are tested in projection.
func (m *mcpScenario) streamWindow(complete bool) (*pb.TapeView, error) {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return nil, err
	}
	start := state.Scope.SeqE.Next()
	if err := m.rawEvent(te.EntryToolCall, `{"role":"model","parts":[{"functionCall":{"id":"tool-42","name":"lookup","args":{}}}]}`); err != nil {
		return nil, err
	}
	chunks := []string{"first", "middle", "last"}
	if !complete {
		chunks = chunks[:1]
	}
	for i, chunk := range chunks {
		continuing := !complete || i < len(chunks)-1
		content := fmt.Sprintf(`{"role":"user","parts":[{"functionResponse":{"id":"tool-42","name":"lookup","willContinue":%t,"response":{"chunk":%q}}}]}`, continuing, chunk)
		if err := m.rawEvent(te.EntryToolResult, content); err != nil {
			return nil, err
		}
	}
	head, err := m.append(te.EntryAssistant, "excluded committed boundary")
	if err != nil {
		return nil, err
	}
	return &pb.TapeView{Source: m.agent.Source(), SnapshotHead: head.GetID().String(), Scope: &pb.EntryRange{SeqS: start.String(), SeqE: head.GetID().String()}}, nil
}

type mcpBearerTransport struct{ token string }

func (t mcpBearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (m *mcpScenario) connect() error {
	client := sdk.NewClient(&sdk.Implementation{Name: "anra-e2e", Version: "1.0.0"}, nil)
	var err error
	m.client, err = client.Connect(m.s.ctx, &sdk.StreamableClientTransport{Endpoint: m.server.URL, HTTPClient: &http.Client{Transport: mcpBearerTransport{m.token}}}, nil)
	return err
}

func (m *mcpScenario) discover() error {
	var err error
	m.tools, err = m.client.ListTools(m.s.ctx, nil)
	if err != nil {
		return err
	}
	if len(m.tools.Tools) != 2 {
		return fmt.Errorf("tools count: %d", len(m.tools.Tools))
	}
	names := map[string]bool{anramcp.ReadRole: false, anramcp.ReadContextWindow: false}
	for _, tool := range m.tools.Tools {
		if _, ok := names[tool.Name]; !ok || tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			return fmt.Errorf("unexpected tool: %v", tool)
		}
		names[tool.Name] = true
	}
	if !names[anramcp.ReadRole] || !names[anramcp.ReadContextWindow] {
		return errors.New("missing context tool")
	}
	return nil
}

func (m *mcpScenario) call(tool, token, seq string, headers map[string]string) error {
	args, _ := json.Marshal(map[string]any{"anchorSeq": seq})
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return m.rawCall(tool, token, args, h)
}

func (m *mcpScenario) rawCall(tool, token string, args json.RawMessage, headers http.Header) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(m.s.ctx, http.MethodPost, m.server.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-11-25")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, values := range headers {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	m.lastStatus = res.StatusCode
	m.lastBody, err = io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		return errors.New("context response is cacheable")
	}
	var rpc struct {
		Result struct {
			StructuredContent json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(m.lastBody, &rpc); err != nil {
		return fmt.Errorf("MCP response: %s: %w", m.lastBody, err)
	}
	m.lastResult = rpc.Result.StructuredContent
	return nil
}

func (m *mcpScenario) success(tool string) (json.RawMessage, error) {
	if m.client != nil {
		r, err := m.client.CallTool(m.s.ctx, &sdk.CallToolParams{Name: tool, Arguments: map[string]any{"anchorSeq": m.grant.AnchorSeq}})
		if err != nil {
			return nil, err
		}
		if r.IsError {
			return nil, fmt.Errorf("tool failed: %v", r.Content)
		}
		return json.Marshal(r.StructuredContent)
	}
	if err := m.call(tool, m.token, m.grant.AnchorSeq, nil); err != nil {
		return nil, err
	}
	var e struct {
		Error *anramcp.Error `json:"error"`
	}
	if json.Unmarshal(m.lastResult, &e) != nil || e.Error != nil || m.lastStatus != 200 {
		return nil, fmt.Errorf("MCP %s failed: %s", tool, m.lastBody)
	}
	return append(json.RawMessage(nil), m.lastResult...), nil
}

func (m *mcpScenario) checkBoth() error {
	r, err := m.success(anramcp.ReadRole)
	if err != nil {
		return err
	}
	var role anramcp.RoleDefinition
	if json.Unmarshal(r, &role) != nil || role.RoleRef != m.role.RoleRef || !jsonEqual(role.Definition, m.role.Definition) {
		return fmt.Errorf("wrong role: %s", r)
	}
	w, err := m.success(anramcp.ReadContextWindow)
	if err != nil {
		return err
	}
	var window anramcp.WindowResult
	if json.Unmarshal(w, &window) != nil || !window.IsComplete || len(window.Entries) == 0 || !jsonEqual(window.View, anchor.ProtoJSON(m.handoff.Context.View)) {
		return fmt.Errorf("wrong window: %s", w)
	}
	return nil
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

func (m *mcpScenario) remember() error {
	for _, tool := range []string{anramcp.ReadRole, anramcp.ReadContextWindow} {
		r, err := m.success(tool)
		if err != nil {
			return err
		}
		m.original[tool] = r
	}
	return nil
}

func (m *mcpScenario) unchanged() error {
	for _, tool := range []string{anramcp.ReadRole, anramcp.ReadContextWindow} {
		r, err := m.success(tool)
		if err != nil {
			return err
		}
		if !bytes.Equal(r, m.original[tool]) {
			return fmt.Errorf("%s changed after persistence or concurrent append", tool)
		}
	}
	return nil
}

func (m *mcpScenario) grow() error {
	r := m.role
	r.Version = "revision-43"
	r.Definition = json.RawMessage(`{"instructions":"New behavior"}`)
	m.providers.setRole(r)
	_, err := m.agent.Run(m.s.ctx, "new request after handoff")
	return err
}

func (m *mcpScenario) restart() error {
	if err := m.close(); err != nil {
		return err
	}
	if err := m.open(); err != nil {
		return err
	}
	m.serve()
	return m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, nil)
}

func (m *mcpScenario) hold() error {
	m.entered = make(chan struct{})
	m.release = make(chan struct{})
	m.finished = make(chan error, 1)
	go func() { _, err := m.agent.Run(m.s.ctx, "hold MCP turn"); m.finished <- err }()
	select {
	case <-m.entered:
		return nil
	case <-m.s.ctx.Done():
		return m.s.ctx.Err()
	}
}

func (m *mcpScenario) finish() error {
	close(m.release)
	m.release = nil
	select {
	case err := <-m.finished:
		return err
	case <-m.s.ctx.Done():
		return m.s.ctx.Err()
	}
}

func (m *mcpScenario) commitTapeOnly() error {
	seq, err := m.commitHandoff(m.handoff)
	if err != nil {
		return err
	}
	m.grant.AnchorSeq = seq
	return nil
}

func (m *mcpScenario) readMismatchedBinding() error {
	g := m.grant
	g.BindingID = "vesta-other-binding"
	return m.call(anramcp.ReadContextWindow, m.authorize(g), g.AnchorSeq, nil)
}

func (m *mcpScenario) readOtherRole() (err error) {
	tape, err := m.s.openTape("mcp-diablo", "diablo")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tape.Close()) }()
	a, err := agent.New("diablo", tape)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.Shutdown(m.s.ctx)) }()
	service, err := anramcp.NewService(anramcp.Config{Source: a.Source(), Owner: "diablo", Tape: tape, Roles: m.providers, Grants: m.providers})
	if err != nil {
		return err
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	previous := m.server
	m.server = server
	defer func() { m.server = previous }()
	return m.readWindow()
}

func (m *mcpScenario) rejected(code string) error {
	var envelope struct {
		Error *anramcp.Error `json:"error"`
	}
	payload := m.lastBody
	if m.lastStatus == 200 {
		var rpc struct {
			Result struct {
				IsError           bool            `json:"isError"`
				StructuredContent json.RawMessage `json:"structuredContent"`
				Content           []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal(m.lastBody, &rpc) != nil || !rpc.Result.IsError || len(rpc.Result.Content) != 1 || !jsonEqual([]byte(rpc.Result.Content[0].Text), rpc.Result.StructuredContent) {
			return fmt.Errorf("not a structured MCP tool error: %s", m.lastBody)
		}
		payload = rpc.Result.StructuredContent
	}
	if json.Unmarshal(payload, &envelope) != nil || envelope.Error == nil || envelope.Error.Code != code {
		return fmt.Errorf("MCP error want %s, got HTTP %d %s", code, m.lastStatus, m.lastBody)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil || len(fields) != 1 || fields["error"] == nil {
		return fmt.Errorf("MCP error returned partial context: %s", payload)
	}
	for _, private := range []string{m.token, "vesta-lody-private", m.s.root, "fixed request", "fixed answer", "PRIVATE-"} {
		if private != "" && bytes.Contains(m.lastBody, []byte(private)) {
			return fmt.Errorf("MCP error leaked private metadata %q", private)
		}
	}
	return nil
}

func (m *mcpScenario) roleOnly() error {
	g := m.grant
	g.Permissions = []string{anramcp.ReadRole}
	m.token = m.authorize(g)
	return nil
}

func (m *mcpScenario) checkRoleOnly() error {
	if _, err := m.success(anramcp.ReadRole); err != nil {
		return err
	}
	if err := m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, nil); err != nil {
		return err
	}
	return m.rejected("permission_denied")
}

// Stop all storage users before deleting a committed entry on disk. MCP must
// reject the damaged persisted range even though the role provider is available.
func (m *mcpScenario) removeWindowEntry() (err error) {
	if err := m.close(); err != nil {
		return err
	}
	db, err := bolt.Open(m.tapePath(), 0o600, nil)
	if err != nil {
		return err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket([]byte("entries")).Bucket([]byte("vesta")).Bucket([]byte("mcp-session"))
		cursor := entries.Cursor()
		for key, data := cursor.First(); key != nil; key, data = cursor.Next() {
			var value struct{ Seq string }
			if err := json.Unmarshal(data, &value); err != nil {
				return err
			}
			if value.Seq == m.grant.Window.View.Scope.SeqS {
				return entries.Delete(key)
			}
		}
		return errors.New("physical tape entry not found")
	})
	if err = errors.Join(err, db.Close()); err != nil {
		return err
	}
	// Reopen only MCP: agent recovery must not repair the fixture before reading.
	m.tape, err = bbolt.NewBboltStorage("mcp-session", m.tapePath())
	if err != nil {
		return err
	}
	if err := m.tape.Init(m.tapeContext()); err != nil {
		return err
	}
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.grant.Window.View.Source, Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.providers,
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return m.readWindow()
}

func (m *mcpScenario) checkWindowResponse() error {
	var window anramcp.WindowResult
	if m.lastStatus != http.StatusOK || json.Unmarshal(m.lastResult, &window) != nil || !window.IsComplete || len(window.Entries) == 0 || !jsonEqual(window.View, anchor.ProtoJSON(m.handoff.Context.View)) {
		return fmt.Errorf("wrong window response: HTTP %d %s", m.lastStatus, m.lastBody)
	}
	return nil
}

func (m *mcpScenario) checkStream() error {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var window anramcp.WindowResult
	if err := json.Unmarshal(m.lastResult, &window); err != nil {
		return err
	}
	if len(window.Entries) != 4 {
		return fmt.Errorf("want call plus three chunks: %s", m.lastResult)
	}
	var call struct {
		Parts []struct {
			FunctionCall struct{ ID, Name string }
		} `json:"parts"`
	}
	if err := json.Unmarshal(window.Entries[0].Content, &call); err != nil || len(call.Parts) != 1 || call.Parts[0].FunctionCall.ID != "tool-42" || call.Parts[0].FunctionCall.Name != "lookup" {
		return fmt.Errorf("lost tool call identity: %s", window.Entries[0].Content)
	}
	if bytes.Contains(m.lastBody, []byte("PRIVATE-")) {
		return fmt.Errorf("private stream metadata exported: %s", m.lastBody)
	}
	for i, want := range []string{"first", "middle", "last"} {
		var content struct {
			Parts []struct {
				Response struct {
					ID           string `json:"id"`
					Name         string `json:"name"`
					WillContinue *bool  `json:"willContinue"`
					Response     struct {
						Chunk string `json:"chunk"`
					} `json:"response"`
				} `json:"functionResponse"`
			} `json:"parts"`
		}
		if err := json.Unmarshal(window.Entries[i+1].Content, &content); err != nil || len(content.Parts) != 1 {
			return fmt.Errorf("invalid chunk: %s", window.Entries[i+1].Content)
		}
		r := content.Parts[0].Response
		if r.ID != "tool-42" || r.Name != "lookup" || r.Response.Chunk != want || r.WillContinue == nil || *r.WillContinue != (i < 2) {
			return fmt.Errorf("lost stream identity, order or termination: %s", window.Entries[i+1].Content)
		}
	}
	return nil
}

func (m *mcpScenario) configureTrustedOrigins(origins []string) error {
	m.server.Close()
	var err error
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.providers,
		TrustedOrigins: origins, Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) originRequest(origin string) error {
	return m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, map[string]string{
		"Origin": origin, "X-Forwarded-Host": "trusted.example", "X-Forwarded-Proto": "https", "Forwarded": "host=trusted.example;proto=https",
	})
}

func (m *mcpScenario) readOnly() error {
	before, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	filesBefore, err := os.ReadDir(m.s.root)
	if err != nil {
		return err
	}
	if bytes.Contains(before, []byte(m.token)) {
		return errors.New("plaintext credential was persisted")
	}
	// Reconstruct only MCP, borrowing the same Tape and external providers.
	if err := m.restartService(); err != nil {
		return err
	}
	for i := 0; i < 3; i++ {
		if err := m.checkBoth(); err != nil {
			return err
		}
	}
	after, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	filesAfter, err := os.ReadDir(m.s.root)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) || len(filesBefore) != len(filesAfter) {
		return errors.New("MCP construction or reads changed persistent storage")
	}
	for i := range filesBefore {
		if filesBefore[i].Name() != filesAfter[i].Name() {
			return errors.New("MCP changed files in runtime directory")
		}
	}
	return nil
}

// The embedding controller writes Tape and issues credentials. These fixture
// helpers are deliberately outside pkg/mcp and own only in-memory authorization.
func (m *mcpScenario) commitHandoff(h anramcp.Handoff) (string, error) {
	data, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	tape := m.handoffTape
	if tape == nil {
		tape = m.tape
	}
	raw, err := tape.Store(m.tapeContext(), te.NewAnchor(te.Seq{}, "vesta", te.AnchorKindCustom, data))
	if err != nil {
		return "", err
	}
	return raw.GetID().String(), nil
}

func (m *mcpScenario) authorize(g anramcp.Grant) string {
	token := rand.Text()
	g.Window.View = proto.Clone(g.Window.View).(*pb.TapeView)
	g.Permissions = append([]string(nil), g.Permissions...)
	m.providers.mu.Lock()
	m.providers.grants[token] = g
	m.providers.mu.Unlock()
	return token
}

// Compare both public HTTP paths over the same persisted Tape range.
func (m *mcpScenario) checkA2AProjection() (err error) {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var window anramcp.WindowResult
	if err := json.Unmarshal(m.lastResult, &window); err != nil {
		return err
	}
	scope := m.handoff.Context.View.Scope
	service, err := a2a.NewService(a2a.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape,
		JournalPath: filepath.Join(m.s.root, "projection-grants.db"),
		Exporter:    a2a.ContextExporter{},
		Policy: a2a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a2a.Selection, error) {
			return a2a.Selection{Status: a2a.Granted, TTL: time.Hour, Range: &tv.EntryRange{
				SeqS: te.MustParseSeq(scope.SeqS), SeqE: te.MustParseSeq(scope.SeqE),
			}}, nil
		}),
	})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, service.Close()) }()
	handler, err := a2a.NewViewHandler(service, a2a.TrustedPeerIdentity)
	if err != nil {
		return err
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := restClient{ctx: m.s.ctx, base: server.URL, identity: "projection-reader", extension: true}
	var borrow pb.BorrowResponse
	if err := client.message(http.MethodPost, "/v1/tape/borrows", &pb.BorrowRequest{
		RequestId: "projection-parity", Purpose: "compare public model events", Query: "fixed window",
	}, &borrow); err != nil {
		return err
	}
	if borrow.Decision == nil || borrow.Decision.Status != a2a.Granted || borrow.Decision.View == nil {
		return fmt.Errorf("A2A did not grant the projection window: %v", &borrow)
	}
	var read pb.ReadResponse
	if err := client.message(http.MethodGet, "/v1/tape/views/"+borrow.Decision.View.ViewId+"/entries", nil, &read); err != nil {
		return err
	}
	if read.Page == nil || len(read.Page.Entries) != len(window.Entries) || read.Page.NextCursor != "" {
		return fmt.Errorf("A2A and MCP entry counts differ: %v", &read)
	}
	for i, exported := range read.Page.Entries {
		entry := window.Entries[i]
		if exported.Seq != entry.Seq || exported.Kind != entry.Kind || !exported.Timestamp.AsTime().Equal(entry.Timestamp) || exported.Payload == nil || !bytes.Equal(exported.Payload.JsonData, entry.Content) {
			return fmt.Errorf("A2A and MCP entry %d differ: %v / %s", i, exported, entry.Content)
		}
		if bytes.Contains(exported.Payload.JsonData, []byte("PRIVATE-")) || strings.Contains(exported.Summary, "PRIVATE-") {
			return fmt.Errorf("A2A entry %d leaked private data", i)
		}
	}
	return nil
}
