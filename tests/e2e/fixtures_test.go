package e2e_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/anra-studio/anra/pkg/a2a"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

// ctx selects the Tape owner. On success the caller owns the open store.
func openBboltTape(ctx context.Context, session, path string) (*bbolt.Bbolt, error) {
	tape, err := bbolt.NewBboltStorage(session, path)
	if err != nil {
		return nil, err
	}
	if err := tape.Init(ctx); err != nil {
		return nil, errors.Join(err, tape.Close())
	}
	return tape, nil
}

func openFixtureTape(ctx context.Context, root, name, role string) (*bbolt.Bbolt, error) {
	return openBboltTape(owner.WithOwnerId(ctx, role), "session", filepath.Join(root, name+".db"))
}

// readAllEntries returns the physical entries through the owner's current head.
func readAllEntries(ctx context.Context, tape storage.TapeStorage) ([]entry.EntryLike, entry.Seq, error) {
	state, err := tape.Get(ctx)
	if err != nil {
		return nil, entry.Seq{}, err
	}
	head := state.Scope.SeqE
	if head.IsZero() {
		return nil, head, nil
	}
	entries, err := tape.Range(
		ctx,
		view.EntryRange{
			SeqS: entry.SeqFromUint64(1),
			SeqE: head.Next(),
		},
	)
	return entries.Raw, head, err
}

func newA2AClient(ctx context.Context, client *http.Client, card *sdk.AgentCard) (*a2aclient.Client, error) {
	return a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(client))
}

// messageClient exercises the external A2A boundary for a trusted caller holding a URL.
func messageClient(ctx context.Context, caller, base string) (*a2a.MessageClient, error) {
	client := a2a.TrustedHTTPClient(caller)
	card, err := a2a.Discover(ctx, client, strings.TrimRight(base, "/")+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		return nil, err
	}
	return a2a.NewMessageClientFromCard(ctx, client, card)
}

func trustedPeerHandler(card *sdk.AgentCard, executor a2asrv.AgentExecutor, borrowing *borrow.Service) (http.Handler, error) {
	return a2a.NewPeerHandler(card, a2asrv.NewHandler(executor), borrowing, a2a.TrustedPeerIdentity)
}

func trustedViewClient(caller string, card *sdk.AgentCard) (*a2a.Client, error) {
	return a2a.NewClient(a2a.TrustedHTTPClient(caller), card)
}
