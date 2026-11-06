package mcp

import (
	"context"
	"fmt"
	"slices"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// TapeReader is the read-only portion of tape-go's storage.EntryStorage.
// The caller owns initialization, recovery, writes, and storage lifetime.
type TapeReader interface {
	Range(context.Context, tv.EntryRange, ...storage.RangeBy) (tv.EntryView, error)
}

type Config struct {
	Source *pb.Source    `validate:"required,structonly"`
	Owner  string        `validate:"required"`
	Tape   TapeReader    `validate:"required,structonly"`
	Roles  RoleProvider  `validate:"required,structonly"`
	Grants GrantVerifier `validate:"required,structonly"`
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
	if err := protovalidate.Validate(c.Source); err != nil {
		return nil, fmt.Errorf("mcp source: %w", err)
	}
	return &Service{config: c}, nil
}

func resolve(g *Grant, input toolInput, permission string) error {
	if input.AnchorSeq != g.AnchorSeq || !slices.Contains(g.Permissions, permission) {
		return ErrPermissionDenied
	}
	return nil
}

func (s *Service) tapeContext(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, s.config.Owner)
}
