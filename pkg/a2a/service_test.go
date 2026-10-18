package a2a_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"google.golang.org/protobuf/proto"
	"path/filepath"
	"sync"
	"testing"
	"time"

	a "github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/model/journal"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
)

func openTape(t *testing.T, path, principal string) *bbolt.Bbolt {
	t.Helper()
	tape, err := bbolt.NewBboltStorage("session", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tape.Close() })
	if err := tape.Init(owner.WithOwnerId(context.Background(), principal)); err != nil {
		t.Fatal(err)
	}
	return tape
}

func appendText(t *testing.T, tape *bbolt.Bbolt, principal, text string) {
	t.Helper()
	if _, err := tape.Store(owner.WithOwnerId(context.Background(), principal), te.NewEntry(te.WithEntryKind(te.EntryUser), te.WithEntryOwner(principal), te.WithEntryContent(text))); err != nil {
		t.Fatal(err)
	}
}
func grantPolicy(start, end uint64) a.Policy {
	return a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Granted, Range: &tv.EntryRange{SeqS: seq(start), SeqE: seq(end)}, TTL: time.Hour}, nil
	})
}
func config(tape *bbolt.Bbolt, journal *journal.Store, policy a.Policy) a.Config {
	c := a.Config{Source: source(), Owner: "local", Tape: tape, Policy: policy}
	if journal != nil {
		c.Journal = journal
	}
	return c
}
func source() *pb.Source {
	return &pb.Source{AgentId: "researcher", TapeId: "opaque-incarnation-1"}
}
func service(t *testing.T, c a.Config) *a.Service {
	t.Helper()
	s, err := a.NewService(c)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func request() *pb.BorrowRequest {
	return &pb.BorrowRequest{RequestId: "request-1", Purpose: "debug storage", Query: "bbolt"}
}
func closeTape(t *testing.T, tape *bbolt.Bbolt) {
	t.Helper()
	if err := tape.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGrantSnapshotRestartAndIsolation(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source")
	journalPath := filepath.Join(root, "journal")
	tape := openTape(t, sourcePath, "local")
	journal := openJournal(t, journalPath)
	for _, text := range []string{"one", "two", "three"} {
		appendText(t, tape, "local", text)
	}
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	cfg := config(tape, journal, grantPolicy(1, 4))
	cfg.Now = func() time.Time { return now }
	s := service(t, cfg)
	d, err := s.Borrow(context.Background(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	vid := d.View.ViewId
	page, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: vid, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Summary != "one" || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	appendText(t, tape, "local", "appended after snapshot")
	if _, err := s.Read(context.Background(), "intruder", &pb.ReadRequest{ViewId: vid}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder read = %v", err)
	}
	if _, err := s.Get(context.Background(), "intruder", request().RequestId); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder get = %v", err)
	}
	if err := s.Release(context.Background(), "intruder", vid); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("intruder release = %v", err)
	}
	closeTape(t, tape)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	tape = openTape(t, sourcePath, "local")
	journal = openJournal(t, journalPath)
	cfg.Tape = tape
	cfg.Journal = journal
	s = service(t, cfg)
	again, err := s.Borrow(context.Background(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	if again.View.ViewId != vid {
		t.Fatal("grant changed after restart")
	}
	changed := request()
	changed.Query = "different"
	if _, err := s.Borrow(context.Background(), "borrower", changed); !errors.Is(err, a.ErrConflict) {
		t.Fatalf("conflict = %v", err)
	}
	rest, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: vid, Cursor: page.NextCursor, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if rest.SnapshotHead != seq(3).String() || len(rest.Entries) != 2 || rest.Entries[1].Summary != "three" || rest.NextCursor != "" {
		t.Fatalf("snapshot page = %#v", rest)
	}
	second := request()
	second.RequestId = "second"
	other, err := s.Borrow(context.Background(), "borrower", second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: other.View.ViewId, Cursor: page.NextCursor}); !errors.Is(err, a.ErrInvalidParams) {
		t.Fatalf("cross-grant cursor = %v", err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: vid}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("expiry = %v", err)
	}
	if err := s.Release(context.Background(), "borrower", vid); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(context.Background(), "borrower", vid); err != nil {
		t.Fatal(err)
	}
	s = service(t, cfg)
	if _, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: vid}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("released after restart = %v", err)
	}
}

func TestPendingDenialResolveAndConcurrentIdempotency(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, tape, "local", "knowledge")
	calls := 0
	cfg := config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		calls++
		return a.Selection{Status: a.Pending}, nil
	}))
	s := service(t, cfg)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.Borrow(context.Background(), "borrower", request())
			if err != nil || d.Status != a.Pending {
				t.Errorf("borrow = %#v, %v", d, err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("policy called %d times", calls)
	}
	s = service(t, cfg)
	d, err := s.Get(context.Background(), "borrower", request().RequestId)
	if err != nil || d.Status != a.Pending {
		t.Fatalf("pending = %#v %v", d, err)
	}
	d, err = s.Resolve(context.Background(), "borrower", request().RequestId, a.Selection{Status: a.Granted, Range: &tv.EntryRange{SeqS: seq(1), SeqE: seq(2)}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(context.Background(), "borrower", d.View.ViewId); err != nil {
		t.Fatal(err)
	}
	if _, err := service(t, cfg).Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("revocation = %v", err)
	}
	deny := request()
	deny.RequestId = "denial"
	if _, err := s.Borrow(context.Background(), "borrower", deny); err != nil {
		t.Fatal(err)
	}
	denied, err := s.Resolve(context.Background(), "borrower", deny.RequestId, a.Selection{Status: a.Denied})
	if err != nil || denied.View != nil || denied.Status != a.Denied {
		t.Fatalf("denied = %#v %v", denied, err)
	}
	got, err := service(t, cfg).Borrow(context.Background(), "borrower", deny)
	if err != nil || got.Status != a.Denied {
		t.Fatalf("replayed denial = %#v %v", got, err)
	}
}

func TestTextExporterPreservesLargeSequence(t *testing.T) {
	value := te.NewEntry(
		te.WithEntryID(te.MustParseSeq("18446744073709551616")),
		te.WithEntryKind(te.EntryUser),
	)
	exported, err := (a.TextExporter{}).Export(value)
	if err != nil || exported.Seq != value.GetID().String() {
		t.Fatalf("Export() = %#v, error %v", exported, err)
	}
}

func TestBorrowPreservesLargeSequences(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	start := te.MustParseSeq("18446744073709551616")
	value := te.NewEntry(te.WithEntryID(start), te.WithEntryOwner("local"), te.WithEntryKind(te.EntryUser), te.WithEntryContent("large sequence"))
	if _, err := tape.Store(owner.WithOwnerId(context.Background(), "local"), value); err != nil {
		t.Fatal(err)
	}
	cfg := config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Granted, Range: &tv.EntryRange{SeqS: start, SeqE: start.Next()}, TTL: time.Hour}, nil
	}))
	s := service(t, cfg)
	d, err := s.Borrow(context.Background(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	// Recover the grant from the journal before reading its source range.
	page, err := service(t, cfg).Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId})
	if err != nil || page.SnapshotHead != start.String() || len(page.Entries) != 1 || page.Entries[0].Seq != start.String() {
		t.Fatalf("Read() = %#v, error %v", page, err)
	}
	data, err := proto.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var decoded pb.Page
	if err := proto.Unmarshal(data, &decoded); err != nil || decoded.Entries[0].Seq != start.String() {
		t.Fatalf("page round trip = %#v, error %v", &decoded, err)
	}
}

