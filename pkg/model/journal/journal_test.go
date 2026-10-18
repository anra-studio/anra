package journal

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"github.com/timshannon/bolthold"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// auditBucket is the bucket bolthold derives from the audit record type.
const auditBucket = "auditEntry"

func testSource() *pb.Source { return &pb.Source{AgentId: "researcher", TapeId: "tape"} }

func openStore(t *testing.T, name string) *Store {
	t.Helper()
	return openStoreAt(t, filepath.Join(t.TempDir(), name), testSource())
}

func openStoreAt(t *testing.T, path string, source *pb.Source) *Store {
	t.Helper()
	s, err := Open(path, source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testRequest() *pb.BorrowRequest {
	return &pb.BorrowRequest{RequestId: "request-1", Purpose: "debug storage", Query: "bbolt"}
}

// testGrant builds the grant a service would record for req.
func testGrant(req *pb.BorrowRequest, viewID string) *pb.Decision {
	return &pb.Decision{RequestId: req.RequestId, Status: pb.DecisionStatus_DECISION_STATUS_GRANTED, View: &pb.View{
		ViewId:       viewID,
		Source:       testSource(),
		SnapshotHead: "3",
		Ranges:       []*pb.EntryRange{{SeqS: "1", SeqE: "3"}},
		ExpiresAt:    timestamppb.New(time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)),
	}}
}

func testDecision(req *pb.BorrowRequest, status pb.DecisionStatus) *pb.Decision {
	return &pb.Decision{RequestId: req.RequestId, Status: status}
}

// rawState reads the stored record for k without decoding it into a State.
func rawState(t *testing.T, s *Store, k string) requestState {
	t.Helper()
	var rec requestState
	if err := s.hold.Bolt().View(func(tx *bolt.Tx) error { return s.hold.TxGet(tx, k, &rec) }); err != nil {
		t.Fatal(err)
	}
	return rec
}

// bucketKey is the key bolthold writes for a string key.
func bucketKey(t *testing.T, k string) []byte {
	t.Helper()
	b, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// auditKinds reads the trail in append order, which is by sequence, not by key.
func auditKinds(t *testing.T, s *Store) []string {
	t.Helper()
	var entries []auditEntry
	if err := s.hold.Find(&entries, bolthold.Where("Seq").Gt(uint64(0)).SortBy("Seq")); err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(entries))
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func TestOpenAndLookupsNeverReplayTheJournal(t *testing.T) {
	s := openStore(t, "journal")
	if n := s.hold.Bolt().Stats().TxN; n != 0 {
		t.Fatalf("Open ran %d transactions", n)
	}
	req := testRequest()
	if err := s.Decide(t.Context(), "borrower", req, testGrant(req, "view-1"), nil); err != nil {
		t.Fatal(err)
	}
	// Records the journal never looks up must not be decoded.
	if err := s.hold.Bolt().Update(func(tx *bolt.Tx) error {
		if err := tx.Bucket([]byte(auditBucket)).Put(bucketKey(t, "unrelated"), []byte("not valid JSON")); err != nil {
			return err
		}
		return tx.Bucket([]byte(requestStateBucket)).Put(bucketKey(t, "unrelated"), []byte("not valid JSON"))
	}); err != nil {
		t.Fatal(err)
	}
	before := s.hold.Bolt().Stats().TxN
	st, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId)
	if err != nil || !ok || st.Decision.Status != pb.DecisionStatus_DECISION_STATUS_GRANTED {
		t.Fatalf("lookup = %#v, %v, %v", st, ok, err)
	}
	if n := s.hold.Bolt().Stats().TxN - before; n != 1 {
		t.Fatalf("lookup ran %d transactions", n)
	}
}

func TestGrantSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	s := openStoreAt(t, path, testSource())
	req := testRequest()
	granted := testGrant(req, "view-1")
	if err := s.Decide(t.Context(), "borrower", req, granted, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStoreAt(t, path, testSource())
	st, ok, err := reopened.RequestGrant(t.Context(), "borrower", req.RequestId)
	if err != nil || !ok || !proto.Equal(st.Decision, granted) {
		t.Fatalf("reopened grant = %#v, %v, %v", st, ok, err)
	}
}

func TestStaleDecisionCannotOverwriteCommittedOutcome(t *testing.T) {
	s := openStore(t, "journal")
	req := testRequest()
	pending := pb.DecisionStatus_DECISION_STATUS_PENDING
	if err := s.Decide(t.Context(), "borrower", req, testDecision(req, pending), nil); err != nil {
		t.Fatal(err)
	}
	previous, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId)
	if err != nil || !ok || previous.Decision.Status != pending {
		t.Fatalf("pending = %#v, %v, %v", previous, ok, err)
	}
	denied := testDecision(req, pb.DecisionStatus_DECISION_STATUS_DENIED)
	if err := s.Decide(t.Context(), "borrower", req, denied, &previous); err != nil {
		t.Fatal(err)
	}
	// The same resolved state cannot be decided twice.
	if err := s.Decide(t.Context(), "borrower", req, denied, &previous); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale decision = %v", err)
	}
	// An existing request is never re-decided without its previous state.
	if err := s.Decide(t.Context(), "borrower", req, denied, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated decision = %v", err)
	}
	st, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId)
	if err != nil || !ok || st.Decision.Status != pb.DecisionStatus_DECISION_STATUS_DENIED {
		t.Fatalf("committed decision = %#v, %v, %v", st, ok, err)
	}
}

