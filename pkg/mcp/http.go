package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type grantContextKey struct{}

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
			h, committed, err := s.resolve(ctx, g, input, name)
			if err != nil {
				return s.toolResult(nil, err), nil
			}
			var result any
			if name == ReadRole {
				result, err = s.sealedRole(ctx, h.Role, committed)
			} else {
				result, err = s.window(ctx, h, committed)
			}
			return s.toolResult(result, err), nil
		})
	}
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
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
		fields := strings.Fields(r.Header.Get("Authorization"))
		var token string
		if len(r.Header.Values("Authorization")) == 1 && len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
			token = fields[1]
		}
		g, err := s.authenticate(r.Context(), token)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			status := http.StatusUnauthorized
			if errors.Is(err, ErrUnavailable) {
				status = http.StatusServiceUnavailable
			} else {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(errorEnvelope(err))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		transport.ServeHTTP(noStoreWriter{w}, r.WithContext(context.WithValue(r.Context(), grantContextKey{}, g)))
	})
}

func errorEnvelope(err error) any {
	var public *Error
	if !errors.As(err, &public) {
		public = ErrUnavailable
	}
	return struct {
		Error *Error `json:"error"`
	}{public}
}

func (s *Service) toolResult(value any, err error) *sdk.CallToolResult {
	if err != nil {
		value = errorEnvelope(err)
	}
	data, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return s.toolResult(nil, ErrFailedPrecondition)
	}
	result := &sdk.CallToolResult{
		Content:           []sdk.Content{&sdk.TextContent{Text: string(data)}},
		StructuredContent: json.RawMessage(data), IsError: err != nil,
	}
	wire, marshalErr := json.Marshal(result)
	// Reserve space for the SDK's JSON-RPC envelope. No partial result is sent.
	if marshalErr != nil || len(wire) > s.config.MaxResponseBytes-256 {
		return s.toolResult(nil, ErrFailedPrecondition)
	}
	return result
}