func TestFailClosedOnCorruptionAndMissingSourceEntry(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source")
	tape := openTape(t, sourcePath, "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, tape, "local", "secret")
	appendText(t, tape, "local", "snapshot head")
	cfg := config(tape, journal, grantPolicy(1, 2))
	s := service(t, cfg)
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	// Inject a real storage gap, then reopen the source with the same identity.
	closeTape(t, tape)
	raw := openDB(t, sourcePath)
	if err := raw.Update(func(tx *bolt.Tx) error {
		entries := tx.Bucket([]byte("entries")).Bucket([]byte("local")).Bucket([]byte("session"))
		k, _ := entries.Cursor().First()
		return entries.Delete(k)
	}); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Tape = openTape(t, sourcePath, "local")
	s = service(t, cfg)
	if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("missing source entry = %v", err)
	}
}
func TestExportDoesNotLeakExtensionsAndRejectsUnsupportedKinds(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	value := te.CustomEntry{Entry: te.NewEntry(te.WithEntryKind(te.EntryAssistant), te.WithEntryOwner("local"), te.WithEntryContent("safe summary")), Extensions: map[string]any{"secret": "private token"}}
	if _, err := tape.Store(owner.WithOwnerId(context.Background(), "local"), value); err != nil {
		t.Fatal(err)
	}
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	d, err := s.Borrow(context.Background(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId})
	if err != nil {
		t.Fatal(err)
	}
	if string(page.Entries[0].Payload.JsonData) != `"safe summary"` {
		t.Fatalf("payload = %s", page.Entries[0].Payload.JsonData)
	}
	if _, err := (a.TextExporter{}).Export(te.NewEntry(te.WithEntryKind("anra:private"))); !errors.Is(err, a.ErrUnsupported) {
		t.Fatalf("unsupported = %v", err)
	}
}
func TestSequencesBoundsAndRanking(t *testing.T) {
	var decoded te.Seq
	for _, bad := range []string{`1`, `"01"`, `"-1"`, `"+1"`, `"1e2"`} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if err := json.Unmarshal([]byte(`"18446744073709551615"`), &decoded); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(decoded)
	if string(out) != `"18446744073709551615"` {
		t.Fatalf("uint64 = %s", out)
	}
	ranker := a.RankerFunc(func(_ context.Context, q string, cs []string) ([]a.Score, error) {
		if q != "bbolt" || len(cs) != 2 || cs[0] != "allowed first" {
			return nil, fmt.Errorf("unexpected rank input: %s %v", q, cs)
		}
		return []a.Score{{Index: 1, Score: 1, Confidence: 1}, {Index: 0, Score: 1, Confidence: 1}}, nil
	})
	policy := a.BestRangePolicy{TTL: time.Hour, Ranker: ranker, Eligible: func(context.Context, string, *pb.BorrowRequest, te.Seq) ([]a.Candidate, error) {
		return []a.Candidate{{Summary: "allowed first", Range: tv.EntryRange{SeqS: seq(1), SeqE: seq(2)}}, {Summary: "allowed second", Range: tv.EntryRange{SeqS: seq(2), SeqE: seq(3)}}}, nil
	}}
	sel, err := policy.Select(context.Background(), "borrower", request(), seq(3))
	if err != nil || sel.Range.SeqS != seq(1) {
		t.Fatalf("stable best = %#v %v", sel, err)
	}
	policy.Ranker = a.RankerFunc(func(context.Context, string, []string) ([]a.Score, error) { return nil, nil })
	sel, err = policy.Select(context.Background(), "borrower", request(), seq(3))
	if err != nil || sel.Status != a.Denied {
		t.Fatalf("no match = %#v %v", sel, err)
	}
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	appendText(t, tape, "local", "one")
	appendText(t, tape, "local", "two")
	req := request()
	req.Bounds = pbRange(tv.EntryRange{SeqS: seq(2), SeqE: seq(3)})
	if _, err := service(t, config(tape, journal, grantPolicy(1, 3))).Borrow(context.Background(), "borrower", req); !errors.Is(err, a.ErrInvalidParams) {
		t.Fatalf("bounds widened = %v", err)
	}
	cfg := config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Granted, TTL: time.Hour}, nil
	}))
	d, err := service(t, cfg).Borrow(context.Background(), "borrower", request())
	if err != nil || len(d.View.Ranges) != 0 {
		t.Fatalf("empty grant = %#v %v", d, err)
	}
	p, err := service(t, cfg).Read(context.Background(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId})
	if err != nil || len(p.Entries) != 0 {
		t.Fatalf("empty read = %#v %v", p, err)
	}
}

