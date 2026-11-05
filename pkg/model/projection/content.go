package projection

import (
	"encoding/json"

	"google.golang.org/genai"
)

// Content is public model content projected from stored ADK content. Its role
// records history, not execution authority; the enclosing channel owns that.
// The content and tool structures select public fields and keep business JSON
// opaque, including number lexemes and keys that resemble protocol fields.
// Function-response media uses the SDK's public media types directly.
type Content struct {
	Role  string  `json:"role,omitempty"`
	Parts []*Part `json:"parts,omitempty"`
}

type Part struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

type FunctionResponse struct {
	ID           string                           `json:"id,omitempty"`
	Name         string                           `json:"name,omitempty"`
	Response     json.RawMessage                  `json:"response,omitempty"`
	WillContinue *bool                            `json:"willContinue,omitempty"`
	Scheduling   genai.FunctionResponseScheduling `json:"scheduling,omitempty"`
	Parts        []*genai.FunctionResponsePart    `json:"parts,omitempty"`
}
