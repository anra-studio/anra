package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func (s *Service) allowsOrigin(r *http.Request) bool {
	if len(s.config.TrustedOrigins) == 0 {
		return true
	}
	origins := r.Header.Values("Origin")
	return len(origins) == 0 || (len(origins) == 1 && slices.Contains(s.config.TrustedOrigins, origins[0]))
}

// The SDK sets its transport caching headers during response delivery. Enforce
// no-store at that boundary so authenticated context is never cached.
type noStoreWriter struct{ http.ResponseWriter }

func (w noStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w noStoreWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}
func (w noStoreWriter) Write(data []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(data)
}

// Handler exposes exactly two read-only tools using the official SDK's
// Streamable HTTP transport. Stateless requests cannot inherit another
// connection's identity through a guessed or replayed MCP session ID.
// Mount on a controlled listener; remote deployments must terminate TLS.
func (s *Service) Handler() http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: "anra-context", Version: "1.0.0"}, nil)
	for _, name := range []string{ReadRole, ReadContextWindow} {
		description := "Read the immutable role identity and definition revision bound to the authorized handoff."
		if name == ReadContextWindow {
			description = "Read the fixed TapeView and complete projected context bound to the authorized handoff."
		}
		server.AddTool(&sdk.Tool{
			Name:        name,
			Description: description,
			InputSchema: map[string]any{
				"type": "object", "required": []string{"anchorSeq"}, "additionalProperties": false,
				"properties": map[string]any{"anchorSeq": map[string]any{"type": "string", "pattern": "^[1-9][0-9]*$"}},
			},
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)},
		}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
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
		})
	}
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	authenticated := s.authenticateRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		transport.ServeHTTP(noStoreWriter{w}, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Apply to every method before transport dispatch or authentication.
		// SDK v1.8.0 does not enable Origin checking by default.
		if !s.allowsOrigin(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(errorEnvelope(ErrPermissionDenied))
			return
		}
		authenticated.ServeHTTP(w, r)
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
