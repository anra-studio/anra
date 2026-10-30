package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/genai"
)

var errUnsupportedEntry = &Error{"failed_precondition", "committed window contains an unsupported entry type or projection"}

// ProjectedEntry exposes only whitelisted model input or a boundary. It never
// serializes the storage owner, arbitrary extensions, state deltas, or thoughts.
type ProjectedEntry struct {
	Seq       string          `json:"seq"`
	Kind      string          `json:"kind"`
	Timestamp time.Time       `json:"timestamp"`
	Content   json.RawMessage `json:"content,omitempty"`
	Boundary  *Boundary       `json:"boundary,omitempty"`
}

// Boundary preserves turn and integration session relationships without
// exporting private execution ownership metadata or delegated task prompts.
type Boundary struct {
	Schema   string  `json:"schema"`
	Type     string  `json:"type"`
	TurnID   string  `json:"turnId,omitempty"`
	StartSeq string  `json:"startSeq,omitempty"`
	Outcome  string  `json:"outcome,omitempty"`
	Origin   *Origin `json:"origin,omitempty"`
}

type WindowResult struct {
	View       json.RawMessage  `json:"view"`
	Entries    []ProjectedEntry `json:"entries"`
	IsComplete bool             `json:"isComplete"`
}

// walkWindow checks every persisted sequence, including the excluded head.
// Bounded batches and a raw input limit prevent loading an unbounded Tape into
// memory. Arithmetic remains arbitrary precision throughout.
func (s *Service) walkWindow(ctx context.Context, ref *pb.TapeView, visit func(te.EntryLike, bool) error) error {
	if validateWindow(WindowRef{ref}) != nil {
		return ErrFailedPrecondition
	}
	start := te.MustParseSeq(ref.Scope.SeqS)
	head := te.MustParseSeq(ref.SnapshotHead)
	end := head.Next()
	expected := start
	rawBytes, count := 0, 0
	for expected.Cmp(end) < 0 {
		batchEnd := expected
		for i := 0; i < 128 && batchEnd.Cmp(end) < 0; i++ {
			batchEnd = batchEnd.Next()
		}
		batch, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{SeqS: expected, SeqE: batchEnd})
		if err != nil {
			return tapeReadError(err, ErrFailedPrecondition)
		}
		for _, raw := range batch.Raw {
			if raw.GetID() != expected || raw.GetOwner() != s.config.Owner {
				return ErrFailedPrecondition
			}
			data, err := json.Marshal(raw)
			if err != nil {
				return ErrFailedPrecondition
			}
			rawBytes += len(data)
			count++
			if rawBytes > 16*s.config.MaxResponseBytes || count > 10000 {
				return ErrFailedPrecondition
			}
			if err := visit(raw, expected == head); err != nil {
				return err
			}
			expected = expected.Next()
		}
		if expected != batchEnd {
			return ErrFailedPrecondition
		}
	}
	return nil
}

