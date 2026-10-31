package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/anra-studio/anra/pkg/agent"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/cucumber/godog"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/scbizu/tape-go/pkg/runner"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

type mcpScenario struct {
	s          *scenario
	tape       *bbolt.Bbolt
	agent      *agent.Anra
	service    *anramcp.Service
	server     *httptest.Server
	client     *sdk.ClientSession
	tools      *sdk.ListToolsResult
	role       anramcp.RoleDefinition
	handoff    anramcp.Handoff
	grant      anramcp.Grant
	token      string
	clock      atomic.Int64
	lastStatus int
	lastBody   []byte
	lastResult json.RawMessage
	original   map[string]json.RawMessage
	controlErr error
	entered    chan struct{}
	release    chan struct{}
	finished   chan error
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
			close(m.release)
			m.release = nil
		}
		return ctx, m.close()
	})
	sc.Step(`^an? "([^"]+)" MCP handoff$`, m.setup)
	sc.Step(`^an official MCP client connects with the handoff credential$`, m.connect)
	sc.Step(`^it discovers exactly the two read-only context tools$`, m.discover)
	sc.Step(`^both MCP tools return the committed role and context$`, m.checkBoth)
	sc.Step(`^I remember both MCP tool results$`, m.remember)
	sc.Step(`^the role changes and its tape grows$`, m.grow)
	sc.Step(`^the MCP storage and server restart$`, m.restart)
	sc.Step(`^another handoff commits without changing authorization storage$`, m.commitTapeOnly)
	sc.Step(`^the controller appends the handoff directly to Tape$`, m.appendHandoff)
	sc.Step(`^the controller authorizes the persisted handoff$`, func() error {
		var err error
		m.token, err = m.service.Authorize(m.s.ctx, m.grant)
		return err
	})
	sc.Step(`^the controller commits a window with "([^"]+)"$`, m.commitMissingWindow)
	sc.Step(`^the handoff commit fails without changing Tape or authorization storage$`, func() error {
		if !errors.Is(m.controlErr, anramcp.ErrFailedPrecondition) {
			return fmt.Errorf("missing window commit: %v", m.controlErr)
		}
		return nil
	})
	sc.Step(`^both MCP tool results are byte-for-byte unchanged$`, m.unchanged)
	sc.Step(`^the role starts a blocked reasoning turn$`, m.hold)
	sc.Step(`^the active reasoning turn can finish normally$`, m.finish)
	sc.Step(`^a handoff commits the persisted active-turn prefix$`, m.activePrefix)
	sc.Step(`^a caller attempts MCP access using "([^"]+)"$`, m.attack)
	sc.Step(`^MCP rejects access with "([^"]+)" and no private metadata$`, m.rejected)
	sc.Step(`^the MCP credential is "([^"]+)"$`, m.endCredential)
	sc.Step(`^the connection is authorized only to read the role$`, m.roleOnly)
	sc.Step(`^the role tool succeeds and the window tool returns permission_denied$`, m.checkRoleOnly)
	sc.Step(`^the controller authorizes a grant with changed "([^"]+)"$`, m.mismatch)
	sc.Step(`^the mismatched MCP grant is denied$`, func() error {
		if !errors.Is(m.controlErr, anramcp.ErrPermissionDenied) {
			return fmt.Errorf("grant mismatch: %v", m.controlErr)
		}
		return nil
	})
	sc.Step(`^an authenticated caller sends "(\{.*\})" as MCP tool arguments$`, m.badArguments)
	sc.Step(`^the committed MCP data suffers "([^"]+)"$`, m.damage)
	sc.Step(`^the MCP window preserves tool identities and omits private event metadata$`, m.checkTools)
	sc.Step(`^the MCP window preserves turn and integration session boundaries$`, m.checkBoundaries)
	sc.Step(`^the MCP window has zero entries and is complete$`, m.checkEmpty)
	sc.Step(`^I read the MCP context window$`, func() error { return m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, nil) })
	sc.Step(`^reusing the role version with different contents fails$`, m.immutableRole)
	sc.Step(`^repeated MCP reads leave tape and authorization storage unchanged$`, m.readOnly)
	sc.Step(`^the MCP window contains every ordered chunk and is complete$`, m.checkStream)
	sc.Step(`^both MCP result representations preserve the raw business JSON$`, m.checkPreciseJSON)
	sc.Step(`^both MCP result representations preserve empty business JSON objects$`, func() error { return m.checkBusinessJSON(`{}`) })
	sc.Step(`^the embedding runtime trusts Origin "([^"]+)"$`, m.trustOrigin)
	sc.Step(`^an authenticated MCP caller sends Origin "([^"]+)" using "([^"]+)"$`, m.originRequest)
	sc.Step(`^an authenticated MCP caller sends "([^"]+)" Origin headers$`, m.originHeaders)
	sc.Step(`^MCP returns HTTP 403 without context or private metadata$`, func() error {
		if m.lastStatus != http.StatusForbidden {
			return fmt.Errorf("want HTTP 403, got %d: %s", m.lastStatus, m.lastBody)
		}
		return m.rejected("permission_denied")
	})
	sc.Step(`^the MCP response contains the complete committed window$`, m.checkWindowResponse)
	sc.Step(`^the embedding runtime configures invalid trusted Origin "([^"]+)"$`, func(origin string) error {
		service, err := anramcp.NewService(anramcp.Config{
			Source: m.agent.Source(), Owner: "vesta", Tape: m.tape,
			StatePath: filepath.Join(m.s.root, "invalid-origin.db"), TrustedOrigins: []string{origin},
		})
		m.controlErr = err
		if service != nil {
			return service.Close()
		}
		return nil
	})
	sc.Step(`^the MCP Origin configuration is rejected$`, func() error {
		if m.controlErr == nil {
			return errors.New("invalid trusted Origin was accepted")
		}
		return nil
	})
}

