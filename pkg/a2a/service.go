package a2a

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
	"uuid"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/scbizu/anra/pkg/model/journal"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
// and must not export arbitrary CustomEntry.Extensions. It is service
// configuration, not part of a grant: a grant references tape ranges, and each
// read projects them with the exporter configured at the time.
type Exporter interface {
	Export(te.EntryLike) (*pb.ExportedEntry, error)
}
type TextExporter struct{}

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

// Journal is the durable grant store a Service publishes to. *journal.Store is
// the bundled bbolt implementation; a store on a server database implements the
// same contract.
//
// An implementation must apply Decide and Release atomically, comparing previous
// inside that transaction, and must report a concurrent change as
// journal.ErrConflict and a storage failure as journal.ErrStorage, which a
// Service maps onto its public errors.
type Journal interface {
	// Source is the tape identity the store is bound to.
	Source() *pb.Source
	// RequestGrant loads the current outcome of one request.
	RequestGrant(ctx context.Context, requester, requestID string) (journal.State, bool, error)
	// ViewGrant loads the grant that issued viewID and whether requester holds it.
	ViewGrant(ctx context.Context, requester, viewID string) (journal.State, bool, error)
	// Decide records a request's outcome, or fails with ErrConflict.
	Decide(ctx context.Context, requester string, req *pb.BorrowRequest, d *pb.Decision, previous *journal.State) error
	// Release ends the grant held by st.
	Release(ctx context.Context, st journal.State) error
}

// The bbolt store in pkg/model/journal is the reference implementation.
var _ Journal = (*journal.Store)(nil)

// Config uses an initialized bbolt source tape and a grant journal. Configure
// the journal either as a path this service opens, or as an already open store
// it shares; the source tape and a shared store stay the caller's to close.
type Config struct {
	Source *pb.Source   `validate:"required,structonly"`
	Owner  string       `validate:"required"`
	Tape   *bbolt.Bbolt `validate:"required,structonly"`
	// JournalPath opens a journal database file and binds it to Source. The
	// service owns that file; Close releases it.
	JournalPath string `validate:"excluded_with=Journal"`
	// Journal is an open store bound to Source, shared with other services and
	// owned by the caller, which closes it.
	Journal  Journal          `validate:"required_without=JournalPath,excluded_with=JournalPath"`
	Views    Views            `validate:"-"`
	Policy   Policy           `validate:"required_without=Views,structonly"`
	Exporter Exporter         `validate:"-"`
	Now      func() time.Time `validate:"-"`
}

// Service queries durable grant indexes on demand; no journal entries or grant
// projection are retained in memory. mu serializes local policy operations.
type Service struct {
	mu     sync.Mutex
	config Config
	// closeJournal releases a journal database this service opened from a path.
	closeJournal func() error
}

func NewService(c Config) (*Service, error) {
	if err := validateStruct(c); err != nil {
		return nil, err
	}
	if err := validateMessage(c.Source); err != nil {
		return nil, err
	}
	store, closer, err := openJournal(c)
	if err != nil {
		return nil, err
	}
	c.Source = proto.Clone(c.Source).(*pb.Source)
	c.Journal = store
	if c.Exporter == nil {
		if c.Views != nil {
			c.Exporter = ContextExporter{}
		} else {
			c.Exporter = TextExporter{}
		}
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Service{config: c, closeJournal: closer}, nil
}

// openJournal resolves the grant journal: a supplied store is shared and stays
// the caller's, a path opens a file this service closes.
func openJournal(c Config) (Journal, func() error, error) {
	if c.Journal != nil {
		if !proto.Equal(c.Journal.Source(), c.Source) {
			return nil, nil, fmt.Errorf("%w: journal is bound to another source tape", ErrInvalidParams)
		}
		return c.Journal, nil, nil
	}
	store, err := journal.Open(c.JournalPath, c.Source)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

// Close releases the journal database this service opened from Config.JournalPath.
// A store supplied in Config.Journal is left open: the caller owns it.
func (s *Service) Close() error {
	if s.closeJournal == nil {
		return nil
	}
	return s.closeJournal()
}
func (s *Service) tapeCtx(ctx context.Context) context.Context {
	return owner.WithOwnerId(ctx, s.config.Owner)
}
func storageError(err error) error { return fmt.Errorf("%w: %v", ErrStorage, err) }

// journalFailure maps the journal's error contract onto this package's, so
// callers keep seeing only the public error categories.
func journalFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, journal.ErrConflict):
		return ErrConflict
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return storageError(err)
	}
}
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
	st, ok, err := s.config.Journal.RequestGrant(ctx, p, req.RequestId)
	if err != nil {
		return nil, journalFailure(err)
	}
	if ok {
		if !proto.Equal(st.Request, req) {
			return nil, ErrConflict
		}
		return st.Decision, nil
	}
	h, err := s.head(ctx)
	if err != nil {
		return nil, err
	}
	head := h.Scope.SeqE
	var sel Selection
	if s.config.Views != nil || req.View != nil {
		sel, head, err = s.viewSelection(ctx, p, req)
	} else {
		sel, err = s.config.Policy.Select(ctx, p, proto.Clone(req).(*pb.BorrowRequest), head)
	}
	if err != nil {
		return nil, err
	}
	d, err := s.decide(ctx, req, sel, head)
	if err != nil {
		return nil, err
	}
	if err := s.config.Journal.Decide(ctx, p, req, d, nil); err != nil {
		return nil, journalFailure(err)
	}
	return d, nil
}

