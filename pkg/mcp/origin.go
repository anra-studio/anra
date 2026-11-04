package mcp

import (
	"encoding/json"
	"net/http"
	"slices"
)

// restrictOrigin rejects requests whose Origin is not in TrustedOrigins.
func (s *Service) restrictOrigin(next http.Handler) http.Handler {
	if len(s.config.TrustedOrigins) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.trustsOrigin(r.Header.Values("Origin")) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(errorEnvelope(ErrPermissionDenied))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Service) trustsOrigin(origins []string) bool {
	switch len(origins) {
	case 0:
		return true
	case 1:
		return slices.Contains(s.config.TrustedOrigins, origins[0])
	default:
		return false
	}
}
