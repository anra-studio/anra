package mcp

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// validOrigin accepts serialized HTTP(S) origins, not URLs with paths, user
// info, query, fragments, wildcards, or the opaque origin "null". Trust is a
// deployment decision; Host and proxy forwarding headers never confer it.
func validOrigin(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") &&
		u.Hostname() != "" && u.User == nil && u.Opaque == "" &&
		value == u.Scheme+"://"+u.Host && !strings.ContainsAny(u.Host, "*\\")
}

func (s *Service) allowsOrigin(r *http.Request) bool {
	if len(s.config.TrustedOrigins) == 0 {
		return true
	}
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	return len(values) == 1 && validOrigin(values[0]) && slices.Contains(s.config.TrustedOrigins, values[0])
}
