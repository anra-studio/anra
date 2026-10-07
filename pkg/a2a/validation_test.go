package a2a_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"
	a "github.com/scbizu/anra/pkg/a2a"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

func TestLocalStructValidationBeforePolicy(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	policy := a.PolicyFunc(func(context.Context, string, a.BorrowRequest, te.Seq) (a.Selection, error) {
		t.Fatal("invalid local request reached policy")
		return a.Selection{}, nil
	})
	s := service(t, config(tape, journal, policy))
	for _, req := range []a.BorrowRequest{
		{},
		{RequestID: strings.Repeat("中", 86), Purpose: "test", Query: "test"},
		{RequestID: "request", Purpose: strings.Repeat("x", 8193), Query: "test"},
		{RequestID: "request", Purpose: "test", Query: strings.Repeat("x", 8193)},
		{RequestID: "request", Purpose: "test", Query: "test", Bounds: &tv.EntryRange{}},
		{RequestID: "request", Purpose: "test", Query: "test", Bounds: &tv.EntryRange{SeqS: seq(10), SeqE: seq(9)}},
	} {
		_, err := s.Borrow(t.Context(), "borrower", req)
		assertStructValidation(t, err)
	}
	for _, req := range []a.ReadRequest{{}, {ViewID: "view", PageSize: -1}, {ViewID: "view", PageSize: 101}} {
		_, err := s.Read(t.Context(), "borrower", req)
		assertStructValidation(t, err)
	}
}

func TestStructValidationKeepsByteAndSequenceSemantics(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	s := service(t, config(tape, journal, a.PolicyFunc(func(context.Context, string, a.BorrowRequest, te.Seq) (a.Selection, error) {
		return a.Selection{Status: a.Denied}, nil
	})))
	start := te.MustParseSeq("184467440737095516160000000001")
	r := a.BorrowRequest{RequestID: strings.Repeat("中", 85), Purpose: strings.Repeat("x", 8192), Query: strings.Repeat("x", 8192), Bounds: &tv.EntryRange{SeqS: start, SeqE: start.Next()}}
	d, err := s.Borrow(t.Context(), "borrower", r)
	if err != nil || d.Status != a.Denied {
		t.Fatalf("valid byte limits/large range rejected: %#v, %v", d, err)
	}
}

func TestServiceStructValidationRejectsMissingDependencies(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	for _, change := range []func(*a.Config){
		func(c *a.Config) { c.Source.AgentID = "" },
		func(c *a.Config) { c.Source.TapeID = "" },
		func(c *a.Config) { c.Owner = "" },
		func(c *a.Config) { c.JournalOwner = c.Owner },
		func(c *a.Config) { c.Tape = nil },
		func(c *a.Config) { var tape *changedTape; c.Tape = tape },
		func(c *a.Config) { c.Journal = nil },
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

func TestServiceRejectsNonBboltStorage(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	journal := openJournal(t, filepath.Join(t.TempDir(), "journal"))
	unsupported, err := jsonl.NewJSONLStorage("session", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*a.Config){
		func(c *a.Config) { c.Tape = unsupported },
	} {
		cfg := config(tape, journal, grantPolicy(1, 2))
		change(&cfg)
		if _, err := a.NewService(cfg); !errors.Is(err, a.ErrInvalidParams) {
			t.Fatalf("unsupported backend = %v", err)
		}
	}
}