func seq(value uint64) te.Seq { return te.SeqFromUint64(value) }

func TestBorrowKeepsCompleteSelectedView(t *testing.T) {
	const viewSize = 1001 // The selected view can exceed the former entry cap.
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	for i := 0; i < viewSize; i++ {
		appendText(t, tape, "local", "finding")
	}
	selected := tv.EntryRange{SeqS: seq(1), SeqE: seq(viewSize + 1)}
	s := service(t, config(tape, journal, grantPolicy(1, viewSize+1)))
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.View.Ranges) != 1 || !proto.Equal(d.View.Ranges[0], pbRange(selected)) {
		t.Fatalf("selected view changed: %#v", d.View.Ranges)
	}
	count := 0
	cursor := ""
	for {
		page, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId, Cursor: cursor, PageSize: a.MaxPageSize})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			count++
			if entry.Seq != seq(uint64(count)).String() {
				t.Fatalf("sequence %s at entry %d", entry.Seq, count)
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if count != viewSize {
		t.Fatalf("read %d entries, want complete view of %d", count, viewSize)
	}
}

func openJournal(t *testing.T, path string) *journal.Store {
	t.Helper()
	store, err := journal.Open(path, source())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func openDB(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func pbRange(r tv.EntryRange) *pb.EntryRange {
	return &pb.EntryRange{SeqS: r.SeqS.String(), SeqE: r.SeqE.String()}
}
