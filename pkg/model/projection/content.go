package projection

import (
	"encoding/json"

	"google.golang.org/genai"
)

// Content wraps SDK model content, preserving its parts and historical role.
// Channel and provenance determine authority, independently of that role.
type Content struct {
	genai.Content
	Parts []*Part `json:"parts,omitempty"`
}

// Part preserves SDK content, including media and code, while excluding provider
// signatures and execution metadata from its JSON representation. Tool payloads
// override the SDK's maps to preserve opaque business JSON and number lexemes.
type Part struct {
	genai.Part
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
	ToolCall         *ToolCall         `json:"toolCall,omitempty"`
	ToolResponse     *ToolResponse     `json:"toolResponse,omitempty"`
}

// partFields omits Part's codec methods. The shallower fields in partJSON shadow
// SDK metadata during decoding and remain absent when encoding public content.
type partFields Part

type partJSON struct {
	partFields
	ThoughtSignature json.RawMessage `json:"thoughtSignature,omitempty"`
	PartMetadata     json.RawMessage `json:"partMetadata,omitempty"`
}

func (p *Part) UnmarshalJSON(data []byte) error {
	var decoded partJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = Part(decoded.partFields)
	return nil
}

func (p *Part) MarshalJSON() ([]byte, error) {
	return json.Marshal(partJSON{partFields: partFields(*p)})
}

type FunctionCall struct {
	genai.FunctionCall
	Args json.RawMessage `json:"args,omitempty"`
}

type FunctionResponse struct {
	genai.FunctionResponse
	Response json.RawMessage `json:"response,omitempty"`
}

type ToolCall struct {
	genai.ToolCall
	Args json.RawMessage `json:"args,omitempty"`
}

type ToolResponse struct {
	genai.ToolResponse
	Response json.RawMessage `json:"response,omitempty"`
}
