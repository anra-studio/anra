package a2a

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
)

const journalKind = "anra:tape-view-grant/v1"

// Selection is the lender's permission decision, made before reading/exporting
// entries. One best permitted anchor range may be selected in v1.
type Selection struct {
	Status Status
	Range  *tv.EntryRange
	TTL    time.Duration
}
type Policy interface {
	Select(context.Context, string, BorrowRequest, te.Seq) (Selection, error)
}
type PolicyFunc func(context.Context, string, BorrowRequest, te.Seq) (Selection, error)

func (f PolicyFunc) Select(c context.Context, p string, r BorrowRequest, h te.Seq) (Selection, error) {
	return f(c, p, r, h)
}

// Exporter defines an explicitly allowed representation. It must be deterministic
// and must not export arbitrary CustomEntry.Extensions. Its Name is persisted.
type Exporter interface {
	Name() string
	Export(te.EntryLike) (ExportedEntry, error)
}
type TextExporter struct{}

func (TextExporter) Name() string { return "anra:text-summary/v1" }
func (TextExporter) Export(e te.EntryLike) (ExportedEntry, error) {
	switch e.GetKind() {
	case te.EntryUser, te.EntryAssistant:
	default:
		return ExportedEntry{}, ErrUnsupported
	}
	data, err := json.Marshal(e.GetSummary())
	if err != nil {
		return ExportedEntry{}, err
	}
	return ExportedEntry{ProjectedEntry: tv.ProjectedEntry{Seq: e.GetID(), Kind: e.GetKind(), Summary: e.GetSummary()}, Timestamp: e.GetTimestamp(), Payload: &Payload{MediaType: TextMediaType, Data: data}}, nil
}

// Config uses an initialized bbolt source tape and an open bbolt grant database.
// The caller owns their lifetime.
type Config struct {
	Source Source       `validate:"required"`
	Owner  string       `validate:"required"`
	Tape   *bbolt.Bbolt `validate:"required,structonly"`
	// Journal holds indexed grant state and audit records, separate from the source.
	Journal      *bolt.DB         `validate:"required,structonly"`
	JournalOwner string           `validate:"required,nefield=Owner"`
	Policy       Policy           `validate:"required,structonly"`
	Exporter     Exporter         `validate:"-"`
	Now          func() time.Time `validate:"-"`
}

// Service queries durable grant indexes on demand; no journal entries or grant
// projection are retained in memory. mu serializes local policy operations.
type Service struct {
	mu     sync.Mutex
	config Config
}

type grantRecord struct {
	Schema    string        `json:"schema"`
	Source    Source        `json:"source"`
	Requester string        `json:"requester"`
	Request   BorrowRequest `json:"request"`
	Decision  Decision      `json:"decision"`
	Session   string        `json:"session,omitempty"`
	HeadHash  string        `json:"headHash,omitempty"`
	EntryHash string        `json:"entryHash,omitempty"`
	Codec     string        `json:"codec,omitempty"`
	Closed    bool          `json:"closed,omitempty"`
}

