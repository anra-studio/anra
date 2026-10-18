package a2a

import (
	"strings"

	"github.com/samber/lo"
)

// headers is satisfied by both http.Header and *connect.Header.
type headers interface {
	Values(key string) []string
}

// extensionActive reports whether the A2A-Extensions header lists ExtensionURI.
func extensionActive(h headers) bool {
	return lo.SomeBy(h.Values(ExtensionHeader), func(v string) bool {
		return lo.ContainsBy(strings.Split(v, ","), func(uri string) bool {
			return strings.TrimSpace(uri) == ExtensionURI
		})
	})
}
