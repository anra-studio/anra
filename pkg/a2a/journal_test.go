package a2a_test

import (
	"context"
	"errors"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"math"
	"path/filepath"
	"testing"

	a "github.com/scbizu/anra/pkg/a2a"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	bolt "go.etcd.io/bbolt"
)

func journalOwner(tx *bolt.Tx) *bolt.Bucket {
	return tx.Bucket([]byte("anra:grant-journal/v2")).Bucket([]byte("grants"))
}

func TestGrantQueriesDoNotReplayAtStartupOrRuntime(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	appendText(t, tape, "local", "knowledge")
	cfg := config(tape, journal, grantPolicy(1, 2))
	s := service(t, cfg)
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	// Historical records and unrelated state must never be scanned by a lookup.
	if err := journal.Update(func(tx *bolt.Tx) error {
		b := journalOwner(tx)
		for _, name := range []string{"history", "requests"} {
			if err := b.Bucket([]byte(name)).Put([]byte("unrelated"), []byte("not valid JSON")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := journal.Stats().TxN
	s = service(t, cfg)
	if journal.Stats().TxN != before {
		t.Fatal("constructor touched journal data")
	}
	for i := 0; i < 10; i++ {
		if _, err := s.Get(t.Context(), "borrower", request().RequestId); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Release(t.Context(), "borrower", d.View.ViewId); err != nil {
		t.Fatal(err)
	}
	if _, err := service(t, cfg).Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("closed read = %v", err)
	}
}

func TestGrantDecisionIsIsolatedFromCaller(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	appendText(t, tape, "local", "knowledge")
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	vid := d.View.ViewId
	for _, get := range []func() (*pb.Decision, error){
		func() (*pb.Decision, error) { return s.Get(t.Context(), "borrower", request().RequestId) },
		func() (*pb.Decision, error) { return s.Borrow(t.Context(), "borrower", request()) },
	} {
		d, err := get()
		if err != nil {
			t.Fatal(err)
		}
		d.View.ViewId = "mutated"
		d.View.Ranges[0].SeqE = "100"
		page, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: vid})
		if err != nil || len(page.Entries) != 1 {
			t.Fatalf("persisted view mutated: %#v %v", page, err)
		}
	}
}

func TestGrantAppendRollsBackStateAndIndexWithHistory(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	appendText(t, tape, "local", "knowledge")
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	if _, err := s.Borrow(t.Context(), "borrower", request()); err != nil {
		t.Fatal(err)
	}
	var views int
	if err := journal.Update(func(tx *bolt.Tx) error {
		b := journalOwner(tx)
		views = b.Bucket([]byte("views")).Stats().KeyN
		return b.Bucket([]byte("history")).SetSequence(math.MaxUint64)
	}); err != nil {
		t.Fatal(err)
	}
	req := request()
	req.RequestId = "rollback"
	if _, err := s.Borrow(t.Context(), "borrower", req); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("append error = %v", err)
	}
	if _, err := s.Get(t.Context(), "borrower", req.RequestId); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("failed grant persisted: %v", err)
	}
	if err := journal.Update(func(tx *bolt.Tx) error {
		b := journalOwner(tx)
		if b.Bucket([]byte("views")).Stats().KeyN != views {
			t.Fatal("failed append left a view index")
		}
		return b.Bucket([]byte("history")).SetSequence(1)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Borrow(t.Context(), "borrower", req); err != nil {
		t.Fatal(err)
	}
}

func TestGrantIndexCorruptionFailsClosed(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	appendText(t, tape, "local", "knowledge")
	cfg := config(tape, journal, grantPolicy(1, 2))
	s := service(t, cfg)
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Update(func(tx *bolt.Tx) error {
		return journalOwner(tx).Bucket([]byte("views")).Put([]byte(d.View.ViewId), []byte("missing-request"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("bad view index = %v", err)
	}
	if _, err := s.Get(t.Context(), "borrower", request().RequestId); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("bad request index = %v", err)
	}
	cfg.Source.TapeId = "replacement"
	if _, err := service(t, cfg).Get(t.Context(), "borrower", request().RequestId); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("source replacement = %v", err)
	}
}

func TestGrantAppendFailureDoesNotReturnDecision(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	path := filepath.Join(root, "journal")
	journal := openJournal(t, path)
	cfg := config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		if err := journal.Close(); err != nil {
			t.Fatal(err)
		}
		return a.Selection{Status: a.Denied}, nil
	}))
	if _, err := service(t, cfg).Borrow(t.Context(), "borrower", request()); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("closed db append = %v", err)
	}
	cfg.Journal = openJournal(t, path)
	if _, err := service(t, cfg).Get(t.Context(), "borrower", request().RequestId); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("failed append persisted: %v", err)
	}
}

func TestGrantWriteRejectsStaleConcurrentPolicyDecision(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	entered, resume := make(chan struct{}), make(chan struct{})
	cfg := config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		close(entered)
		<-resume
		return a.Selection{Status: a.Pending}, nil
	}))
	first := service(t, cfg)
	result := make(chan error, 1)
	go func() { _, err := first.Borrow(context.Background(), "borrower", request()); result <- err }()
	<-entered
	cfg.Policy = a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Denied}, nil
	})
	second := service(t, cfg)
	_, err := second.Borrow(t.Context(), "borrower", request())
	close(resume)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, a.ErrConflict) {
		t.Fatalf("stale writer = %v", err)
	}
	d, err := first.Get(t.Context(), "borrower", request().RequestId)
	if err != nil || d.Status != a.Denied {
		t.Fatalf("committed decision overwritten: %#v %v", d, err)
	}
	if err := journal.View(func(tx *bolt.Tx) error {
		if n := journalOwner(tx).Bucket([]byte("history")).Stats().KeyN; n != 1 {
			t.Fatalf("audit records = %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyTapeJournalIsNotSilentlyIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal")
	tapeJournal := openTape(t, path, "grants")
	if err := tapeJournal.Close(); err != nil {
		t.Fatal(err)
	}
	journal := openJournal(t, path)
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	if _, err := s.Get(t.Context(), "borrower", "request"); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("legacy journal = %v", err)
	}
}

func TestBorrowRetryComparesBoundsByValue(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	s := service(t, config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Denied}, nil
	})))
	req := request()
	req.Bounds = pbRange(tv.EntryRange{SeqS: seq(1), SeqE: seq(3)})
	if _, err := s.Borrow(t.Context(), "borrower", req); err != nil {
		t.Fatal(err)
	}
	req.Bounds = &pb.EntryRange{SeqS: req.Bounds.SeqS, SeqE: req.Bounds.SeqE}
	if _, err := s.Borrow(t.Context(), "borrower", req); err != nil {
		t.Fatalf("equal bounds rejected: %v", err)
	}
	req.Bounds.SeqE = "4"
	if _, err := s.Borrow(t.Context(), "borrower", req); !errors.Is(err, a.ErrConflict) {
		t.Fatalf("changed bounds = %v", err)
	}
}