func TestDuplicateViewIDIsAConflict(t *testing.T) {
	s := openStore(t, "journal")
	first := testRequest()
	if err := s.Decide(t.Context(), "borrower", first, testGrant(first, "view-1"), nil); err != nil {
		t.Fatal(err)
	}
	second := &pb.BorrowRequest{RequestId: "request-2", Purpose: "debug storage", Query: "bbolt"}
	if err := s.Decide(t.Context(), "borrower", second, testGrant(second, "view-1"), nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate view = %v", err)
	}
}

func TestReleaseIsASeparateRecordAndLeavesTheGrantUntouched(t *testing.T) {
	s := openStore(t, "journal")
	req := testRequest()
	if err := s.Decide(t.Context(), "borrower", req, testGrant(req, "view-1"), nil); err != nil {
		t.Fatal(err)
	}
	before := rawState(t, s, key("borrower", req.RequestId))
	st, ok, err := s.ViewGrant(t.Context(), "borrower", "view-1")
	if err != nil || !ok || st.Released {
		t.Fatalf("view grant = %#v, %v, %v", st, ok, err)
	}
	if err := s.Release(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	// Releasing the same loaded state again is a conflict, not a rewrite.
	if err := s.Release(t.Context(), st); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeated release = %v", err)
	}
	// Another requester does not hold the grant.
	if _, ok, err := s.ViewGrant(t.Context(), "other", "view-1"); err != nil || ok {
		t.Fatalf("foreign view grant = %v, %v", ok, err)
	}
	reloaded, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId)
	if err != nil || !ok || !reloaded.Released {
		t.Fatalf("released grant = %#v, %v, %v", reloaded, ok, err)
	}
	if after := rawState(t, s, key("borrower", req.RequestId)); !reflect.DeepEqual(before, after) {
		t.Fatal("release rewrote the grant record")
	}
	if kinds := auditKinds(t, s); !slices.Equal(kinds, []string{grantKind, releaseKind}) {
		t.Fatalf("audit = %v", kinds)
	}
}

