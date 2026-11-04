package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var toolDescriptions = map[string]string{
	ReadRole:          "Read the immutable role identity and definition revision bound to the authorized handoff.",
	ReadContextWindow: "Read the fixed TapeView and complete projected context bound to the authorized handoff.",
}

// Handler exposes exactly two read-only tools using the official SDK's
// Streamable HTTP transport. Stateless requests cannot inherit another
// connection's identity through a guessed or replayed MCP session ID.
// Mount on a controlled listener; remote deployments must terminate TLS.
func (s *Service) Handler() http.Handler {
	server := s.newServer()
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return noStore(s.restrictOrigin(s.authenticateRequest(limitBody(transport))))
}

func (s *Service) newServer() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "anra-context", Version: "1.0.0"}, nil)
	for _, name := range []string{ReadRole, ReadContextWindow} {
		server.AddTool(&sdk.Tool{
			Name:        name,
			Description: toolDescriptions[name],
			InputSchema: map[string]any{
				"type": "object", "required": []string{"anchorSeq"}, "additionalProperties": false,
				"properties": map[string]any{"anchorSeq": map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$"}},
			},
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
		}, s.callTool(name))
	}
	return server
}

func (s *Service) callTool(name string) sdk.ToolHandler {
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		g, _ := ctx.Value(grantContextKey{}).(*Grant)
		var input toolInput
		if decode(req.Params.Arguments, &input) != nil || validate.Struct(input) != nil {
			return s.toolResult(nil, ErrInvalidReference), nil
		}
		h, err := s.resolve(ctx, g, input, name)
		if err != nil {
			return s.toolResult(nil, err), nil
		}
		var result any
		if name == ReadRole {
			result, err = s.role(ctx, h.Role)
		} else {
			result, err = s.window(ctx, h)
		}
		return s.toolResult(result, err), nil
	}
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		next.ServeHTTP(w, r)
	})
}

func errorEnvelope(err error) ErrorResponse {
	var public *Error
	if !errors.As(err, &public) {
		public = ErrUnavailable
	}
	return ErrorResponse{Error: public}
}

func (s *Service) toolResult(value any, err error) *sdk.CallToolResult {
	if err != nil {
		value = errorEnvelope(err)
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return s.toolResult(nil, ErrFailedPrecondition)
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(data)}},
		StructuredContent: json.RawMessage(data), IsError: err != nil,
	}
}