func NewService(c Config) (*Service, error) {
	if err := validateStruct(c); err != nil {
		return nil, err
	}
	if c.Exporter == nil {
		c.Exporter = TextExporter{}
	}
	if c.Exporter.Name() == "" {
		return nil, ErrInvalidParams
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c}, nil
}
func (s *Service) tapeCtx(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, s.config.Owner)
}
func storageError(err error) error { return fmt.Errorf("%w: %v", ErrStorage, err) }
func (s *Service) head(ctx context.Context) (tv.TapeView, error) {
	v, err := s.config.Tape.Get(s.tapeCtx(ctx))
	if err != nil {
		return v, storageError(err)
	}
	if v.Owner != s.config.Owner {
		return v, ErrStorage
	}
	return v, nil
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func id() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (s *Service) Borrow(ctx context.Context, p string, req BorrowRequest) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == "" {
		return Decision{}, ErrUnavailable
	}
	if err := req.validate(); err != nil {
		return Decision{}, err
	}
	r, ok, err := s.requestGrant(ctx, p, req.RequestID)
	if err != nil {
		return Decision{}, err
	}
	if ok {
		if hash(r.Request) != hash(req) {
			return Decision{}, ErrConflict
		}
		return r.Decision, nil
	}
	h, err := s.head(ctx)
	if err != nil {
		return Decision{}, err
	}
	head := h.Scope.SeqE
	sel, err := s.config.Policy.Select(ctx, p, req, head)
	if err != nil {
		return Decision{}, err
	}
	r, err = s.decide(ctx, p, req, sel, h)
	if err != nil {
		return Decision{}, err
	}
	if err := s.append(ctx, r, nil); err != nil {
		return Decision{}, err
	}
	return r.Decision, nil
}
func (s *Service) decide(ctx context.Context, p string, req BorrowRequest, sel Selection, h tv.TapeView) (grantRecord, error) {
	head := h.Scope.SeqE
	r := grantRecord{Schema: journalKind, Source: s.config.Source, Requester: p, Request: req, Decision: Decision{RequestID: req.RequestID, Status: sel.Status}}
	if sel.Status == Pending || sel.Status == Denied {
		if sel.Range != nil {
			return r, ErrInvalidParams
		}
		return r, nil
	}
	if sel.Status != Granted || sel.TTL <= 0 || sel.TTL > 24*time.Hour {
		return r, ErrInvalidParams
	}
	ranges := []tv.EntryRange{}
	if sel.Range != nil {
		b := *sel.Range
		if !validRange(b, head) {
			return r, ErrInvalidParams
		}
		if req.Bounds != nil && (b.SeqS.Cmp(req.Bounds.SeqS) < 0 || b.SeqE.Cmp(req.Bounds.SeqE) > 0) {
			return r, ErrInvalidParams
		}
		ranges = append(ranges, b)
	}
	vid, err := id()
	if err != nil {
		return r, err
	}
	r.Decision.View = &View{ID: vid, Source: r.Source, SnapshotHead: head, Ranges: ranges, ExpiresAt: s.config.Now().UTC().Add(sel.TTL)}
	r.Session = h.SessionID
	r.Codec = s.config.Exporter.Name()
	r.HeadHash, err = s.headHash(ctx, head)
	if err != nil {
		return r, err
	}
	entries, err := s.entries(ctx, r.Decision.View)
	if err != nil {
		return r, err
	}
	r.EntryHash = hash(entries)
	// Recheck metadata and head contents after selection/export, before committing.
	if err := s.checkSource(ctx, r); err != nil {
		return r, err
	}
	return r, nil
}
func (s *Service) headHash(ctx context.Context, head te.Seq) (string, error) {
	if head.IsZero() {
		return hash("empty"), nil
	}
	v, err := s.config.Tape.Range(s.tapeCtx(ctx), tv.EntryRange{SeqS: head, SeqE: head.Next()})
	if err != nil {
		return "", storageError(err)
	}
	if len(v.Raw) != 1 || v.Raw[0].GetID() != head || v.Raw[0].GetOwner() != s.config.Owner {
		return "", ErrStorage
	}
	e := v.Raw[0]
	return hash(struct {
		Seq  te.Seq
		Kind string
		Text string
		Time time.Time
	}{head, string(e.GetKind()), e.GetSummary(), e.GetTimestamp()}), nil
}
func (s *Service) entries(ctx context.Context, v *View) ([]ExportedEntry, error) {
	out := []ExportedEntry{}
	for _, b := range v.Ranges {
		raw, err := s.config.Tape.Range(s.tapeCtx(ctx), b)
		if err != nil {
			return nil, storageError(err)
		}
		expected := b.SeqS
		for _, e := range raw.Raw {
			if e == nil || expected.Cmp(b.SeqE) >= 0 || e.GetOwner() != s.config.Owner || e.GetID() != expected {
				return nil, ErrStorage
			}
			x, err := s.config.Exporter.Export(e)
			if err != nil {
				return nil, err
			}
			if x.Seq != e.GetID() || x.Kind != e.GetKind() || !x.Timestamp.Equal(e.GetTimestamp()) || (x.Payload != nil && (x.Payload.MediaType == "" || !json.Valid(x.Payload.Data))) {
				return nil, ErrUnsupported
			}
			encoded, err := json.Marshal(x)
			if err != nil || len(encoded) > MaxEntryBytes {
				return nil, ErrUnsupported
			}
			out = append(out, x)
			expected = expected.Next()
		}
		if expected != b.SeqE {
			return nil, ErrStorage
		}
	}
	return out, nil
}
func (s *Service) checkSource(ctx context.Context, r grantRecord) error {
	h, err := s.head(ctx)
	if err != nil {
		return err
	}
	head := h.Scope.SeqE
	if h.SessionID != r.Session || head.Cmp(r.Decision.View.SnapshotHead) < 0 || r.Codec != s.config.Exporter.Name() {
		return ErrUnavailable
	}
	hh, err := s.headHash(ctx, r.Decision.View.SnapshotHead)
	if err != nil {
		return err
	}
	if hh != r.HeadHash {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) Get(ctx context.Context, p, requestID string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.requestGrant(ctx, p, requestID)
	if err != nil {
		return Decision{}, err
	}
	if p == "" || !ok {
		return Decision{}, ErrUnavailable
	}
	return r.Decision, nil
}

// Resolve is a local policy operation, deliberately absent from the peer RPC.
func (s *Service) Resolve(ctx context.Context, p, requestID string, sel Selection) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.requestGrant(ctx, p, requestID)
	if err != nil {
		return Decision{}, err
	}
	if !ok {
		return Decision{}, ErrUnavailable
	}
	if r.Decision.Status != Pending {
		return Decision{}, ErrConflict
	}
	h, err := s.head(ctx)
	if err != nil {
		return Decision{}, err
	}
	previous := r
	r, err = s.decide(ctx, p, r.Request, sel, h)
	if err != nil {
		return Decision{}, err
	}
	if err := s.append(ctx, r, &previous); err != nil {
		return Decision{}, err
	}
	return r.Decision, nil
}

