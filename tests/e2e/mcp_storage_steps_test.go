package e2e_test

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

// The embedding runtime owns these fixtures across MCP service lifetimes.
// Values are replaced under the lock and never mutated after publication.
type mcpProviders struct {
	mu       sync.RWMutex
	roles    map[anramcp.RoleRef]anramcp.RoleDefinition
	grants   map[string]anramcp.Grant
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

func (m *mcpScenario) useJSONLTape() error {
	state, err := m.tape.Get(m.tapeContext())
	if err != nil {
		return err
	}
	entries, err := m.tape.Range(m.tapeContext(), tv.EntryRange{SeqS: te.SeqFromUint64(1), SeqE: state.Scope.SeqE.Next()})
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
	for _, entry := range entries.Raw {
		if _, err := tape.Store(m.tapeContext(), entry); err != nil {
			return err
		}
	}
	m.server.Close()
	m.server = nil
	m.handoffTape = tape
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: tape, Roles: m.providers, Grants: m.grantsFor(m.agent.Source()),
	})
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
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.grantsFor(m.agent.Source()),
	})
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
	now    func() time.Time
}

func (m *mcpScenario) grantsFor(source *pb.Source) anramcp.GrantVerifier {
	return scopedGrants{mcpProviders: m.providers, source: source, now: func() time.Time { return time.Unix(0, m.clock.Load()) }}
}

func (v scopedGrants) VerifyGrant(ctx context.Context, credential string) (*anramcp.Grant, error) {
	g, err := v.mcpProviders.VerifyGrant(ctx, credential)
	if err != nil {
		return nil, err
	}
	if !g.ExpiresAt.After(v.now()) || g.Role.ID != v.source.AgentId || !proto.Equal(g.View.Source, v.source) {
		return nil, anramcp.ErrPermissionDenied
	}
	return g, nil
}
