package a2a_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	a "github.com/scbizu/anra/pkg/a2a"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

func TestServiceOwnsJournalOpenedFromPath(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	path := filepath.Join(root, "journal")
	appendText(t, tape, "local", "knowledge")
	cfg := config(tape, nil, grantPolicy(1, 2))
	cfg.JournalPath = path
	s := service(t, cfg)
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
	if _, err := s.Get(t.Context(), "borrower", request().RequestId); !errors.Is(err, a.ErrStorage) {
		t.Fatalf("closed journal = %v", err)
	}
	// The next service reopens the same file and finds the grant.
	again := service(t, cfg)
	defer func() { _ = again.Close() }()
	got, err := again.Get(t.Context(), "borrower", request().RequestId)
	if err != nil || !proto.Equal(got, d) {
		t.Fatalf("reopened grant = %v, %v", got, err)
	}
}

func TestCloseLeavesACallerOwnedJournalOpen(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	if err := s.Close(); err != nil {
		t.Fatalf("close shared journal = %v", err)
	}
	if _, ok, err := journal.RequestGrant(t.Context(), "borrower", request().RequestId); err != nil || ok {
		t.Fatalf("shared journal unusable = %v, %v", ok, err)
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

func TestReleaseIsIdempotentAndLeavesTheGrantUntouched(t *testing.T) {
	root := t.TempDir()
	tape := openTape(t, filepath.Join(root, "source"), "local")
	journal := openJournal(t, filepath.Join(root, "journal"))
	appendText(t, tape, "local", "knowledge")
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Release(t.Context(), "borrower", d.View.ViewId); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(t.Context(), "borrower", d.View.ViewId); err != nil {
		t.Fatalf("second release = %v", err)
	}
	if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("released read = %v", err)
	}
	got, err := s.Get(t.Context(), "borrower", request().RequestId)
	if err != nil || !proto.Equal(got, d) {
		t.Fatalf("grant changed by release: %v %v", got, err)
	}
}