func (m *mcpScenario) statePath() string            { return filepath.Join(m.s.root, "mcp-state.db") }
func (m *mcpScenario) tapePath() string             { return filepath.Join(m.s.root, "mcp-tape.db") }
func (m *mcpScenario) tapeContext() context.Context { return owner.WithOwnerId(m.s.ctx, "vesta") }

func (m *mcpScenario) open() error {
	var err error
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
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, StatePath: m.statePath(),
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
	if m.service != nil {
		err = errors.Join(err, m.service.Close())
		m.service = nil
	}
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
		ref, err = m.specialWindow(kind)
		if err != nil {
			return err
		}
	}
	m.role = anramcp.RoleDefinition{RoleRef: anramcp.RoleRef{ID: "vesta", Version: "revision-42"}, Definition: json.RawMessage(`{"instructions":"Help maintain Anra","tools":["read_files"]}`)}
	if err := m.service.SaveRole(m.s.ctx, m.role); err != nil {
		return err
	}
	m.handoff = anramcp.Handoff{
		Schema: anramcp.HandoffSchema, Type: "integration.handoff", Role: m.role.RoleRef,
		Target: anramcp.Target{Name: "lody", BindingID: "vesta-lody-private"}, Task: "implement MCP",
		Context: anramcp.WindowRef{View: ref}, Origin: &anramcp.Origin{Name: "raft", BindingID: "vesta-raft", SessionID: "origin-session", EventID: "origin-event"},
	}
	seq, err := m.service.CommitHandoff(m.s.ctx, m.handoff)
	if err != nil {
		return err
	}
	m.grant = anramcp.Grant{RoleID: "vesta", BindingID: m.handoff.Target.BindingID, AnchorSeq: seq, Window: anramcp.WindowRef{View: proto.Clone(ref).(*pb.TapeView)}, Permissions: []string{anramcp.ReadRole, anramcp.ReadContextWindow}, ExpiresAt: time.Unix(0, m.clock.Load()).Add(time.Hour)}
	m.token, err = m.service.Authorize(m.s.ctx, m.grant)
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) append(kind te.EntryKind, text string) (te.EntryLike, error) {
	return m.tape.Store(m.tapeContext(), te.NewEntry(te.WithEntryKind(kind), te.WithEntryContent(text), te.WithEntryOwner("vesta")))
}

func (m *mcpScenario) event(kind te.EntryKind, content *genai.Content) (te.EntryLike, error) {
	data, err := json.Marshal(map[string]any{"content": content, "stateDelta": map[string]any{"private": "PRIVATE-STATE"}, "author": "PRIVATE-AUTHOR"})
	if err != nil {
		return nil, err
	}
	return m.tape.Store(m.tapeContext(), te.CustomEntry{Entry: te.NewEntry(te.WithEntryKind(kind), te.WithEntryOwner("vesta"), te.WithEntryContent("PRIVATE-SUMMARY")), Extensions: map[string]any{"adk:event": string(data), "secret": "PRIVATE-EXTENSION"}})
}