func (s *Service) windowDigest(ctx context.Context, ref *pb.TapeView) (string, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	err := s.walkWindow(ctx, ref, func(raw te.EntryLike, _ bool) error { return enc.Encode(raw) })
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) window(ctx context.Context, h *Handoff, committed *seal) (*WindowResult, error) {
	result := &WindowResult{View: anchor.ProtoJSON(h.Context.View), Entries: []ProjectedEntry{}, IsComplete: true}
	hash := sha256.New()
	enc := json.NewEncoder(hash)
	calls := make(map[string]string)
	projectedBytes := 0
	err := s.walkWindow(ctx, h.Context.View, func(raw te.EntryLike, boundary bool) error {
		if err := enc.Encode(raw); err != nil {
			return ErrFailedPrecondition
		}
		if boundary {
			return nil
		}
		e, err := project(raw, calls)
		if err != nil {
			return err
		}
		if e != nil {
			data, err := json.Marshal(e)
			if err != nil {
				return ErrFailedPrecondition
			}
			projectedBytes += len(data)
			if projectedBytes > s.config.MaxResponseBytes {
				return ErrFailedPrecondition
			}
			result.Entries = append(result.Entries, *e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(calls) != 0 || hex.EncodeToString(hash.Sum(nil)) != committed.WindowDigest {
		return nil, ErrFailedPrecondition
	}
	return result, nil
}

func project(raw te.EntryLike, calls map[string]string) (*ProjectedEntry, error) {
	e := &ProjectedEntry{Seq: raw.GetID().String(), Kind: string(raw.GetKind()), Timestamp: raw.GetTimestamp()}
	if raw.GetKind() == anchor.Kind {
		if len(calls) != 0 {
			return nil, ErrFailedPrecondition
		}
		r, err := anchor.Decode(raw)
		if err != nil {
			return nil, ErrFailedPrecondition
		}
		if r != nil {
			e.Boundary = &Boundary{Schema: r.Schema, Type: string(r.Type), TurnID: r.TurnID, StartSeq: r.StartSeq, Outcome: r.Outcome}
			return e, nil
		}
		var h Handoff
		if decode([]byte(raw.GetSummary()), &h) != nil || validate.Struct(h) != nil || validateWindow(h.Context) != nil {
			return nil, errUnsupportedEntry
		}
		e.Boundary = &Boundary{Schema: h.Schema, Type: h.Type, Origin: h.Origin}
		return e, nil
	}
	if err := checkModelProjection(raw); err != nil {
		return nil, err
	}
	exported, err := (a2a.ContextExporter{}).Export(raw)
	if err != nil {
		return nil, errUnsupportedEntry
	}
	if exported.Payload == nil {
		return nil, errUnsupportedEntry
	}
	e.Content = exported.Payload.JsonData
	if exported.Payload.MediaType == a2a.ContextMediaType {
		var content genai.Content
		if json.Unmarshal(e.Content, &content) != nil {
			return nil, errUnsupportedEntry
		}
		if len(content.Parts) == 0 {
			return nil, nil
		} // thought-only input has no public model content
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				c := part.FunctionCall
				ref := toolReference{c.ID, c.Name}
				key := ref.key()
				if validate.Struct(ref) != nil || calls[key] != "" {
					return nil, ErrFailedPrecondition
				}
				calls[key] = c.Name
			}
			if part.FunctionResponse != nil {
				r := part.FunctionResponse
				ref := toolReference{r.ID, r.Name}
				key := ref.key()
				if validate.Struct(ref) != nil || calls[key] != r.Name {
					return nil, ErrFailedPrecondition
				}
				delete(calls, key)
			}
		}
	}
	return e, nil
}

// ContextExporter deliberately omits non-model metadata. MCP additionally
// rejects unsupported visible content, so omission cannot claim completeness
// for images, executable code, unknown future parts, or an unrecognized codec.
func checkModelProjection(raw te.EntryLike) error {
	var extensions map[string]any
	switch e := raw.(type) {
	case te.CustomEntry:
		extensions = e.Extensions
	case *te.CustomEntry:
		extensions = e.Extensions
	}
	if _, legacy := extensions["anra:runner-message/v1"]; legacy {
		return errUnsupportedEntry
	}
	value, exists := extensions["adk:event"]
	if !exists {
		return nil
	}
	payload, ok := value.(string)
	if !ok {
		return errUnsupportedEntry
	}
	var event struct {
		Content *struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(payload), &event) != nil || event.Content == nil {
		return errUnsupportedEntry
	}
	for _, data := range event.Content.Parts {
		var part map[string]json.RawMessage
		if json.Unmarshal(data, &part) != nil || part == nil {
			return errUnsupportedEntry
		}
		if string(part["thought"]) == "true" {
			continue
		}
		for name := range part {
			switch name {
			case "text", "functionCall", "functionResponse", "thought", "thoughtSignature":
			default:
				return errUnsupportedEntry
			}
		}
	}
	return nil
}

type toolReference struct {
	ID   string
	Name string `validate:"required"`
}

func (r toolReference) key() string {
	if r.ID != "" {
		return "id:" + r.ID
	}
	return "name:" + r.Name
}
