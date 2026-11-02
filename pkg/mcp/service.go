package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	TrustedOrigins []string `validate:"dive,http_url,excludes=*"`
	// Zero uses 1 MiB. Limits apply to the entire MCP tool result, including its
	// structured and text representations. Oversized results fail atomically.
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
	// Validate the serialized origin form once, before serving requests.
	for _, origin := range c.TrustedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" || origin != u.Scheme+"://"+u.Host {
			return nil, fmt.Errorf("mcp config: trusted origin %q must contain only scheme and host", origin)
		}
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
	if r == nil || validate.Struct(r) != nil || r.RoleRef != ref {
		return nil, ErrInvalidReference
	}
	if len(r.Definition) > s.config.MaxResponseBytes/2 {
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
	n, err := te.ParseSeq(seq)
	if err != nil || n.IsZero() {
		return nil, ErrInvalidReference
	}
	entries, err := s.config.Tape.Range(s.tapeContext(ctx), tv.EntryRange{SeqS: n, SeqE: n.Next()})
	if err != nil {
		return nil, tapeReadError(err, ErrInvalidReference)
	}
	if len(entries.Raw) != 1 || entries.Raw[0].GetID() != n || entries.Raw[0].GetKind() != anchor.Kind {
		return nil, ErrInvalidReference
	}
	raw := entries.Raw[0]
	if len(raw.GetSummary()) > s.config.MaxResponseBytes {
		return nil, ErrFailedPrecondition
	}
	var h Handoff
	if raw.GetOwner() != s.config.Owner || decode([]byte(raw.GetSummary()), &h) != nil || s.validateHandoff(h) != nil {
		return nil, ErrInvalidReference
	}
	if te.MustParseSeq(h.Context.View.SnapshotHead).Cmp(n) >= 0 {
		return nil, ErrInvalidReference
	}
	return &h, nil
}

func tapeReadError(err error, corrupt *Error) error {
	var syntax *json.SyntaxError
	var shape *json.UnmarshalTypeError
	var timestamp *time.ParseError
	if errors.As(err, &syntax) || errors.As(err, &shape) || errors.As(err, &timestamp) || errors.Is(err, te.ErrInvalidSeq) {
		return corrupt
	}
	return ErrUnavailable
}

func matches(g Grant, h *Handoff) bool {
	return g.RoleID == h.Role.ID && g.BindingID == h.Target.BindingID && proto.Equal(g.Window.View, h.Context.View)
}

func (s *Service) authenticate(ctx context.Context, token string) (*Grant, error) {
	if token == "" || len(token) > 512 {
		return nil, ErrPermissionDenied
	}
	g, err := s.config.Grants.VerifyGrant(ctx, token)
	if errors.Is(err, ErrPermissionDenied) {
		return nil, ErrPermissionDenied
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if g == nil || validate.Struct(g) != nil || protovalidate.Validate(g.Window.View) != nil || !s.config.Now().Before(g.ExpiresAt) || g.RoleID != s.config.Source.AgentId || !proto.Equal(g.Window.View.Source, s.config.Source) {
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