// Raw fixture JSON starts on disk, without a float64 round trip in the test.
const preciseBusinessJSON = `{"id":9007199254740993,"fraction":0.12345678901234567890123456789,"tiny":1e-1000,"futureVisiblePart":{"text":"business data","id":9007199254740993}}`

func (m *mcpScenario) rawEvent(kind te.EntryKind, content string) error {
	_, err := m.tape.Store(m.tapeContext(), te.CustomEntry{
		Entry:      te.NewEntry(te.WithEntryKind(kind), te.WithEntryOwner("vesta"), te.WithEntryContent("PRIVATE-SUMMARY")),
		Extensions: map[string]any{"adk:event": `{"content":` + content + `,"stateDelta":{"private":"PRIVATE-STATE"}}`},
	})
	return err
}

func (m *mcpScenario) protocolWindow(kind string) error {
	call := `{"id":"tool-42","name":"lookup","args":{}}`
	response := `{"id":"tool-42","name":"lookup","response":{}}`
	switch kind {
	case "precise tool JSON":
		call = `{"id":"tool-42","name":"lookup","args":` + preciseBusinessJSON + `}`
		response = `{"id":"tool-42","name":"lookup","response":` + preciseBusinessJSON + `}`
	case "unknown content field":
		return m.rawEvent(te.EntryAssistant, `{"role":"model","parts":[{"text":"visible"}],"futureContent":"must retain or reject"}`)
	case "unknown call field":
		call = `{"id":"tool-42","name":"lookup","args":{},"futureCall":"must retain or reject"}`
	case "streamed call arguments":
		call = `{"id":"tool-42","name":"lookup","partialArgs":[{"jsonPath":"$.query","stringValue":"partial"}]}`
	case "call continuation":
		call = `{"id":"tool-42","name":"lookup","args":{},"willContinue":true}`
	case "unknown response field":
		response = `{"id":"tool-42","name":"lookup","response":{},"futureResponse":"must retain or reject"}`
	case "unknown response part":
		response = `{"id":"tool-42","name":"lookup","response":{},"parts":[{"futureVisiblePart":{"text":"must retain or reject"}}]}`
	case "unknown response media":
		response = `{"id":"tool-42","name":"lookup","response":{},"parts":[{"inlineData":{"mimeType":"image/png","data":"aW1hZ2U=","futureMedia":"must retain or reject"}}]}`
	}
	if err := m.rawEvent(te.EntryToolCall, `{"role":"model","parts":[{"functionCall":`+call+`}]}`); err != nil {
		return err
	}
	if kind == "unfinished stream" || kind == "complete stream" {
		chunks := []string{`{"chunk":"first"}`, `{"chunk":"middle"}`, `{"chunk":"last"}`}
		if kind == "unfinished stream" {
			chunks = chunks[:1]
		}
		for i, chunk := range chunks {
			continuing := kind == "unfinished stream" || i < len(chunks)-1
			content := fmt.Sprintf(`{"role":"user","parts":[{"functionResponse":{"id":"tool-42","name":"lookup","willContinue":%t,"response":%s}}]}`, continuing, chunk)
			if err := m.rawEvent(te.EntryToolResult, content); err != nil {
				return err
			}
		}
		return nil
	}
	return m.rawEvent(te.EntryToolResult, `{"role":"user","parts":[{"functionResponse":`+response+`}]}`)
}

