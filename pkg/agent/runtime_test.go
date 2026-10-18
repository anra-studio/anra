package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/scbizu/anra/pkg/agent"
	taperunner "github.com/scbizu/tape-go/pkg/runner"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

func TestRunPersistsAndReplaysToolLoop(t *testing.T) {
	store := newStore(t, "p1")
	runner := &scriptedRunner{responses: []taperunner.Response{
		{ToolCalls: []taperunner.ToolCall{{ID: "call-1", Name: "echo", Arguments: json.RawMessage(`{"text":"hello"}`)}}},
		{Output: "finished"},
	}}
	tool := &stubTool{result: json.RawMessage(`{"text":"hello"}`)}
	anra, err := agent.New(
		"p1",
		store,
		agent.WithRunner(runner),
		agent.WithModel("test-model"),
		agent.WithSystemInstruction("be concise"),
		agent.WithTool(tool),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	output, err := anra.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if output != "finished" {
		t.Fatalf("Run() = %q", output)
	}
	if tool.calls != 1 || string(tool.arguments) != `{"text":"hello"}` {
		t.Fatalf("tool calls = %d, arguments = %s", tool.calls, tool.arguments)
	}
	if len(runner.requests) != 2 {
		t.Fatalf("RunTurn calls = %d", len(runner.requests))
	}
	first := runner.requests[0]
	if first.Model != "test-model" || first.SystemInstruction != "be concise" || len(first.Tools) != 1 {
		t.Fatalf("first request configuration = %#v", first)
	}
	second := runner.requests[1]
	if len(second.Messages) != 3 || second.Messages[0].Role != taperunner.RoleUser ||
		second.Messages[1].Role != taperunner.RoleAssistant || len(second.Messages[1].ToolCalls) != 1 ||
		second.Messages[2].Role != taperunner.RoleTool || len(second.Messages[2].ToolResults) != 1 {
		t.Fatalf("second request messages = %#v", second.Messages)
	}

	entries, err := store.Range(
		owner.WithOwnerId(context.Background(), "p1"),
		view.EntryRange{SeqS: tapeentry.SeqFromUint64(1), SeqE: tapeentry.SeqFromUint64(5)},
	)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	wantKinds := []tapeentry.EntryKind{
		tapeentry.EntryUser,
		tapeentry.EntryToolCall,
		tapeentry.EntryToolResult,
		tapeentry.EntryAssistant,
	}
	if len(entries.Raw) != len(wantKinds) {
		t.Fatalf("stored entries = %d", len(entries.Raw))
	}
	for index, want := range wantKinds {
		if got := entries.Raw[index].GetKind(); got != want {
			t.Fatalf("entry %d kind = %q, want %q", index, got, want)
		}
	}
}

func TestRunRebuildsContextFromTape(t *testing.T) {
	store := newStore(t, "p1")
	firstRunner := &scriptedRunner{responses: []taperunner.Response{{Output: "first answer"}}}
	first, err := agent.New("p1", store, agent.WithRunner(firstRunner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Run(context.Background(), "first question"); err != nil {
		t.Fatal(err)
	}

	secondRunner := &scriptedRunner{responses: []taperunner.Response{{Output: "second answer"}}}
	restarted, err := agent.New("p1", store, agent.WithRunner(secondRunner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Run(context.Background(), "second question"); err != nil {
		t.Fatal(err)
	}
	messages := secondRunner.requests[0].Messages
	if len(messages) != 3 || messages[0].Text != "first question" ||
		messages[1].Text != "first answer" || messages[2].Text != "second question" {
		t.Fatalf("replayed messages = %#v", messages)
	}
}

func TestRunKeepsMemberHistoryIsolated(t *testing.T) {
	store := newStore(t, "p1")
	if err := store.Init(owner.WithOwnerId(context.Background(), "p2")); err != nil {
		t.Fatal(err)
	}
	oneRunner := &scriptedRunner{responses: []taperunner.Response{{Output: "one answer"}, {Output: "one follow-up"}}}
	twoRunner := &scriptedRunner{responses: []taperunner.Response{{Output: "two answer"}}}
	one, err := agent.New("p1", store, agent.WithRunner(oneRunner))
	if err != nil {
		t.Fatal(err)
	}
	two, err := agent.New("p2", store, agent.WithRunner(twoRunner))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := one.Run(context.Background(), "one question"); err != nil {
		t.Fatal(err)
	}
	if _, err := two.Run(context.Background(), "two question"); err != nil {
		t.Fatal(err)
	}
	if _, err := one.Run(context.Background(), "one follow-up question"); err != nil {
		t.Fatal(err)
	}
	if messages := twoRunner.requests[0].Messages; len(messages) != 1 || messages[0].Text != "two question" {
		t.Fatalf("p2 context = %#v", messages)
	}
	if messages := oneRunner.requests[1].Messages; len(messages) != 3 || messages[0].Text != "one question" || messages[1].Text != "one answer" || messages[2].Text != "one follow-up question" {
		t.Fatalf("p1 context = %#v", messages)
	}
}

func TestRunRequiresConfiguredRuntimeAndStopsAtLimit(t *testing.T) {
	store := newStore(t, "p1")
	withoutRunner, err := agent.New("p1", store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutRunner.Run(context.Background(), "hello"); !errors.Is(err, agent.ErrRuntimeNotConfigured) {
		t.Fatalf("Run() error = %v, want ErrRuntimeNotConfigured", err)
	}

	looping := &scriptedRunner{responses: []taperunner.Response{
		{ToolCalls: []taperunner.ToolCall{{ID: "one", Name: "missing", Arguments: json.RawMessage(`{}`)}}},
	}}
	limited, err := agent.New("p1", newStore(t, "p1"), agent.WithRunner(looping), agent.WithMaxTurns(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := limited.Run(context.Background(), "loop"); !errors.Is(err, agent.ErrRuntimeTurnLimit) {
		t.Fatalf("Run() error = %v, want ErrRuntimeTurnLimit", err)
	}
}

type scriptedRunner struct {
	responses []taperunner.Response
	requests  []taperunner.Request
}

func (runner *scriptedRunner) RunTurn(_ context.Context, request taperunner.Request) (taperunner.Response, error) {
	runner.requests = append(runner.requests, request)
	index := len(runner.requests) - 1
	if index >= len(runner.responses) {
		return taperunner.Response{}, errors.New("unexpected RunTurn call")
	}
	return runner.responses[index], nil
}

type stubTool struct {
	result    json.RawMessage
	arguments json.RawMessage
	calls     int
}

func (tool *stubTool) Spec() taperunner.ToolSpec {
	return taperunner.ToolSpec{
		Name:        "echo",
		Description: "returns its input",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (tool *stubTool) Execute(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	tool.calls++
	tool.arguments = append(tool.arguments[:0], arguments...)
	return tool.result, nil
}
