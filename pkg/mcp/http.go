package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var toolDescriptions = map[string]string{
	ReadRole:     "Read the immutable role identity and definition revision bound to the authorized handoff.",
	ReadTapeView: "Read the entries of the fixed TapeView bound to the authorized handoff.",
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
	for _, name := range []string{ReadRole, ReadTapeView} {
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
			return s.toolResult(nil, ErrInvalidReference, 0), nil
		}
		if err := resolve(g, input, name); err != nil {
			return s.toolResult(nil, err, 0), nil
		}
		if name == ReadRole {
			result, err := s.role(ctx, g.Role)
			return s.toolResult(result, err, s.config.MaxResponseBytes), nil
		}
		result, err := s.readTapeView(ctx, g.View)
		return s.toolResult(result, err, s.config.MaxResponseBytes), nil
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

// toolResult encodes value once and shares it as text and structured content.
// A positive limit bounds the encoded result; an oversized result fails whole.
func (s *Service) toolResult(value any, err error, limit int) *sdk.CallToolResult {
	if err != nil {
		value = errorEnvelope(err)
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil || (limit > 0 && len(data) > limit) {
		return s.toolResult(nil, ErrFailedPrecondition, 0)
	}
	return &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(data)}},
		StructuredContent: json.RawMessage(data), IsError: err != nil,
	}
}