func (m *mcpScenario) specialWindow(kind string) (*pb.TapeView, error) {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return nil, err
	}
	start := state.Scope.SeqE.Next()
	switch kind {
	case "unfinished stream", "complete stream", "precise tool JSON", "empty tool JSON", "unknown content field", "unknown call field", "unknown response field", "unknown response part", "unknown response media", "streamed call arguments", "call continuation":
		err = m.protocolWindow(kind)
	case "large sequences":
		start = te.MustParseSeq("18446744073709551616000000000000000001")
		_, err = m.tape.Store(m.tapeContext(), te.NewEntry(te.WithEntryID(start), te.WithEntryKind(te.EntryUser), te.WithEntryContent("large sequence request"), te.WithEntryOwner("vesta")))
	case "unsupported part":
		_, err = m.event(te.EntryAssistant, &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "text alongside unsupported image", InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("image")}}}})
	case "malformed event":
		_, err = m.tape.Store(m.tapeContext(), te.CustomEntry{Entry: te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryOwner("vesta"), te.WithEntryContent("PRIVATE-SUMMARY")), Extensions: map[string]any{"adk:event": 42}})
	case "empty":
		_, err = m.event(te.EntryAssistant, &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "PRIVATE-THOUGHT", Thought: true}}})
	case "unknown":
		_, err = m.append("integration:unknown", "PRIVATE-UNKNOWN")
	case "oversized":
		_, err = m.append(te.EntryUser, strings.Repeat("large-window-", 50000))
	case "tools", "orphan result", "unfinished call":
		if kind != "orphan result" {
			_, err = m.event(te.EntryToolCall, &genai.Content{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: "tool-42", Name: "lookup", Args: map[string]any{"query": "Anra"}}}, {Text: "PRIVATE-THOUGHT", Thought: true}}})
		}
		if err == nil && kind != "unfinished call" {
			_, err = m.event(te.EntryToolResult, &genai.Content{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: "tool-42", Name: "lookup", Response: map[string]any{"answer": "found"}}}}})
		}
	case "boundaries":
		for _, r := range []anchor.Record{{Schema: anchor.Schema, Type: anchor.TurnStarted, TurnID: "boundary-turn", OwnerEpoch: "PRIVATE-EPOCH"}, {Schema: anchor.Schema, Type: anchor.TurnClosed, TurnID: "boundary-turn", StartSeq: start.String(), Outcome: "failed"}} {
			data, _ := json.Marshal(r)
			if _, err = m.append(anchor.Kind, string(data)); err != nil {
				return nil, err
			}
		}
		prior := anramcp.Handoff{Schema: anramcp.HandoffSchema, Type: "integration.handoff", Role: anramcp.RoleRef{ID: "vesta", Version: "old"}, Target: anramcp.Target{Name: "lody", BindingID: "old-binding"}, Task: "PRIVATE-TASK", Context: anramcp.WindowRef{View: &pb.TapeView{Source: m.agent.Source(), SnapshotHead: "5", Scope: &pb.EntryRange{SeqS: "3", SeqE: "5"}}}, Origin: &anramcp.Origin{Name: "raft", SessionID: "origin-session", EventID: "origin-event"}}
		data, _ := json.Marshal(prior)
		_, err = m.append(anchor.Kind, string(data))
	default:
		return nil, fmt.Errorf("unknown MCP fixture %q", kind)
	}
	if err != nil {
		return nil, err
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
	if err := m.service.SaveRole(m.s.ctx, r); err != nil {
		return err
	}
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

func (m *mcpScenario) activePrefix() error {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return err
	}
	// The active user input becomes an excluded, durably persisted boundary.
	// Include the persisted start record; later model output cannot grow it.
	head := state.Scope.SeqE
	entries, err := m.tape.Range(m.tapeContext(), tv.EntryRange{SeqS: te.MustParseSeq(m.grant.AnchorSeq).Next(), SeqE: head.Next()})
	if err != nil {
		return err
	}
	if len(entries.Raw) < 2 {
		return errors.New("active prefix not persisted")
	}
	m.handoff.Context.View = &pb.TapeView{Source: m.agent.Source(), SnapshotHead: head.String(), Scope: &pb.EntryRange{SeqS: entries.Raw[0].GetID().String(), SeqE: head.String()}}
	seq, err := m.service.CommitHandoff(m.s.ctx, m.handoff)
	if err != nil {
		return err
	}
	m.grant.AnchorSeq = seq
	m.grant.Window.View = proto.Clone(m.handoff.Context.View).(*pb.TapeView)
	m.token, err = m.service.Authorize(m.s.ctx, m.grant)
	return err
}

func (m *mcpScenario) commitTapeOnly() error {
	before, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	seq, err := m.service.CommitHandoff(m.s.ctx, m.handoff)
	if err != nil {
		return err
	}
	after, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return errors.New("handoff commit wrote authorization storage")
	}
	m.grant.AnchorSeq = seq
	return nil
}

