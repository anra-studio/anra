package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	taperunner "github.com/scbizu/tape-go/pkg/runner"
	tapeentry "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

const (
	defaultMaxTurns        = 32
	runnerMessageExtension = "anra:runner-message/v1"
)

var (
	ErrRuntimeNotConfigured = errors.New("agent: runtime is not configured")
	ErrRuntimeTurnLimit     = errors.New("agent: runtime turn limit reached")
)

// Tool is a capability that Anra can expose to its model runner. Protocol
// integrations such as MCP and A2A can implement this boundary without
// entering the runtime loop.
type Tool interface {
	Spec() taperunner.ToolSpec
	Execute(context.Context, json.RawMessage) (json.RawMessage, error)
}

type runtimeConfig struct {
	runner            taperunner.Runner
	model             string
	systemInstruction string
	tools             []Tool
	toolByName        map[string]Tool
	maxTurns          int
}

// Option configures optional Anra capabilities.
type Option func(*Anra) error

func WithRunner(value taperunner.Runner) Option {
	return func(anra *Anra) error {
		if value == nil {
			return errors.New("agent: nil runner")
		}
		anra.runtime.runner = value
		return nil
	}
}

func WithModel(model string) Option {
	return func(anra *Anra) error {
		anra.runtime.model = model
		return nil
	}
}

func WithSystemInstruction(instruction string) Option {
	return func(anra *Anra) error {
		anra.runtime.systemInstruction = instruction
		return nil
	}
}

func WithTool(value Tool) Option {
	return func(anra *Anra) error {
		if value == nil {
			return errors.New("agent: nil tool")
		}
		spec := value.Spec()
		if spec.Name == "" {
			return errors.New("agent: tool has empty name")
		}
		if len(spec.InputSchema) == 0 || !json.Valid(spec.InputSchema) {
			return fmt.Errorf("agent: tool %q has invalid input schema", spec.Name)
		}
		if _, exists := anra.runtime.toolByName[spec.Name]; exists {
			return fmt.Errorf("agent: duplicate tool %q", spec.Name)
		}
		anra.runtime.tools = append(anra.runtime.tools, value)
		anra.runtime.toolByName[spec.Name] = value
		return nil
	}
}

func WithMaxTurns(limit int) Option {
	return func(anra *Anra) error {
		if limit <= 0 {
			return errors.New("agent: max turns must be positive")
		}
		anra.runtime.maxTurns = limit
		return nil
	}
}

// Run appends one user message and drives Anra until the runner returns final
// text. Tool calls and results are persisted before the next model turn.
func (a *Anra) Run(ctx context.Context, input string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.runtime.runner == nil {
		return "", ErrRuntimeNotConfigured
	}
	if strings.TrimSpace(input) == "" {
		return "", errors.New("agent: empty runtime input")
	}
	if err := a.appendMessage(ctx, taperunner.Message{Role: taperunner.RoleUser, Text: input}); err != nil {
		return "", err
	}

	for turn := 0; turn < a.runtime.maxTurns; turn++ {
		messages, err := a.runtimeMessages(ctx)
		if err != nil {
			return "", err
		}
		response, err := a.runtime.runner.RunTurn(ctx, taperunner.Request{
			Model:             a.runtime.model,
			SystemInstruction: a.runtime.systemInstruction,
			Messages:          messages,
			Tools:             a.toolSpecs(),
		})
		if err != nil {
			return "", fmt.Errorf("agent: run turn: %w", err)
		}
		if err := validateResponse(response); err != nil {
			return "", err
		}
		if response.Output != "" {
			if err := a.appendMessage(ctx, taperunner.Message{Role: taperunner.RoleAssistant, Text: response.Output}); err != nil {
				return "", err
			}
			return response.Output, nil
		}

		callMessage := taperunner.Message{Role: taperunner.RoleAssistant, ToolCalls: response.ToolCalls}
		if err := a.appendMessage(ctx, callMessage); err != nil {
			return "", err
		}
		results := make([]taperunner.ToolResult, 0, len(response.ToolCalls))
		for _, call := range response.ToolCalls {
			result := a.executeTool(ctx, call)
			results = append(results, result)
		}
		if err := a.appendMessage(ctx, taperunner.Message{Role: taperunner.RoleTool, ToolResults: results}); err != nil {
			return "", err
		}
	}
	return "", ErrRuntimeTurnLimit
}

func (a *Anra) executeTool(ctx context.Context, call taperunner.ToolCall) taperunner.ToolResult {
	result := taperunner.ToolResult{ID: call.ID, Name: call.Name}
	tool, ok := a.runtime.toolByName[call.Name]
	if !ok {
		result.Result = errorPayload(fmt.Errorf("unknown tool %q", call.Name))
		return result
	}
	value, err := tool.Execute(ctx, call.Arguments)
	if err != nil {
		result.Result = errorPayload(err)
		return result
	}
	if len(value) == 0 || !json.Valid(value) {
		result.Result = errorPayload(errors.New("tool returned invalid JSON"))
		return result
	}
	result.Result = value
	return result
}