type cursor struct {
	ViewID string `json:"viewId"`
	Head   te.Seq `json:"head"`
	Offset int    `json:"offset"`
}

func (s *Service) Read(ctx context.Context, p string, req ReadRequest) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := req.validate(); err != nil {
		return Page{}, err
	}
	if req.PageSize == 0 {
		req.PageSize = 32
	}
	r, ok, err := s.viewGrant(ctx, p, req.ViewID)
	if err != nil {
		return Page{}, err
	}
	if p == "" || !ok || r.Closed || !s.config.Now().Before(r.Decision.View.ExpiresAt) {
		return Page{}, ErrUnavailable
	}
	if err := s.checkSource(ctx, r); err != nil {
		return Page{}, err
	}
	entries, err := s.entries(ctx, r.Decision.View)
	if err != nil {
		return Page{}, err
	}
	if hash(entries) != r.EntryHash {
		return Page{}, ErrUnavailable
	}
	offset := 0
	if req.Cursor != "" {
		if len(req.Cursor) > 1024 {
			return Page{}, ErrInvalidParams
		}
		b, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil {
			return Page{}, ErrInvalidParams
		}
		var c cursor
		if decode(b, &c) != nil || c.ViewID != req.ViewID || c.Head != r.Decision.View.SnapshotHead || c.Offset < 1 || c.Offset >= len(entries) {
			return Page{}, ErrInvalidParams
		}
		offset = c.Offset
	}
	end := min(offset+req.PageSize, len(entries))
	page := Page{ViewID: req.ViewID, Source: r.Source, SnapshotHead: r.Decision.View.SnapshotHead, Entries: entries[offset:end]}
	if end < len(entries) {
		b, _ := json.Marshal(cursor{req.ViewID, page.SnapshotHead, end})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

// Release and Revoke persist the same terminal grant closure; only Release is
// available to the authenticated borrower over RPC.
func (s *Service) Release(ctx context.Context, p, vid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.viewGrant(ctx, p, vid)
	if err != nil {
		return err
	}
	if p == "" || !ok {
		return ErrUnavailable
	}
	if r.Closed {
		return nil
	}
	previous := r
	r.Closed = true
	return s.append(ctx, r, &previous)
}
func (s *Service) Revoke(ctx context.Context, p, vid string) error { return s.Release(ctx, p, vid) }
