package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"uuid"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const journalKind = "anra:tape-view-grant/v1"

// Selection is the lender's permission decision, made before reading/exporting
// entries. One best permitted anchor range may be selected in v1.
type Selection struct {
	Status pb.DecisionStatus
	Range  *tv.EntryRange
	TTL    time.Duration
}
type Policy interface {
	Select(context.Context, string, *pb.BorrowRequest, te.Seq) (Selection, error)
}
type PolicyFunc func(context.Context, string, *pb.BorrowRequest, te.Seq) (Selection, error)

func (f PolicyFunc) Select(c context.Context, p string, r *pb.BorrowRequest, h te.Seq) (Selection, error) {
	return f(c, p, r, h)
}

// Exporter defines an explicitly allowed representation. It must be deterministic
// and must not export arbitrary CustomEntry.Extensions. Its Name is persisted.
type Exporter interface {
	Name() string
	Export(te.EntryLike) (*pb.ExportedEntry, error)
}
type TextExporter struct{}

func (TextExporter) Name() string { return "anra:text-summary/v1" }
func (TextExporter) Export(e te.EntryLike) (*pb.ExportedEntry, error) {
	switch e.GetKind() {
	case te.EntryUser, te.EntryAssistant:
	default:
		return nil, ErrUnsupported
	}
	data, err := json.Marshal(e.GetSummary())
	if err != nil {
		return nil, err
	}
	return &pb.ExportedEntry{Seq: e.GetID().String(), Kind: string(e.GetKind()), Summary: e.GetSummary(), Timestamp: timestamppb.New(e.GetTimestamp()), Payload: &pb.Payload{MediaType: TextMediaType, JsonData: data}}, nil
}

// Config uses an initialized bbolt source tape and an open bbolt grant database.
// The caller owns their lifetime.
type Config struct {
	Source *pb.Source   `validate:"required,structonly"`
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
	Schema    string            `json:"schema"`
	Source    *pb.Source        `json:"source"`
	Requester string            `json:"requester"`
	Request   *pb.BorrowRequest `json:"request"`
	Decision  *pb.Decision      `json:"decision"`
	Session   string            `json:"session,omitempty"`
	HeadHash  string            `json:"headHash,omitempty"`
	EntryHash string            `json:"entryHash,omitempty"`
	Codec     string            `json:"codec,omitempty"`
	Closed    bool              `json:"closed,omitempty"`
}

func NewService(c Config) (*Service, error) {
	if err := validateStruct(c); err != nil {
		return nil, err
	}
	if err := validateStruct(storeSource(c.Source)); err != nil {
		return nil, err
	}
	c.Source = proto.Clone(c.Source).(*pb.Source)
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
func (s *Service) Borrow(ctx context.Context, p string, req *pb.BorrowRequest) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == "" {
		return nil, ErrUnavailable
	}
	if err := validateMessage(req); err != nil {
		return nil, err
	}
	r, ok, err := s.requestGrant(ctx, p, req.RequestId)
	if err != nil {
		return nil, err
	}
	if ok {
		if !proto.Equal(r.Request, req) {
			return nil, ErrConflict
		}
		return r.Decision, nil
	}
	h, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	head := h.Scope.SeqE
	sel, err := s.config.Policy.Select(ctx, p, proto.Clone(req).(*pb.BorrowRequest), head)
	if err != nil {
		return nil, err
	}
	r, err = s.decide(ctx, p, req, sel, h)
	if err != nil {
		return nil, err
	}
	if err := s.append(ctx, r, nil); err != nil {
		return nil, err
	}
	return r.Decision, nil
}
func (s *Service) decide(ctx context.Context, p string, req *pb.BorrowRequest, sel Selection, h tv.TapeView) (grantRecord, error) {
	head := h.Scope.SeqE
	r := grantRecord{Schema: journalKind, Source: proto.Clone(s.config.Source).(*pb.Source), Requester: p, Request: proto.Clone(req).(*pb.BorrowRequest), Decision: &pb.Decision{RequestId: req.RequestId, Status: sel.Status}}
	if sel.Status == Pending || sel.Status == Denied {
		if sel.Range != nil {
			return r, ErrInvalidParams
		}
		return r, nil
	}
	if sel.Status != Granted || sel.TTL <= 0 || sel.TTL > 24*time.Hour {
		return r, ErrInvalidParams
	}
	ranges := []*pb.EntryRange{}
	if sel.Range != nil {
		b := *sel.Range
		if !validRange(b, head) {
			return r, ErrInvalidParams
		}
		if req.Bounds != nil {
			bounds, err := rangeFromProto(req.Bounds)
			if err != nil || b.SeqS.Cmp(bounds.SeqS) < 0 || b.SeqE.Cmp(bounds.SeqE) > 0 {
				return r, ErrInvalidParams
			}
		}
		ranges = append(ranges, rangeToProto(b))
	}
	r.Decision.View = &pb.View{ViewId: uuid.New().String(), Source: proto.Clone(r.Source).(*pb.Source), SnapshotHead: head.String(), Ranges: ranges, ExpiresAt: timestamppb.New(s.config.Now().UTC().Add(sel.TTL))}
	r.Session = h.SessionID
	r.Codec = s.config.Exporter.Name()
	headHash, err := s.headHash(ctx, head)
	if err != nil {
		return r, err
	}
	r.HeadHash = headHash
	_, entryHash, err := s.entries(ctx, r.Decision.View)
	if err != nil {
		return r, err
	}
	r.EntryHash = entryHash
	// Recheck metadata and head contents after selection/export, before committing.
	if err := s.checkSource(ctx, r); err != nil {
		return r, err
	}
	return r, nil
}
func (s *Service) headHash(ctx context.Context, head te.Seq) (string, error) {
	if head.IsZero() {
		return fingerprint([]byte(`"empty"`)), nil
	}
	v, err := s.config.Tape.Range(s.tapeCtx(ctx), tv.EntryRange{SeqS: head, SeqE: head.Next()})
	if err != nil {
		return "", storageError(err)
	}
	if len(v.Raw) != 1 || v.Raw[0].GetID() != head || v.Raw[0].GetOwner() != s.config.Owner {
		return "", ErrStorage
	}
	e := v.Raw[0]
	encoded, err := json.Marshal(struct {
		Seq  te.Seq
		Kind string
		Text string
		Time time.Time
	}{head, string(e.GetKind()), e.GetSummary(), e.GetTimestamp()})
	if err != nil {
		return "", storageError(err)
	}
	return fingerprint(encoded), nil
}
func (s *Service) entries(ctx context.Context, v *pb.View) ([]*pb.ExportedEntry, string, error) {
	out := []*pb.ExportedEntry{}
	digest := newEntryFingerprint()
	for _, bounds := range v.Ranges {
		b, err := rangeFromProto(bounds)
		if err != nil {
			return nil, "", err
		}
		raw, err := s.config.Tape.Range(s.tapeCtx(ctx), b)
		if err != nil {
			return nil, "", storageError(err)
		}
		expected := b.SeqS
		for _, e := range raw.Raw {
			if e == nil || expected.Cmp(b.SeqE) >= 0 || e.GetOwner() != s.config.Owner || e.GetID() != expected {
				return nil, "", ErrStorage
			}
			x, err := s.config.Exporter.Export(e)
			if err != nil {
				return nil, "", err
			}
			if x == nil || validateEntry(x) != nil || x.Seq != e.GetID().String() || x.Kind != string(e.GetKind()) || !x.Timestamp.AsTime().Equal(e.GetTimestamp()) {
				return nil, "", ErrUnsupported
			}
			stored, err := storeEntry(x)
			if err != nil {
				return nil, "", err
			}
			// Preserve the original tape timestamp representation in legacy fingerprints.
			stored.Timestamp = e.GetTimestamp()
			encoded, err := json.Marshal(stored)
			if err != nil || len(encoded) > MaxEntryBytes {
				return nil, "", ErrUnsupported
			}
			digest.add(encoded)
			out = append(out, x)
			expected = expected.Next()
		}
		if expected != b.SeqE {
			return nil, "", ErrStorage
		}
	}
	return out, digest.sum(), nil
}
func (s *Service) checkSource(ctx context.Context, r grantRecord) error {
	h, err := s.head(ctx)
	if err != nil {
		return err
	}
	head := h.Scope.SeqE
	snapshot, err := te.ParseSeq(r.Decision.View.SnapshotHead)
	if err != nil {
		return ErrStorage
	}
	if h.SessionID != r.Session || head.Cmp(snapshot) < 0 || r.Codec != s.config.Exporter.Name() {
		return ErrUnavailable
	}
	hh, err := s.headHash(ctx, snapshot)
	if err != nil {
		return err
	}
	if hh != r.HeadHash {
		return ErrUnavailable
	}
	return nil
}
func (s *Service) Get(ctx context.Context, p, requestID string) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.requestGrant(ctx, p, requestID)
	if err != nil {
		return nil, err
	}
	if p == "" || !ok {
		return nil, ErrUnavailable
	}
	return r.Decision, nil
}

