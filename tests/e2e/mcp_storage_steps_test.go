package e2e_test

import (
	"context"
	"crypto/rand"
	"path/filepath"
	"slices"
	"sync"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/model/view/v1"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	ae "github.com/anra-studio/anra/pkg/model/entry"
	bv "github.com/anra-studio/anra/pkg/model/view"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// The embedding runtime owns these fixtures across MCP service lifetimes.
// Values are replaced under the lock and never mutated after publication.
type mcpProviders struct {
	mu sync.RWMutex

	roles map[anramcp.RoleRef]anramcp.RoleDefinition

	grants map[string]anramcp.Grant

	grantErr error
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

func (p *mcpProviders) VerifyGrant(ctx context.Context, credential string) (*anramcp.Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.grantErr != nil {
		return nil, p.grantErr
	}
	g, ok := p.grants[credential]
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
	delete(p.grants, credential)
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
		if _, err := tape.Store(m.tapeContext(), ae.FromTape(raw)); err != nil {
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

// scopedGrants enforces what a GrantVerifier owes the service it is wired to.
type scopedGrants struct {
	*mcpProviders
	source *pb.Source

	now func() time.Time
}

func (m *mcpScenario) grantsFor(source *pb.Source) anramcp.GrantVerifier {
	return scopedGrants{
		mcpProviders: m.providers,
		source:       source,
		now: func() time.Time {
			return time.Unix(0, m.clock.Load())
		},
	}
}

func (v scopedGrants) VerifyGrant(ctx context.Context, credential string) (*anramcp.Grant, error) {
	g, err := v.mcpProviders.VerifyGrant(ctx, credential)
	if err != nil {
		return nil, err
	}

	if !g.ExpiresAt.After(v.now()) || g.Role.ID != v.source.AgentId {
		return nil, anramcp.ErrPermissionDenied
	}
	return g, nil
}

func (m *mcpScenario) configureViews(tape storage.TapeStorage) error {
	if m.views != nil {
		if err := m.views.Close(); err != nil {
			return err
		}
	}

	var err error

	m.views, err = bv.NewService(
		bv.Config{
			Source:     m.agent.Source(),
			Owner:      "vesta",
			Tape:       tape,
			RecordPath: filepath.Join(m.s.root, "mcp.grants.db"),
			Views: recoveredViews{
				Tape:  tape,
				Owner: "vesta",
			},
			Now: func() time.Time {
				return time.Unix(0, m.clock.Load())
			},
		},
	)
	if err != nil {
		return err
	}

	m.service, err = anramcp.NewService(
		anramcp.Config{
			Views:  m.views,
			Roles:  m.providers,
			Grants: m.grantsFor(m.agent.Source()),
		},
	)

	return err
}

func (m *mcpScenario) issueLease(caller string, ref *pb.Ref) error {
	lease, err := m.views.Grant(m.s.ctx, caller, rand.Text(), ref)
	if err != nil {
		return err
	}

	m.lease = lease

	m.grant.LeaseID = lease.Id

	return nil
}
