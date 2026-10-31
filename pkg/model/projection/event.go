package projection

import "encoding/json"

// Event is the version 1 public model event projected from stored ADK content.
// Its protocol structures are explicit: upgrading an SDK must not silently
// expand the projection's supported fields. Business JSON is opaque,
// including number lexemes and keys that resemble protocol fields.
type Event struct {
	Role  string  `json:"role,omitempty"`
	Parts []*Part `json:"parts,omitempty" validate:"dive,required"`
}

type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *FunctionResponse `json:"functionResponse,omitempty"`
}

type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty" validate:"required"`
	Args json.RawMessage `json:"args,omitempty" validate:"omitempty,jsonobject"`
}

type FunctionResponse struct {
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty" validate:"required"`
	Response     json.RawMessage `json:"response,omitempty" validate:"omitempty,jsonobject"`
	WillContinue *bool           `json:"willContinue,omitempty"`
	Scheduling   string          `json:"scheduling,omitempty" validate:"omitempty,oneof=SCHEDULING_UNSPECIFIED SILENT WHEN_IDLE INTERRUPT"`
	Parts        []*ResponsePart `json:"parts,omitempty" validate:"dive,required"`
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