func errorPayload(err error) json.RawMessage {
	payload, _ := json.Marshal(map[string]string{"error": err.Error()})
	return payload
}

func (a *Anra) toolSpecs() []taperunner.ToolSpec {
	specs := make([]taperunner.ToolSpec, len(a.runtime.tools))
	for index, tool := range a.runtime.tools {
		specs[index] = tool.Spec()
	}
	return specs
}

func validateResponse(response taperunner.Response) error {
	if response.Output != "" && len(response.ToolCalls) != 0 {
		return errors.New("agent: runner returned output and tool calls")
	}
	if response.Output == "" && len(response.ToolCalls) == 0 {
		return errors.New("agent: runner returned an empty response")
	}
	for index, call := range response.ToolCalls {
		if call.ID == "" || call.Name == "" || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			return fmt.Errorf("agent: runner returned invalid tool call %d", index)
		}
	}
	return nil
}

func (a *Anra) appendMessage(ctx context.Context, message taperunner.Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("agent: encode runtime message: %w", err)
	}
	value := tapeentry.CustomEntry{
		Entry: tapeentry.NewEntry(
			tapeentry.WithEntryKind(messageEntryKind(message)),
			tapeentry.WithEntryContent(message.Text),
			tapeentry.WithEntryOwner(a.principal),
		),
		Extensions: map[string]any{runnerMessageExtension: string(payload)},
	}
	if _, err := a.tape.Store(owner.WithOwnerId(ctx, a.principal), value); err != nil {
		return fmt.Errorf("agent: append runtime message: %w", err)
	}
	return nil
}

func messageEntryKind(message taperunner.Message) tapeentry.EntryKind {
	switch message.Role {
	case taperunner.RoleUser:
		return tapeentry.EntryUser
	case taperunner.RoleTool:
		return tapeentry.EntryToolResult
	case taperunner.RoleAssistant:
		if len(message.ToolCalls) != 0 {
			return tapeentry.EntryToolCall
		}
		return tapeentry.EntryAssistant
	default:
		return tapeentry.EntrySystem
	}
}

func (a *Anra) runtimeMessages(ctx context.Context) ([]taperunner.Message, error) {
	tapeCtx := owner.WithOwnerId(ctx, a.principal)
	tapeView, err := a.tape.Get(tapeCtx)
	if err != nil {
		return nil, fmt.Errorf("agent: get runtime tape: %w", err)
	}
	if tapeView.Scope.SeqE.IsZero() {
		return nil, nil
	}
	entries, err := a.tape.Range(tapeCtx, view.EntryRange{SeqS: tapeentry.SeqFromUint64(1), SeqE: tapeView.Scope.SeqE.Next()})
	if err != nil {
		return nil, fmt.Errorf("agent: read runtime tape: %w", err)
	}
	messages := make([]taperunner.Message, 0, len(entries.Raw))
	for _, raw := range entries.Raw {
		message, ok, err := runtimeMessage(raw)
		if err != nil {
			return nil, fmt.Errorf("agent: decode runtime entry %s: %w", raw.GetID(), err)
		}
		if ok {
			messages = append(messages, message)
		}
	}
	return messages, nil
}

func runtimeMessage(raw tapeentry.EntryLike) (taperunner.Message, bool, error) {
	var extensions map[string]any
	switch value := raw.(type) {
	case tapeentry.CustomEntry:
		extensions = value.Extensions
	case *tapeentry.CustomEntry:
		extensions = value.Extensions
	default:
		return basicRuntimeMessage(raw)
	}
	payload, ok := extensions[runnerMessageExtension].(string)
	if !ok {
		return basicRuntimeMessage(raw)
	}
	var message taperunner.Message
	if err := json.Unmarshal([]byte(payload), &message); err != nil {
		return taperunner.Message{}, false, err
	}
	return message, true, nil
}

func basicRuntimeMessage(raw tapeentry.EntryLike) (taperunner.Message, bool, error) {
	switch raw.GetKind() {
	case tapeentry.EntryUser:
		return taperunner.Message{Role: taperunner.RoleUser, Text: raw.GetSummary()}, true, nil
	case tapeentry.EntryAssistant:
		return taperunner.Message{Role: taperunner.RoleAssistant, Text: raw.GetSummary()}, true, nil
	case tapeentry.EntryToolCall, tapeentry.EntryToolResult:
		return taperunner.Message{}, false, errors.New("structured tool entry has no Anra runtime payload")
	default:
		return taperunner.Message{}, false, nil
	}
}
