package agent

import (
	"context"
	"encoding/json"

	"github.com/anra-studio/anra/pkg/model/anchor"
	"github.com/anra-studio/anra/pkg/model/entry"

	"github.com/scbizu/tape-go/pkg/runner"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// eventStorage preserves the tape's tool entry kinds and projects the former
// Anra message payload into ADK events on read. Existing tapes remain append-only.
// Control anchors and aborted Agent Turn intervals are excluded from model history.
type eventStorage struct {
	storage.TapeStorage
	aborted []view.EntryRange
}

func (s *eventStorage) Unwrap() storage.TapeStorage { return s.TapeStorage }

func (s *eventStorage) Store(ctx context.Context, raw te.EntryLike) (te.EntryLike, error) {
	value := entry.FromTape(raw)
	var event session.Event
	if _, err := value.DecodeExtension(entry.ADKEvent, &event); err != nil {
		return nil, err
	}
	if event.Content != nil {
		for _, part := range event.Content.Parts {
			if part.FunctionCall != nil {
				value = value.WithKind(te.EntryToolCall)
			}
			if part.FunctionResponse != nil {
				value = value.WithKind(te.EntryToolResult)
			}
		}
	}
	return s.TapeStorage.Store(ctx, value)
}

func (s *eventStorage) Range(ctx context.Context, r view.EntryRange, opts ...storage.RangeBy) (view.EntryView, error) {
	result, err := s.TapeStorage.Range(ctx, r, opts...)
	if err != nil {
		return result, err
	}
	filtered := result.Raw[:0]
	for _, raw := range result.Raw {
		if raw.GetKind().IsAnchor() && raw.GetKind() == "anchor:custom" {
			record, err := anchor.Decode(raw)
			if err != nil {
				return view.EntryView{}, err
			}
			// Only native execution records are control metadata here.
			// Other schemas keep their storage representation.
			if record != nil {
				continue
			}
		}
		skip := false
		for _, interval := range s.aborted {
			if raw.GetID().Cmp(interval.SeqS) >= 0 && raw.GetID().Cmp(interval.SeqE) < 0 {
				skip = true
				break
			}
		}
		if !skip {
			filtered = append(filtered, raw)
		}
	}
	result.Raw = filtered
	for i, raw := range result.Raw {
		value := entry.FromTape(raw)
		if _, exists := value.Extension(entry.ADKEvent); exists {
			result.Raw[i] = value.Tape()
			continue
		}
		var message runner.Message
		exists, err := value.DecodeExtension(entry.RunnerMessage, &message)
		if err != nil {
			return view.EntryView{}, err
		}
		if !exists {
			result.Raw[i] = value.Tape()
			continue
		}

		content := &genai.Content{Role: genai.RoleModel}
		author := appName
		if message.Role == runner.RoleUser || message.Role == runner.RoleTool {
			content.Role = genai.RoleUser
		}
		if message.Role == runner.RoleUser {
			author = "user"
		}
		if message.Text != "" {
			content.Parts = append(content.Parts, &genai.Part{Text: message.Text})
		}
		for _, call := range message.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal(call.Arguments, &args); err != nil {
				return view.EntryView{}, err
			}
			content.Parts = append(content.Parts, &genai.Part{FunctionCall: &genai.FunctionCall{ID: call.ID, Name: call.Name, Args: args}})
		}
		for _, toolResult := range message.ToolResults {
			var decoded any
			if err := json.Unmarshal(toolResult.Result, &decoded); err != nil {
				return view.EntryView{}, err
			}
			object, ok := decoded.(map[string]any)
			if !ok {
				object = map[string]any{"output": decoded}
			}
			content.Parts = append(content.Parts, &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: toolResult.ID, Name: toolResult.Name, Response: object}})
		}
		event := session.Event{ID: raw.GetID().String(), Timestamp: raw.GetTimestamp(), Author: author, LLMResponse: model.LLMResponse{Content: content}}
		value, err = value.WithJSONExtension(entry.ADKEvent, event)
		if err != nil {
			return view.EntryView{}, err
		}
		// TapeAdapter currently requires tape-go's concrete extension carrier.
		result.Raw[i] = value.Tape()
	}
	return result, nil
}
