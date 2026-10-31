package a2a

import (
	"bytes"
	"encoding/json"
	"strings"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
)

// ContextContent is the version 1 public model-input projection shared by A2A
// and MCP. Its protocol structures are explicit: upgrading an SDK must not
// silently expand the projection's supported fields. Business JSON is opaque,
// including number lexemes and keys that resemble protocol fields.
type ContextContent struct {
	Role  string         `json:"role,omitempty"`
	Parts []*ContextPart `json:"parts,omitempty" validate:"dive,required"`
}

type ContextPart struct {
	Text             string                   `json:"text,omitempty"`
	FunctionCall     *ContextFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *ContextFunctionResponse `json:"functionResponse,omitempty"`
}

type ContextFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty" validate:"required"`
	Args json.RawMessage `json:"args,omitempty" validate:"omitempty,jsonobject"`
}

type ContextFunctionResponse struct {
	ID           string                 `json:"id,omitempty"`
	Name         string                 `json:"name,omitempty" validate:"required"`
	Response     json.RawMessage        `json:"response,omitempty" validate:"omitempty,jsonobject"`
	WillContinue *bool                  `json:"willContinue,omitempty"`
	Scheduling   string                 `json:"scheduling,omitempty" validate:"omitempty,oneof=SCHEDULING_UNSPECIFIED SILENT WHEN_IDLE INTERRUPT"`
	Parts        []*contextResponsePart `json:"parts,omitempty" validate:"dive,required"`
}

type contextResponsePart struct {
	InlineData *contextResponseBlob `json:"inlineData,omitempty"`
	FileData   *contextResponseFile `json:"fileData,omitempty"`
}

type contextResponseBlob struct {
	MIMEType    string `json:"mimeType,omitempty"`
	Data        []byte `json:"data,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type contextResponseFile struct {
	FileURI     string `json:"fileUri,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

func contextExtensions(e te.EntryLike) map[string]any {
	switch value := e.(type) {
	case te.CustomEntry:
		return value.Extensions
	case *te.CustomEntry:
		return value.Extensions
	default:
		return nil
	}
}

func projectContext(payload string) (*ContextContent, string, error) {
	// Execution metadata outside content is intentionally private and ignored.
	var event struct {
		Content json.RawMessage `json:"content" validate:"required,jsonobject"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil || validateStruct(event) != nil {
		return nil, "", ErrUnsupported
	}
	var input struct {
		Role  string            `json:"role"`
		Parts []json.RawMessage `json:"parts"`
	}
	if strictContextJSON(event.Content, &input) != nil {
		return nil, "", ErrUnsupported
	}
	content := &ContextContent{Role: input.Role}
	var summary strings.Builder
	for _, raw := range input.Parts {
		var visibility struct {
			Thought bool `json:"thought"`
		}
		if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, &visibility) != nil {
			return nil, "", ErrUnsupported
		}
		if visibility.Thought {
			continue
		}
		var part struct {
			ContextPart
			Thought          bool            `json:"thought"`
			ThoughtSignature json.RawMessage `json:"thoughtSignature"`
		}
		// DisallowUnknownFields recurses through every protocol object, including
		// response parts and media. It stops at opaque args/response RawMessages.
		if strictContextJSON(raw, &part) != nil || validateStruct(part.ContextPart) != nil {
			return nil, "", ErrUnsupported
		}
		if part.Text == "" && part.FunctionCall == nil && part.FunctionResponse == nil {
			continue
		}
		content.Parts = append(content.Parts, &part.ContextPart)
		summary.WriteString(part.Text)
	}
	return content, summary.String(), nil
}

// Input RawMessages have already passed json.Unmarshal, so each contains one
// complete JSON value. No interface{} or float64 participates in projection.
func strictContextJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}