// decide validates a policy selection against the tape and builds its decision.
func (s *Service) decide(ctx context.Context, req *pb.BorrowRequest, sel Selection, head te.Seq) (*pb.Decision, error) {
	d := &pb.Decision{RequestId: req.RequestId, Status: sel.Status}
	if sel.Status == Pending || sel.Status == Denied {
		if sel.Range != nil {
			return nil, ErrInvalidParams
		}
		return d, nil
	}
	if sel.Status != Granted || sel.TTL <= 0 || sel.TTL > 24*time.Hour {
		return nil, ErrInvalidParams
	}
	ranges := []*pb.EntryRange{}
	if sel.Range != nil {
		b := *sel.Range
		if !validRange(b, head) {
			return nil, ErrInvalidParams
		}
		if req.Bounds != nil {
			bounds, err := rangeFromProto(req.Bounds)
			if err != nil || b.SeqS.Cmp(bounds.SeqS) < 0 || b.SeqE.Cmp(bounds.SeqE) > 0 {
				return nil, ErrInvalidParams
			}
		}
		ranges = append(ranges, rangeToProto(b))
	}
	d.View = &pb.View{ViewId: uuid.New().String(), Source: proto.Clone(s.config.Source).(*pb.Source), SnapshotHead: head.String(), Ranges: ranges, ExpiresAt: timestamppb.New(s.config.Now().UTC().Add(sel.TTL))}
	// The selected range must exist and be exportable now; Read exports it again.
	if _, err := s.entries(ctx, d.View); err != nil {
		return nil, err
	}
	return d, nil
}
func (s *Service) entries(ctx context.Context, v *pb.View) ([]*pb.ExportedEntry, error) {
	out := []*pb.ExportedEntry{}
	for _, bounds := range v.Ranges {
		b, err := rangeFromProto(bounds)
		if err != nil {
			return nil, err
		}
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
			if x == nil || validateEntry(x) != nil || x.Seq != e.GetID().String() || x.Kind != string(e.GetKind()) || !x.Timestamp.AsTime().Equal(e.GetTimestamp()) {
				return nil, ErrUnsupported
			}
			stored, err := storeEntry(x)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(stored)
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
func (s *Service) Get(ctx context.Context, p, requestID string) (*pb.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.config.Journal.RequestGrant(ctx, p, requestID)
	if err != nil {
		return nil, journalFailure(err)
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
	r, ok, err := s.config.Journal.RequestGrant(ctx, p, requestID)
	if err != nil {
		return nil, journalFailure(err)
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
	head := h.Scope.SeqE
	if s.config.Views != nil || r.Request.View != nil {
		// Resolve cannot expand view permissions or change the selected snapshot.
		allowed, pinned, e := s.viewSelection(ctx, p, r.Request)
		if e != nil {
			return nil, e
		}
		if sel.Status == Granted {
			sel = allowed
			head = pinned
		}
	}
	d, err := s.decide(ctx, r.Request, sel, head)
	if err != nil {
		return nil, err
	}
	if err := s.config.Journal.Decide(ctx, p, r.Request, d, &r); err != nil {
		return nil, journalFailure(err)
	}
	return d, nil
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
	r, ok, err := s.config.Journal.ViewGrant(ctx, p, req.ViewId)
	if err != nil {
		return nil, journalFailure(err)
	}
	if p == "" || !ok || r.Released || !s.config.Now().Before(r.Decision.View.ExpiresAt.AsTime()) {
		return nil, ErrUnavailable
	}
	entries, err := s.entries(ctx, r.Decision.View)
	if err != nil {
		return nil, err
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
	page := &pb.Page{ViewId: req.ViewId, Source: proto.Clone(r.Decision.View.Source).(*pb.Source), SnapshotHead: r.Decision.View.SnapshotHead, Entries: entries[offset:end]}
	if end < len(entries) {
		b, _ := json.Marshal(cursor{req.ViewId, te.MustParseSeq(page.SnapshotHead), end})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

// Release and Revoke persist the same terminal grant release; only Release is
// available to the authenticated borrower over RPC.
func (s *Service) Release(ctx context.Context, p, vid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok, err := s.config.Journal.ViewGrant(ctx, p, vid)
	if err != nil {
		return journalFailure(err)
	}
	if p == "" || !ok {
		return ErrUnavailable
	}
	if r.Released {
		return nil
	}
	return journalFailure(s.config.Journal.Release(ctx, r))
}
func (s *Service) Revoke(ctx context.Context, p, vid string) error { return s.Release(ctx, p, vid) }
