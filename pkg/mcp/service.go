package mcp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

const DefaultMaxResponseBytes = 1024 * 1024

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
	// Zero uses 1 MiB. Bounds role definitions, committed handoffs, and projected
	// window entries, so an oversized window fails atomically instead of being
	// truncated. A tool result repeats that projection as both text and
	// structured content, so its serialized size is not checked.
	MaxResponseBytes int              `validate:"omitempty,min=1024,max=16777216"`
	Now              func() time.Time `validate:"-"`
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
	c.Source = proto.Clone(c.Source).(*pb.Source)
	c.TrustedOrigins = slices.Clone(c.TrustedOrigins)
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = DefaultMaxResponseBytes
	}
	return &Service{config: c}, nil
}

func (s *Service) role(ctx context.Context, ref RoleRef) (*RoleDefinition, error) {
	r, err := s.config.Roles.GetRole(ctx, ref)
	if err != nil {
		return nil, ErrUnavailable
	}
	if r == nil {
		return nil, ErrInvalidReference
	}
	if len(r.Definition) > s.config.MaxResponseBytes {
		return nil, ErrFailedPrecondition
	}
	return r, nil
}

func (s *Service) validateHandoff(h Handoff) error {
	if validate.Struct(h) != nil || protovalidate.Validate(h.Context.View) != nil || h.Role.ID != s.config.Source.AgentId || !proto.Equal(h.Context.View.Source, s.config.Source) {
		return ErrInvalidReference
	}
	return nil
}

func (s *Service) tapeContext(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, s.config.Owner)
}

func (s *Service) handoff(ctx context.Context, seq string) (*Handoff, error) {
	// resolve supplies the sequence from validated tool input and grant.
	n := te.MustParseSeq(seq)
	entries, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{SeqS: n, SeqE: n.Next()})
	if err != nil {
		return nil, ErrUnavailable
	}
	if len(entries.Raw) != 1 || entries.Raw[0].GetID() != n || entries.Raw[0].GetKind() != anchor.Kind {
		return nil, ErrInvalidReference
	}
	raw := entries.Raw[0]
	if len(raw.GetSummary()) > s.config.MaxResponseBytes {
		return nil, ErrFailedPrecondition
	}
	var h Handoff
	if decode([]byte(raw.GetSummary()), &h) != nil || s.validateHandoff(h) != nil {
		return nil, ErrInvalidReference
	}
	if raw.GetOwner() != s.config.Owner || te.MustParseSeq(h.Context.View.SnapshotHead).Cmp(n) >= 0 {
		return nil, ErrInvalidReference
	}
	return &h, nil
}

func matches(g Grant, h *Handoff) bool {
	return g.RoleID == h.Role.ID && g.BindingID == h.Target.BindingID && proto.Equal(g.Window.View, h.Context.View)
}

func (s *Service) authenticate(ctx context.Context, token string) (*Grant, error) {
	if token == "" {
		return nil, ErrPermissionDenied
	}
	g, err := s.config.Grants.VerifyGrant(ctx, token)
	if errors.Is(err, ErrPermissionDenied) {
		return nil, ErrPermissionDenied
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if validate.Struct(g) != nil || protovalidate.Validate(g.Window.View) != nil {
		return nil, ErrPermissionDenied
	}
	if g.ExpiresAt.Compare(s.config.Now()) <= 0 || g.RoleID != s.config.Source.AgentId || !proto.Equal(g.Window.View.Source, s.config.Source) {
		return nil, ErrPermissionDenied
	}
	return g, nil
}

func (s *Service) resolve(ctx context.Context, g *Grant, input toolInput, permission string) (*Handoff, error) {
	if g == nil || input.AnchorSeq != g.AnchorSeq || !slices.Contains(g.Permissions, permission) {
		return nil, ErrPermissionDenied
	}
	h, err := s.handoff(ctx, input.AnchorSeq)
	if err != nil {
		return nil, err
	}
	if !matches(*g, h) {
		return nil, ErrPermissionDenied
	}
	return h, nil
}
