package a2a_test

import (
	"buf.build/go/protovalidate"
	"context"
	"errors"
	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	a "github.com/scbizu/anra/pkg/a2a"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func TestLocalProtobufValidationBeforePolicy(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	policy := a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		t.Fatal("invalid local request reached policy")
		return a.Selection{}, nil
	})
	s := service(t, config(tape, journal, policy))
	for _, req := range []*pb.BorrowRequest{
		{},
		{RequestId: strings.Repeat("中", 86), Purpose: "test", Query: "test"},
		{RequestId: "request", Purpose: strings.Repeat("x", 8193), Query: "test"},
		{RequestId: "request", Purpose: "test", Query: strings.Repeat("x", 8193)},
		{RequestId: "request", Purpose: "test", Query: "test", Bounds: pbRange(tv.EntryRange{})},
		{RequestId: "request", Purpose: "test", Query: "test", Bounds: pbRange(tv.EntryRange{SeqS: seq(10), SeqE: seq(9)})},
	} {
		_, err := s.Borrow(t.Context(), "borrower", req)
		assertMessageValidation(t, err)
	}
	for _, req := range []*pb.ReadRequest{{}, {ViewId: "view", PageSize: -1}, {ViewId: "view", PageSize: 101}} {
		_, err := s.Read(t.Context(), "borrower", req)
		assertMessageValidation(t, err)
	}
}

func TestProtobufValidationKeepsByteAndSequenceSemantics(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	s := service(t, config(tape, journal, a.PolicyFunc(func(context.Context, string, *pb.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Denied}, nil
	})))
	start := te.MustParseSeq("184467440737095516160000000001")
	r := &pb.BorrowRequest{RequestId: strings.Repeat("中", 85), Purpose: strings.Repeat("x", 8192), Query: strings.Repeat("x", 8192), Bounds: pbRange(tv.EntryRange{SeqS: start, SeqE: start.Next()})}
	d, err := s.Borrow(t.Context(), "borrower", r)
	if err != nil || d.Status != a.Denied {
		t.Fatalf("valid byte limits/large range rejected: %#v, %v", d, err)
	}
}

func TestServiceStructValidationRejectsMissingDependencies(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	for _, change := range []func(*a.Config){
		func(c *a.Config) { c.Source = nil },
		func(c *a.Config) { c.Owner = "" },
		func(c *a.Config) { c.Tape = nil },
		func(c *a.Config) { var tape *bbolt.Bbolt; c.Tape = tape },
		func(c *a.Config) { c.Journal = nil },
		func(c *a.Config) { c.JournalPath = filepath.Join(t.TempDir(), "journal") },
		func(c *a.Config) { c.Policy = nil },
		func(c *a.Config) { var policy *a.BestRangePolicy; c.Policy = policy },
	} {
		c := config(tape, journal, grantPolicy(1, 2))
		change(&c)
		_, err := a.NewService(c)
		assertStructValidation(t, err)
	}
}

func assertStructValidation(t *testing.T, err error) {
	t.Helper()
	var fields validator.ValidationErrors
	if !errors.Is(err, a.ErrInvalidParams) || !errors.As(err, &fields) {
		t.Fatalf("want ErrInvalidParams and field validation errors, got %v", err)
	}
}

func assertMessageValidation(t *testing.T, err error) {
	t.Helper()
	var fields *protovalidate.ValidationError
	if !errors.Is(err, a.ErrInvalidParams) || !errors.As(err, &fields) {
		t.Fatalf("want ErrInvalidParams and protobuf validation errors, got %v", err)
	}
}

func TestLocalProtobufRequestsRejectNil(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	s := service(t, config(tape, journal, grantPolicy(1, 2)))
	if _, err := s.Borrow(t.Context(), "borrower", nil); !errors.Is(err, a.ErrInvalidParams) {
		t.Fatalf("nil Borrow = %v", err)
	}
	if _, err := s.Read(t.Context(), "borrower", nil); !errors.Is(err, a.ErrInvalidParams) {
		t.Fatalf("nil Read = %v", err)
	}
}

func TestServiceValidatesSourceWithProtobufRules(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	for _, source := range []*pb.Source{{}, {AgentId: "peer"}, {TapeId: "tape"}} {
		c := config(tape, journal, grantPolicy(1, 2))
		c.Source = source
		_, err := a.NewService(c)
		assertMessageValidation(t, err)
	}
}
