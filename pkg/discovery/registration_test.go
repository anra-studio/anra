package discovery

import (
	"testing"

	pv "github.com/anra-studio/anra/gen/anra/peer/v1"
	rv "github.com/anra-studio/anra/gen/anra/registry/v1"
)

func TestRegistrationReconcilesCanonicalSnapshotOnce(t *testing.T) {
	t.Parallel()

	var updates []string
	session := &Registration{
		value: &pv.Peer{
			Id:          "alice",
			Description: "old metadata",
		},
		onChange: func(value *pv.Peer) {
			updates = append(updates, value.Description)
			value.Description = "callback edit"
		},
	}
	entry := &rv.Entry{
		Peer: &pv.Peer{
			Id:          "alice",
			Description: "committed metadata",
		},
		Revision: "registry/2",
	}
	session.accept(entry)
	session.accept(entry)
	entry.Peer.Description = "caller edit"
	if session.value.Description != "committed metadata" || session.revision != "registry/2" {
		t.Fatalf("registered snapshot changed: %v at %q", session.value, session.revision)
	}
	if len(updates) != 1 || updates[0] != "committed metadata" {
		t.Fatalf("canonical metadata notifications = %v", updates)
	}
}
