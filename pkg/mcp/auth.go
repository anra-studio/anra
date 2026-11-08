package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type credentialContextKey struct{}

func bearerToken(header http.Header) string {
	values := header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	scheme, value, found := strings.Cut(values[0], " ")
	if !found {
		return ""
	}
	if !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

func (s *Service) authenticate(ctx context.Context, token string) *Error {
	if token == "" {
		return ErrPermissionDenied
	}
	return accessError(s.config.Access.Authenticate(ctx, token))
}

func accessError(err error) *Error {
	if errors.Is(err, ErrPermissionDenied) {
		return ErrPermissionDenied
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) authenticateRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header)
		err := s.authenticate(r.Context(), token)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			status := http.StatusUnauthorized
			if errors.Is(err, ErrUnavailable) {
				status = http.StatusServiceUnavailable
			} else {
				w.Header().Set("WWW-Authenticate", "Bearer")
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(ErrorResponse{Error: err})
			return
		}
		ctx := context.WithValue(r.Context(), credentialContextKey{}, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