// Resolve is a local policy operation, deliberately absent from the peer RPC.
func (s *Service) Resolve(ctx context.Context, p, requestID string, sel Selection) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.requestGrant(ctx, p, requestID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnavailable
	}
	if r.Decision.Status != Pending {
		return nil, ErrConflict
	}
	h, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	previous := r
	r, err = s.decide(ctx, p, r.Request, sel, h)
	if err != nil {
		return nil, err
	}
	if err := s.append(ctx, r, &previous); err != nil {
		return nil, err
	}
	return r.Decision, nil
}

type cursor struct {
	ViewID string `json:"viewId"`
	Head   te.Seq `json:"head"`
	Offset int    `json:"offset"`
}

func (s *Service) Read(ctx context.Context, p string, req *pb.ReadRequest) (*pb.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateMessage(req); err != nil {
		return nil, err
	}
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = 32
	}
	r, ok, err := s.viewGrant(ctx, p, req.ViewId)
	if err != nil {
		return nil, err
	}
	if p == "" || !ok || r.Closed || !s.config.Now().Before(r.Decision.View.ExpiresAt.AsTime()) {
		return nil, ErrUnavailable
	}
	if err := s.checkSource(ctx, r); err != nil {
		return nil, err
	}
	entries, entryHash, err := s.entries(ctx, r.Decision.View)
	if err != nil {
		return nil, err
	}
	if entryHash != r.EntryHash {
		return nil, ErrUnavailable
	}
	offset := 0
	if req.Cursor != "" {
		if len(req.Cursor) > 1024 {
			return nil, ErrInvalidParams
		}
		b, err := base64.RawURLEncoding.DecodeString(req.Cursor)
		if err != nil {
			return nil, ErrInvalidParams
		}
		var c cursor
		if decode(b, &c) != nil || c.ViewID != req.ViewId || c.Head.String() != r.Decision.View.SnapshotHead || c.Offset < 1 || c.Offset >= len(entries) {
			return nil, ErrInvalidParams
		}
		offset = c.Offset
	}
	end := min(offset+int(pageSize), len(entries))
	page := &pb.Page{ViewId: req.ViewId, Source: proto.Clone(r.Source).(*pb.Source), SnapshotHead: r.Decision.View.SnapshotHead, Entries: entries[offset:end]}
	if end < len(entries) {
		b, _ := json.Marshal(cursor{req.ViewId, te.MustParseSeq(page.SnapshotHead), end})
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
