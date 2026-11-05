package projection

import "encoding/json"

// Event is public model content projected from stored ADK content. Its role
// records history, not execution authority; the enclosing channel owns that.
// Explicit fields keep SDK upgrades from expanding disclosure. Business JSON is opaque,
// including number lexemes and keys that resemble protocol fields.
type Event struct {
	Role  string  `json:"role,omitempty"`
	Parts []*Part `json:"parts,omitempty"`
}

type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

type FunctionResponse struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Response     json.RawMessage `json:"response,omitempty"`
	WillContinue *bool           `json:"willContinue,omitempty"`
	Scheduling   string          `json:"scheduling,omitempty"`
	Parts        []*ResponsePart `json:"parts,omitempty"`
}

type ResponsePart struct {
	InlineData *Blob `json:"inlineData,omitempty"`
	FileData   *File `json:"fileData,omitempty"`
}

type Blob struct {
	MIMEType    string `json:"mimeType,omitempty"`
	Data        []byte `json:"data,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

type File struct {
	FileURI     string `json:"fileUri,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}