func (m *mcpScenario) appendHandoff() error {
	data, err := json.Marshal(m.handoff)
	if err != nil {
		return err
	}
	raw, err := m.append(anchor.Kind, string(data))
	if err != nil {
		return err
	}
	m.grant.AnchorSeq = raw.GetID().String()
	return nil
}

func (m *mcpScenario) commitMissingWindow(missing string) error {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return err
	}
	h := m.handoff
	h.Context.View = proto.Clone(h.Context.View).(*pb.TapeView)
	head := state.Scope.SeqE.Next()
	switch missing {
	case "missing boundary":
		h.Context.View.SnapshotHead = head.String()
		h.Context.View.Scope.SeqE = head.String()
	case "missing entry":
		start := head
		head = head.Next()
		if _, err := m.tape.Store(m.tapeContext(), te.NewEntry(te.WithEntryID(head), te.WithEntryKind(te.EntryAssistant), te.WithEntryContent("persisted boundary after gap"), te.WithEntryOwner("vesta"))); err != nil {
			return err
		}
		h.Context.View.SnapshotHead = head.String()
		h.Context.View.Scope = &pb.EntryRange{SeqS: start.String(), SeqE: head.String()}
	default:
		return fmt.Errorf("unknown missing window fixture %q", missing)
	}
	tapeBefore, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	stateBefore, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	seq, commitErr := m.service.CommitHandoff(m.s.ctx, h)
	m.controlErr = commitErr
	if seq != "" {
		return fmt.Errorf("failed commit returned anchor %s", seq)
	}
	tapeAfter, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	stateAfter, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	if !bytes.Equal(tapeBefore, tapeAfter) || !bytes.Equal(stateBefore, stateAfter) {
		return errors.New("failed handoff commit changed persistent storage")
	}
	return nil
}

func (m *mcpScenario) attack(attack string) error {
	token, seq := m.token, m.grant.AnchorSeq
	headers := map[string]string{}
	switch attack {
	case "no credential":
		token = ""
	case "anchor as credential":
		token = seq
	case "invented credential":
		token = "not-a-credential"
	case "nonexistent anchor":
		seq = "999999999999999999999999999999999999"
	case "spoofed identity headers":
		token = ""
		headers = map[string]string{"X-Anra-Role": "vesta", "X-Anra-Binding": "vesta-lody-private", "X-Session-ID": "claimed-session"}
	case "replayed MCP session identifier":
		token = ""
		headers["Mcp-Session-Id"] = m.token
	case "another binding's anchor":
		h := m.handoff
		h.Target.BindingID = "vesta-other-binding"
		var err error
		seq, err = m.service.CommitHandoff(m.s.ctx, h)
		if err != nil {
			return err
		}
	case "another role's endpoint":
		tape, err := m.s.openTape("mcp-diablo", "diablo")
		if err != nil {
			return err
		}
		defer tape.Close()
		a, err := agent.New("diablo", tape)
		if err != nil {
			return err
		}
		defer a.Shutdown(m.s.ctx)
		service, err := anramcp.NewService(anramcp.Config{Source: a.Source(), Owner: "diablo", Tape: tape, StatePath: filepath.Join(m.s.root, "diablo-mcp.db")})
		if err != nil {
			return err
		}
		defer service.Close()
		server := httptest.NewServer(service.Handler())
		defer server.Close()
		previous := m.server
		m.server = server
		defer func() { m.server = previous }()
	default:
		return fmt.Errorf("unknown MCP attack %q", attack)
	}
	return m.call(anramcp.ReadContextWindow, token, seq, headers)
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
	for _, private := range []string{m.token, "vesta-lody-private", m.s.root, "fixed request", "fixed answer", "PRIVATE-"} {
		if private != "" && bytes.Contains(m.lastBody, []byte(private)) {
			return fmt.Errorf("MCP error leaked private metadata %q", private)
		}
	}
	return nil
}

func (m *mcpScenario) endCredential(state string) error {
	if state == "expired" {
		m.clock.Add(int64(time.Hour))
		return nil
	}
	if state == "revoked" {
		if err := m.service.Revoke(m.s.ctx, m.token); err != nil {
			return err
		}
		return m.service.Revoke(m.s.ctx, m.token)
	}
	return fmt.Errorf("unknown credential state %q", state)
}

