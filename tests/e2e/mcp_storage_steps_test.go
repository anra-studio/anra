package e2e_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sync"
	"time"

	rpcpb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	"github.com/anra-studio/anra/pkg/flow/borrow"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// The embedding runtime owns these fixtures across MCP service lifetimes.
// Values are replaced under the lock and never mutated after publication.
// Credential policy belongs to this host fixture, not to the MCP adapter.
type mcpCredential struct {
	Caller      string `validate:"required,max=256"`
	LeaseID     string `validate:"required,max=256"`
	Role        anramcp.RoleRef
	AnchorSeq   string    `validate:"required,number,startsnotwith=0"`
	Permissions []string  `validate:"min=1,max=2,unique,dive,oneof=read_role read_tape_view"`
	ExpiresAt   time.Time `validate:"required"`
}

type mcpProviders struct {
	mu sync.RWMutex

	roles map[anramcp.RoleRef]anramcp.RoleDefinition

	credentials map[string]mcpCredential

	accessErr error

	revokeAfterAuthentication bool

	selection borrow.Selection
}

func (p *mcpProviders) GetRole(ctx context.Context, ref anramcp.RoleRef) (*anramcp.RoleDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	r, ok := p.roles[ref]
	if !ok {
		return nil, anramcp.ErrInvalidReference
	}
	return &r, nil
}

func (p *mcpProviders) credential(ctx context.Context, credential string) (*mcpCredential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.accessErr != nil {
		return nil, p.accessErr
	}
	g, ok := p.credentials[credential]
	if !ok {
		return nil, anramcp.ErrPermissionDenied
	}
	return &g, nil
}

func (p *mcpProviders) setRole(r anramcp.RoleDefinition) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r.Profile.Tools = slices.Clone(r.Profile.Tools)
	p.roles[r.RoleRef] = r
}

func (p *mcpProviders) revoke(credential string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.credentials, credential)
}

