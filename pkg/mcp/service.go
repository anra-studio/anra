package mcp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"buf.build/go/protovalidate"
	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	"github.com/anra-studio/anra/pkg/model/anchor"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

const DefaultMaxResponseBytes = 1024 * 1024

type Config struct {
	Source    *pb.Source   `validate:"required,structonly"`
	Owner     string       `validate:"required"`
	Tape      *bbolt.Bbolt `validate:"required,structonly"`
	StatePath string       `validate:"required"`
	// TrustedOrigins is an exact allowlist of browser origins (scheme + host,
	// optional port). Empty disables Origin restrictions. When configured,
	// Origin must be a single valid allowlisted value if present. All clients,
	// including native clients without Origin, require Bearer authentication.
	TrustedOrigins []string `validate:"dive,trusted_origin"`
	// Zero uses 1 MiB. Limits apply to the entire MCP tool result, including its
	// structured and text representations. Oversized results fail atomically.
	MaxResponseBytes int              `validate:"omitempty,min=1024,max=16777216"`
	Now              func() time.Time `validate:"-"`
}

// Service owns its authorization database, but borrows the initialized Tape.
// Readers never acquire an Agent's reasoning admission or mutate the Tape.
type Service struct {
	config Config
	state  *stateStore
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
	// A source is the persisted Tape incarnation, not a caller-selected label.
	first, err := c.Tape.Range(owner.WithOwnerId(context.Background(), c.Owner), tv.EntryRange{SeqS: te.SeqFromUint64(1), SeqE: te.SeqFromUint64(2)})
	if err != nil {
		return nil, err
	}
	if len(first.Raw) != 1 || first.Raw[0].GetID() != te.SeqFromUint64(1) || first.Raw[0].GetOwner() != c.Owner {
		return nil, ErrInvalidReference
	}
	initialized, err := anchor.Decode(first.Raw[0])
	if err != nil || initialized == nil || initialized.Type != anchor.TapeInitialized || !proto.Equal(initialized.Source, c.Source) {
		return nil, ErrInvalidReference
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = DefaultMaxResponseBytes
	}
	s, err := openState(c.StatePath, c.Source)
	if err != nil {
		return nil, err
	}
	return &Service{config: c, state: s}, nil
}

// Close must follow HTTP server shutdown; it does not close the borrowed Tape.
func (s *Service) Close() error { return s.state.db.Close() }

// SaveRole publishes an immutable definition revision through the local control
// plane. Reusing a version for different contents fails.
func (s *Service) SaveRole(ctx context.Context, r RoleDefinition) error {
	if validate.Struct(r) != nil || r.ID != s.config.Source.AgentId {
		return ErrInvalidReference
	}
	if len(r.Definition) > s.config.MaxResponseBytes/2 {
		return ErrFailedPrecondition
	}
	return s.state.insert(ctx, rolesBucket, roleKey(r.RoleRef), r)
}

func (s *Service) role(ctx context.Context, ref RoleRef) (*RoleDefinition, error) {
	var r RoleDefinition
	err := s.state.load(ctx, rolesBucket, roleKey(ref), &r)
	if errors.Is(err, errMissing) {
		return nil, ErrInvalidReference
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	if validate.Struct(r) != nil || r.RoleRef != ref {
		return nil, ErrInvalidReference
	}
	return &r, nil
}

// CommitHandoff appends an integration anchor to Tape, the sole source of truth
// for handoffs and their fixed windows. The explicit view must already be fully
// persisted, including its excluded boundary. A prefix of an active turn is
// allowed: the prefix is fixed and never follows the live head. No authorization
// state is written until the controller separately calls Authorize.
func (s *Service) CommitHandoff(ctx context.Context, h Handoff) (string, error) {
	if err := s.validateHandoff(h); err != nil {
		return "", err
	}
	if _, err := s.role(ctx, h.Role); err != nil {
		return "", err
	}
	if err := s.walkWindow(ctx, h.Context.View, nil); err != nil {
		return "", err
	}
	data, err := json.Marshal(h)
	if err != nil {
		return "", ErrInvalidReference
	}
	if len(data) > s.config.MaxResponseBytes {
		return "", ErrFailedPrecondition
	}
	raw, err := s.config.Tape.Store(s.tapeContext(ctx), te.NewAnchor(te.Seq{}, s.config.Owner, te.AnchorKindCustom, data))
	if err != nil {
		return "", ErrUnavailable
	}
	if te.MustParseSeq(h.Context.View.SnapshotHead).Cmp(raw.GetID()) >= 0 {
		return "", ErrFailedPrecondition
	}
	return raw.GetID().String(), nil
}

func (s *Service) validateHandoff(h Handoff) error {
	if validate.Struct(h) != nil || validateWindow(h.Context) != nil || h.Role.ID != s.config.Source.AgentId || !proto.Equal(h.Context.View.Source, s.config.Source) {
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

// Authorize issues a new high-entropy credential only for an exactly matched,
// committed handoff. Deliver it through runtime configuration, never the Tape,
// a prompt, or an artifact. Only its SHA-256 digest is retained on disk.
func (s *Service) Authorize(ctx context.Context, g Grant) (string, error) {
	if validate.Struct(g) != nil || validateWindow(g.Window) != nil || !g.ExpiresAt.After(s.config.Now()) {
		return "", ErrPermissionDenied
	}
	h, err := s.handoff(ctx, g.AnchorSeq)
	if err != nil {
		return "", err
	}
	if !matches(g, h) {
		return "", ErrPermissionDenied
	}
	if _, err := s.role(ctx, h.Role); err != nil {
		return "", err
	}
	token := rand.Text()
	if err := s.state.insert(ctx, grantsBucket, tokenHash(token), storedGrant{Grant: g}); err != nil {
		return "", ErrUnavailable
	}
	return token, nil
}

func matches(g Grant, h *Handoff) bool {
	return g.RoleID == h.Role.ID && g.BindingID == h.Target.BindingID && proto.Equal(g.Window.View, h.Context.View)
}

// Revoke is idempotent and durable across server restarts.
func (s *Service) Revoke(ctx context.Context, token string) error { return s.state.revoke(ctx, token) }

func (s *Service) authenticate(ctx context.Context, token string) (*Grant, error) {
	if token == "" || len(token) > 512 {
		return nil, ErrPermissionDenied
	}
	var stored storedGrant
	if err := s.state.load(ctx, grantsBucket, tokenHash(token), &stored); err != nil {
		if errors.Is(err, errMissing) {
			return nil, ErrPermissionDenied
		}
		return nil, ErrUnavailable
	}
	g := &stored.Grant
	if stored.Revoked || validate.Struct(g) != nil || validateWindow(g.Window) != nil || !s.config.Now().Before(g.ExpiresAt) {
		return nil, ErrPermissionDenied
	}
	return g, nil
}

func (s *Service) resolve(ctx context.Context, g *Grant, input toolInput, permission string) (*Handoff, error) {
	if g == nil || input.AnchorSeq != g.AnchorSeq || !slices.Contains(g.Permissions, permission) {
		return nil, ErrPermissionDenied
	}
	if validate.Struct(input) != nil {
		return nil, ErrInvalidReference
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
