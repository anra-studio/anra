package a2a_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	pb "github.com/scbizu/anra/gen/anra/a2a/tape/v1"
	a "github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/model/journal"
	"google.golang.org/protobuf/proto"
)

// memoryJournal is a non-bbolt implementation of a.Journal, showing that the
// service depends on the storage contract and not on the bundled store.
type memoryJournal struct {
	mu     sync.Mutex
	source *pb.Source
	states map[string]journal.State
}

func (m *memoryJournal) Source() *pb.Source { return m.source }

func (m *memoryJournal) RequestGrant(_ context.Context, requester, requestID string) (journal.State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.states[requester+"/"+requestID]
	return st, ok, nil
}

func (m *memoryJournal) ViewGrant(_ context.Context, requester, viewID string) (journal.State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.states {
		if st.Decision.GetView().GetViewId() == viewID {
			return st, st.Requester == requester, nil
		}
	}
	return journal.State{}, false, nil
}

func (m *memoryJournal) Decide(_ context.Context, requester string, req *pb.BorrowRequest, d *pb.Decision, previous *journal.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := requester + "/" + req.GetRequestId()
	current, found := m.states[k]
	if (previous == nil && found) || (previous != nil && (!found || !proto.Equal(current.Decision, previous.Decision))) {
		return journal.ErrConflict
	}
	m.states[k] = journal.State{Requester: requester, Request: req, Decision: d}
	return nil
}

func (m *memoryJournal) Release(_ context.Context, st journal.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := st.Requester + "/" + st.Request.GetRequestId()
	current, found := m.states[k]
	if !found || current.Released {
		return journal.ErrConflict
	}
	current.Released = true
	m.states[k] = current
	return nil
}

func TestServiceRunsOnAnotherJournalImplementation(t *testing.T) {
	tape := openTape(t, filepath.Join(t.TempDir(), "source"), "local")
	appendText(t, tape, "local", "knowledge")
	cfg := config(tape, nil, grantPolicy(1, 2))
	cfg.Journal = &memoryJournal{source: source(), states: map[string]journal.State{}}
	s := service(t, cfg)
	d, err := s.Borrow(t.Context(), "borrower", request())
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("read = %#v, %v", page, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close foreign journal = %v", err)
	}
	if err := s.Release(t.Context(), "borrower", d.View.ViewId); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(t.Context(), "borrower", &pb.ReadRequest{ViewId: d.View.ViewId}); !errors.Is(err, a.ErrUnavailable) {
		t.Fatalf("released read = %v", err)
	}
}