func (m *mcpScenario) roleOnly() error {
	g := m.grant
	g.Permissions = []string{anramcp.ReadRole}
	var err error
	m.token, err = m.service.Authorize(m.s.ctx, g)
	return err
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

func (m *mcpScenario) mismatch(dimension string) error {
	g := m.grant
	g.Window.View = proto.Clone(m.grant.Window.View).(*pb.TapeView)
	switch dimension {
	case "role":
		g.RoleID = "diablo"
	case "binding":
		g.BindingID = "different-binding"
	case "source":
		g.Window.View.Source.TapeId = "different-tape"
	case "snapshotHead":
		n := te.MustParseSeq(g.Window.View.SnapshotHead).Next().String()
		g.Window.View.SnapshotHead = n
		g.Window.View.Scope.SeqE = n
	case "scope":
		g.Window.View.Scope.SeqS = te.MustParseSeq(g.Window.View.Scope.SeqS).Next().String()
	case "permissions":
		g.Permissions = []string{"write_context"}
	case "expiry":
		g.ExpiresAt = time.Unix(0, m.clock.Load())
	default:
		return fmt.Errorf("unknown grant dimension %q", dimension)
	}
	_, m.controlErr = m.service.Authorize(m.s.ctx, g)
	return nil
}

func (m *mcpScenario) badArguments(args string) error {
	args = strings.ReplaceAll(args, "current", m.grant.AnchorSeq)
	return m.rawCall(anramcp.ReadContextWindow, m.token, json.RawMessage(args), nil)
}

// Storage corruption is deliberate fault injection. All service operations and
// reads still use the public control plane and real MCP HTTP transport.
func (m *mcpScenario) damage(damage string) error {
	switch damage {
	case "storage unavailable":
		if err := m.service.Close(); err != nil {
			return err
		}
		m.service = nil // The HTTP handler retains the closed store for this fault.
	case "non-handoff anchor", "changed handoff binding", "unsupported snapshot content":
		seq := m.grant.AnchorSeq
		kind := anchor.Kind
		text := `{"schema":"other.dev/v1","type":"unrelated"}`
		if damage == "changed handoff binding" {
			h := m.handoff
			h.Target.BindingID = "different-binding"
			data, _ := json.Marshal(h)
			text = string(data)
		}
		if damage == "unsupported snapshot content" {
			seq = m.grant.Window.View.Scope.SeqS
			kind = "integration:unsupported"
			text = "PRIVATE-UNSUPPORTED"
		}
		_, err := m.tape.Store(m.tapeContext(), te.NewEntry(te.WithEntryID(te.MustParseSeq(seq)), te.WithEntryKind(kind), te.WithEntryContent(text), te.WithEntryOwner("vesta")))
		if err != nil {
			return err
		}
	case "missing role revision":
		m.server.Close()
		m.server = nil
		if err := m.service.Close(); err != nil {
			return err
		}
		m.service = nil
		db, err := bolt.Open(m.statePath(), 0600, nil)
		if err != nil {
			return err
		}
		err = db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte("anra.mcp.roles.v1")) })
		err = errors.Join(err, db.Close())
		if err != nil {
			return err
		}
		m.service, err = anramcp.NewService(anramcp.Config{Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, StatePath: m.statePath(), Now: func() time.Time { return time.Unix(0, m.clock.Load()) }})
		if err != nil {
			return err
		}
		m.serve()
	case "missing anchor", "missing snapshot entry", "missing snapshot boundary", "corrupt snapshot entry":
		// Stop all users before modifying the physical Tape file offline.
		if err := m.close(); err != nil {
			return err
		}
		seq := m.grant.AnchorSeq
		if damage != "missing anchor" {
			seq = m.grant.Window.View.Scope.SeqS
		}
		if damage == "missing snapshot boundary" {
			seq = m.grant.Window.View.SnapshotHead
		}
		if err := damageMCPPhysicalEntry(m.tapePath(), seq, damage == "corrupt snapshot entry"); err != nil {
			return err
		}
		// Avoid agent recovery here: the fault is specifically tested by MCP.
		var err error
		m.tape, err = bbolt.NewBboltStorage("mcp-session", m.tapePath())
		if err != nil {
			return err
		}
		if err = m.tape.Init(m.tapeContext()); err != nil {
			return err
		}
		m.service, err = anramcp.NewService(anramcp.Config{Source: m.grant.Window.View.Source, Owner: "vesta", Tape: m.tape, StatePath: m.statePath(), Now: func() time.Time { return time.Unix(0, m.clock.Load()) }})
		if err != nil {
			return err
		}
		m.serve()
	default:
		return fmt.Errorf("unknown storage damage %q", damage)
	}
	tool := anramcp.ReadContextWindow
	if damage == "missing role revision" {
		tool = anramcp.ReadRole
	}
	return m.call(tool, m.token, m.grant.AnchorSeq, nil)
}

