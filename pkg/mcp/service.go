package mcp

import (
	"fmt"
	"slices"

	"github.com/anra-studio/anra/pkg/model/view"
)

type Config struct {
	Roles RoleProvider `validate:"required,structonly"`

	Grants GrantVerifier `validate:"required,structonly"`

	Views *view.Service `validate:"required,structonly"`

	// TrustedOrigins is an exact allowlist of browser origins (scheme + host,
	// optional port). Empty disables Origin restrictions. When configured,
	// Origin must be a single allowlisted value if present. All clients,
	// including native clients without Origin, require Bearer authentication.
	// Entries are compared byte-for-byte without normalization. Configure the
	// serialized scheme, host, and port sent by the browser.
	TrustedOrigins []string `validate:"dive,http_url,excludes=*"`
}

// Service reads caller-owned Tape, role definitions, and authorization.
// It creates no files, owns no storage, and never mutates its dependencies.
type Service struct {
	config Config
}

func NewService(c Config) (*Service, error) {
	if err := validate.Struct(c); err != nil {
		return nil, fmt.Errorf("mcp config: %w", err)
	}

	return &Service{
		config: c,
	}, nil
}

func resolve(g *Grant, input toolInput, permission string) error {
	if input.AnchorSeq != g.AnchorSeq || !slices.Contains(g.Permissions, permission) {
		return ErrPermissionDenied
	}
	return nil
}
