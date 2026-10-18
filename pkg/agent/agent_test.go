package agent_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
)

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	store := newStore(t, "p1")
	if _, err := agent.New("", store); err == nil {
		t.Fatal("New() empty principal error = nil")
	}
	if _, err := agent.New("p1", nil); err == nil {
		t.Fatal("New() nil tape error = nil")
	}
	var nilTape *bbolt.Bbolt
	if _, err := agent.New("p1", nilTape); err == nil {
		t.Fatal("New() typed nil tape error = nil")
	}
}

func newStore(t *testing.T, principal string) *bbolt.Bbolt {
	t.Helper()
	store, err := bbolt.NewBboltStorage("session", filepath.Join(t.TempDir(), "tape.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Init(owner.WithOwnerId(context.Background(), principal)); err != nil {
		t.Fatal(err)
	}
	return store
}