func damageMCPPhysicalEntry(path, seq string, corrupt bool) error {
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket([]byte("entries")).Bucket([]byte("vesta")).Bucket([]byte("mcp-session"))
		// Locate by decoded sequence rather than reproducing tape-go's key codec.
		cursor := entries.Cursor()
		for key, data := cursor.First(); key != nil; key, data = cursor.Next() {
			var value struct{ Seq string }
			if json.Unmarshal(data, &value) != nil {
				return errors.New("cannot decode physical tape fixture")
			}
			if value.Seq == seq {
				if corrupt {
					return entries.Put(key, []byte("{invalid-json"))
				}
				return entries.Delete(key)
			}
		}
		return errors.New("physical tape entry not found")
	})
}

func (m *mcpScenario) checkTools() error {
	data, err := m.success(anramcp.ReadContextWindow)
	if err != nil {
		return err
	}
	var w anramcp.WindowResult
	if err = json.Unmarshal(data, &w); err != nil {
		return err
	}
	if len(w.Entries) != 2 || w.Entries[0].Kind != "tool_call" || w.Entries[1].Kind != "tool_result" {
		return fmt.Errorf("lost tool entries: %s", data)
	}
	for _, part := range []string{"tool-42", "lookup", "query", "Anra", "answer", "found"} {
		if !bytes.Contains(data, []byte(part)) {
			return fmt.Errorf("missing tool relationship %q", part)
		}
	}
	if bytes.Contains(data, []byte("PRIVATE-")) || bytes.Contains(data, []byte("stateDelta")) || bytes.Contains(data, []byte("extensions")) {
		return fmt.Errorf("private metadata exported: %s", data)
	}
	return nil
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

func (m *mcpScenario) checkPreciseJSON() error {
	return m.checkBusinessJSON(preciseBusinessJSON)
}

func (m *mcpScenario) checkBusinessJSON(expectedJSON string) error {
	if err := m.checkWindowResponse(); err != nil {
		return err
	}
	var rpc struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(m.lastBody, &rpc); err != nil || len(rpc.Result.Content) != 1 {
		return fmt.Errorf("missing text representation: %s", m.lastBody)
	}
	for _, data := range [][]byte{m.lastResult, []byte(rpc.Result.Content[0].Text)} {
		var window anramcp.WindowResult
		if err := json.Unmarshal(data, &window); err != nil || len(window.Entries) != 2 {
			return fmt.Errorf("missing tool entries: %s", data)
		}
		for i, field := range []string{"args", "response"} {
			var content struct {
				Parts []map[string]map[string]json.RawMessage `json:"parts"`
			}
			if err := json.Unmarshal(window.Entries[i].Content, &content); err != nil || len(content.Parts) != 1 {
				return fmt.Errorf("invalid tool content: %s", window.Entries[i].Content)
			}
			name := []string{"functionCall", "functionResponse"}[i]
			var got, want map[string]json.RawMessage
			if json.Unmarshal(content.Parts[0][name][field], &got) != nil || got == nil || json.Unmarshal([]byte(expectedJSON), &want) != nil || len(got) != len(want) {
				return fmt.Errorf("invalid raw %s: %s", field, window.Entries[i].Content)
			}
			for key, expected := range want {
				var actual bytes.Buffer
				if json.Compact(&actual, got[key]) != nil || actual.String() != string(expected) {
					return fmt.Errorf("raw %s.%s changed: %s", field, key, window.Entries[i].Content)
				}
			}
		}
	}
	return nil
}

func (m *mcpScenario) trustOrigin(origin string) error {
	m.server.Close()
	if err := m.service.Close(); err != nil {
		return err
	}
	var err error
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, StatePath: m.statePath(),
		TrustedOrigins: []string{origin}, Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) originRequest(origin, method string) error {
	if origin == "endpoint" {
		origin = m.server.URL
	}
	if method == http.MethodPost {
		return m.call(anramcp.ReadContextWindow, m.token, m.grant.AnchorSeq, map[string]string{
			"Origin": origin, "X-Forwarded-Host": "trusted.example", "X-Forwarded-Proto": "https", "Forwarded": "host=trusted.example;proto=https",
		})
	}
	req, err := http.NewRequestWithContext(m.s.ctx, method, m.server.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Origin", origin)
	req.Header.Set("Accept", "text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.Header.Get("Cache-Control") != "no-store" {
		return errors.New("Origin rejection is cacheable")
	}
	m.lastStatus = res.StatusCode
	m.lastBody, err = io.ReadAll(res.Body)
	return err
}

func (m *mcpScenario) originHeaders(shape string) error {
	trusted, evil := "https://trusted.example", "https://evil.example"
	var values []string
	switch shape {
	case "empty":
		values = []string{""}
	case "repeated trusted":
		values = []string{trusted, trusted}
	case "trusted and evil":
		values = []string{trusted, evil}
	case "comma separated":
		values = []string{trusted + ", " + evil}
	case "space separated":
		values = []string{trusted + " " + evil}
	default:
		return fmt.Errorf("unknown Origin header fixture %q", shape)
	}
	args, _ := json.Marshal(map[string]string{"anchorSeq": m.grant.AnchorSeq})
	return m.rawCall(anramcp.ReadContextWindow, m.token, args, http.Header{"Origin": values})
}

func (m *mcpScenario) checkBoundaries() error {
	data, err := m.success(anramcp.ReadContextWindow)
	if err != nil {
		return err
	}
	var w anramcp.WindowResult
	if err = json.Unmarshal(data, &w); err != nil {
		return err
	}
	if len(w.Entries) != 3 || w.Entries[0].Boundary == nil || w.Entries[0].Boundary.TurnID != "boundary-turn" || w.Entries[1].Boundary == nil || w.Entries[1].Boundary.Type != "turn.closed" || w.Entries[2].Boundary == nil || w.Entries[2].Boundary.Origin == nil || w.Entries[2].Boundary.Origin.SessionID != "origin-session" || w.Entries[2].Boundary.Origin.EventID != "origin-event" {
		return fmt.Errorf("boundaries lost: %s", data)
	}
	if bytes.Contains(data, []byte("PRIVATE-")) {
		return fmt.Errorf("private boundary fields exported: %s", data)
	}
	return nil
}

func (m *mcpScenario) checkEmpty() error {
	data, err := m.success(anramcp.ReadContextWindow)
	if err != nil {
		return err
	}
	var w anramcp.WindowResult
	if err = json.Unmarshal(data, &w); err != nil {
		return err
	}
	if !w.IsComplete || w.Entries == nil || len(w.Entries) != 0 || len(w.View) == 0 {
		return fmt.Errorf("not a complete empty window: %s", data)
	}
	return nil
}

func (m *mcpScenario) immutableRole() error {
	if err := m.service.SaveRole(m.s.ctx, m.role); err != nil {
		return fmt.Errorf("idempotent role save: %w", err)
	}
	r := m.role
	r.Definition = json.RawMessage(`{"instructions":"replacement"}`)
	if err := m.service.SaveRole(m.s.ctx, r); !errors.Is(err, anramcp.ErrFailedPrecondition) {
		return fmt.Errorf("role overwrite: %v", err)
	}
	return m.checkBoth()
}

func (m *mcpScenario) readOnly() error {
	tapeBefore, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	stateBefore, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	if bytes.Contains(tapeBefore, []byte(m.token)) || bytes.Contains(stateBefore, []byte(m.token)) {
		return errors.New("plaintext credential was persisted")
	}
	for i := 0; i < 3; i++ {
		if err := m.checkBoth(); err != nil {
			return err
		}
	}
	tapeAfter, err := os.ReadFile(m.tapePath())
	if err != nil {
		return err
	}
	stateAfter, err := os.ReadFile(m.statePath())
	if err != nil {
		return err
	}
	if sha256.Sum256(tapeBefore) != sha256.Sum256(tapeAfter) || sha256.Sum256(stateBefore) != sha256.Sum256(stateAfter) {
		return errors.New("read-only tools changed persistent state")
	}
	return nil
}