func TestDecideRollsBackWhenAuditFails(t *testing.T) {
	s := openStore(t, "journal")
	first := testRequest()
	if err := s.Decide(t.Context(), "borrower", first, testGrant(first, "view-1"), nil); err != nil {
		t.Fatal(err)
	}
	// Occupy the next audit key so the append collides inside the transaction.
	taken := bucketKey(t, auditKey(2))
	if err := s.hold.Bolt().Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(auditBucket)).Put(taken, []byte("{}"))
	}); err != nil {
		t.Fatal(err)
	}
	second := &pb.BorrowRequest{RequestId: "request-2", Purpose: "debug storage", Query: "bbolt"}
	if err := s.Decide(t.Context(), "borrower", second, testGrant(second, "view-2"), nil); !errors.Is(err, ErrStorage) {
		t.Fatalf("append error = %v", err)
	}
	if _, ok, err := s.RequestGrant(t.Context(), "borrower", second.RequestId); err != nil || ok {
		t.Fatalf("failed decision persisted = %v, %v", ok, err)
	}
	if _, ok, err := s.ViewGrant(t.Context(), "borrower", "view-2"); err != nil || ok {
		t.Fatalf("failed decision indexed a view = %v, %v", ok, err)
	}
	if err := s.hold.Bolt().Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(auditBucket)).Delete(taken) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Decide(t.Context(), "borrower", second, testGrant(second, "view-2"), nil); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptRecordFailsClosed(t *testing.T) {
	s := openStore(t, "journal")
	req := testRequest()
	if err := s.Decide(t.Context(), "borrower", req, testGrant(req, "view-1"), nil); err != nil {
		t.Fatal(err)
	}
	// The same record under another request's key, and a record that is not JSON.
	if err := s.hold.Bolt().Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(requestStateBucket))
		stored := b.Get(bucketKey(t, key("borrower", req.RequestId)))
		if stored == nil {
			return ErrStorage
		}
		if err := b.Put(bucketKey(t, key("borrower", "request-2")), stored); err != nil {
			return err
		}
		return b.Put(bucketKey(t, key("borrower", "request-3")), []byte("not valid JSON"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RequestGrant(t.Context(), "borrower", "request-2"); !errors.Is(err, ErrStorage) {
		t.Fatalf("mis-keyed record = %v", err)
	}
	if _, _, err := s.RequestGrant(t.Context(), "borrower", "request-3"); !errors.Is(err, ErrStorage) {
		t.Fatalf("invalid record = %v", err)
	}
	if _, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId); err != nil || !ok {
		t.Fatalf("valid record = %v, %v", ok, err)
	}
}

func TestLegacyTapeJournalIsNotSilentlyIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte("entries"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openStoreAt(t, path, testSource()).RequestGrant(t.Context(), "borrower", "request-1"); !errors.Is(err, ErrStorage) {
		t.Fatalf("legacy journal = %v", err)
	}
}

func TestPreviousLayoutFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucket([]byte("anra:grant-journal/v2"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openStoreAt(t, path, testSource()).RequestGrant(t.Context(), "borrower", "request-1"); !errors.Is(err, ErrStorage) {
		t.Fatalf("previous layout = %v", err)
	}
}

func TestReplacedSourceFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	s := openStoreAt(t, path, testSource())
	req := testRequest()
	if err := s.Decide(t.Context(), "borrower", req, testDecision(req, pb.DecisionStatus_DECISION_STATUS_DENIED), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	replacement := testSource()
	replacement.TapeId = "replacement"
	if _, _, err := openStoreAt(t, path, replacement).RequestGrant(t.Context(), "borrower", req.RequestId); !errors.Is(err, ErrStorage) {
		t.Fatalf("source replacement = %v", err)
	}
}

func TestCancelledContextWritesNothing(t *testing.T) {
	s := openStore(t, "journal")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := testRequest()
	if err := s.Decide(ctx, "borrower", req, testGrant(req, "view-1"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled decide = %v", err)
	}
	if _, ok, err := s.RequestGrant(t.Context(), "borrower", req.RequestId); err != nil || ok {
		t.Fatalf("cancelled decide persisted = %v, %v", ok, err)
	}
}