func (p *mcpProviders) review(ctx context.Context, _ string, _ *pb.BorrowRequest) (borrow.Selection, error) {
	if err := ctx.Err(); err != nil {
		return borrow.Selection{}, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.selection, nil
}

func (p *mcpProviders) setApproval(sel borrow.Selection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.selection = sel
}

func (m *mcpScenario) viewStorage() storage.TapeStorage {
	if m.handoffTape != nil {
		return m.handoffTape
	}
	return m.tape
}

func (m *mcpScenario) useJSONLTape() error {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return err
	}

	entries, err := m.tape.Range(
		m.tapeContext(),
		tv.EntryRange{
			SeqS: te.SeqFromUint64(1),
			SeqE: state.Scope.SeqE.Next(),
		},
	)
	if err != nil {
		return err
	}
	tape, err := jsonl.NewJSONLStorage("mcp-session", filepath.Join(m.s.root, "jsonl-tape"))
	if err != nil {
		return err
	}
	if err := tape.Init(m.tapeContext()); err != nil {
		return err
	}
	for _, raw := range entries.Raw {
		value, err := ae.FromTape(raw)
		if err != nil {
			return err
		}
		if _, err := tape.Store(m.tapeContext(), value); err != nil {
			return err
		}
	}
	m.server.Close()
	m.server = nil
	m.handoffTape = tape

	err = m.configureViews(tape)
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) restartJSONLTape() error {
	m.server.Close()
	tape, err := jsonl.NewJSONLStorage("mcp-session", filepath.Join(m.s.root, "jsonl-tape"))
	if err != nil {
		return err
	}
	if err := tape.Init(m.tapeContext()); err != nil {
		return err
	}
	m.handoffTape = tape

	err = m.configureViews(tape)
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) restartService() error {
	if m.client != nil {
		if err := m.client.Close(); err != nil {
			return err
		}
		m.client = nil
	}
	m.server.Close()
	m.server = nil
	var err error

	err = m.configureViews(m.tape)
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

// scopedAccess owns the host's credential policy and handoff resolution.
type scopedAccess struct {
	*mcpProviders
	source *pb.Source

	now func() time.Time
}

func (m *mcpScenario) accessFor(source *pb.Source) anramcp.AccessProvider {
	return scopedAccess{
		mcpProviders: m.providers,
		source:       source,
		now: func() time.Time {
			return time.Unix(0, m.clock.Load())
		},
	}
}

func (v scopedAccess) credential(ctx context.Context, credential string) (*mcpCredential, error) {
	g, err := v.mcpProviders.credential(ctx, credential)
	if err != nil {
		return nil, err
	}

	if !g.ExpiresAt.After(v.now()) || g.Role.ID != v.source.AgentId {
		return nil, anramcp.ErrPermissionDenied
	}
	return g, nil
}

func (v scopedAccess) Authenticate(ctx context.Context, credential string) error {
	_, err := v.credential(ctx, credential)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.revokeAfterAuthentication {
		delete(v.credentials, credential)
		v.revokeAfterAuthentication = false
	}
	return nil
}

func (v scopedAccess) Authorize(ctx context.Context, credential, anchorSeq, tool string) (*anramcp.ReadTarget, error) {
	g, err := v.credential(ctx, credential)
	if err != nil {
		return nil, err
	}
	if anchorSeq != g.AnchorSeq || !slices.Contains(g.Permissions, tool) {
		return nil, anramcp.ErrPermissionDenied
	}
	return &anramcp.ReadTarget{
		Caller:  g.Caller,
		LeaseID: g.LeaseID,
		Role:    g.Role,
	}, nil
}

func (m *mcpScenario) configureViews(tape storage.TapeStorage) error {
	if m.grants != nil {
		if err := m.grants.close(); err != nil {
			return err
		}
	}

	var err error

	m.grants, err = openGrantFixture(
		m.s.ctx,
		filepath.Join(m.s.root, "mcp.lease.db"),
		m.agent.Source(),
		"vesta",
		tape,
		func() time.Time {
			return time.Unix(0, m.clock.Load())
		},
		false,
	)
	if err != nil {
		return err
	}
	m.borrowing, err = m.grants.newService(
		borrow.Config{
			Source: m.agent.Source(),
			Tape:   m.grants.tape,
			Views: recoveredViews{
				Tape:  tape,
				Owner: "vesta",
			},
			Review: m.providers.review,
		},
	)
	if err != nil {
		return err
	}

	m.service, err = anramcp.NewService(
		anramcp.Config{
			Borrowing: m.borrowing,
			Roles:     m.providers,
			Access:    m.accessFor(m.agent.Source()),
		},
	)

	return err
}

func (m *mcpScenario) issueLease(caller string, ref *pb.Ref) error {
	decision, err := m.borrowing.Borrow(
		m.s.ctx,
		caller,
		&pb.BorrowRequest{
			RequestId: rand.Text(),
			Purpose:   "read shared context",
			Query:     "committed view",
			View:      ref,
		},
	)
	if err != nil {
		return err
	}
	if decision.Status != borrow.Granted || decision.Lease == nil {
		return fmt.Errorf("context borrowing returned %s without a lease", decision.Status)
	}

	m.lease = decision.Lease

	m.credential.LeaseID = decision.Lease.Id

	return nil
}

func (m *mcpScenario) borrowHandoffView() error {
	client, closeServer, err := m.peerREST()
	if err != nil {
		return err
	}
	defer closeServer()
	var response rpcpb.BorrowResponse
	err = client.message(
		http.MethodPost,
		"/v1/tape/borrows",
		&pb.BorrowRequest{
			RequestId: rand.Text(),
			Purpose:   "read shared context",
			Query:     "committed view",
			View:      m.handoff.Context.View,
		},
		&response,
	)
	if err != nil {
		return err
	}
	m.borrowDecision = response.Decision
	if m.borrowDecision == nil {
		return fmt.Errorf("borrowing response has no decision")
	}
	if m.borrowDecision.Status != borrow.Granted {
		return nil
	}
	if m.borrowDecision.Lease == nil {
		return fmt.Errorf("granted borrowing has no lease")
	}
	m.lease = m.borrowDecision.Lease
	m.credential.LeaseID = m.lease.Id
	m.token = m.authorize(m.credential)
	return nil
}
