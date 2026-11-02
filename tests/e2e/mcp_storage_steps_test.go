package e2e_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
)

// The embedding runtime owns these fixtures across MCP service lifetimes.
// Values are replaced under the lock and never mutated after publication.
type mcpProviders struct {
	mu       sync.RWMutex
	roles    map[anramcp.RoleRef]anramcp.RoleDefinition
	grants   map[string]anramcp.Grant
	grantErr error
	roleErr  error
}

func (p *mcpProviders) GetRole(ctx context.Context, ref anramcp.RoleRef) (*anramcp.RoleDefinition, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.roleErr != nil {
		return nil, p.roleErr
	}
	r, ok := p.roles[ref]
	if !ok {
		return nil, nil
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
		return nil, nil
	}
	return &g, nil
}

func (p *mcpProviders) setRole(r anramcp.RoleDefinition) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r.Definition = append([]byte(nil), r.Definition...)
	p.roles[r.RoleRef] = r
}

func (p *mcpProviders) revoke(credential string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.grants, credential)
}

// This read-only dependency has no Store method; MCP needs only Range.
type unavailableMCPTape struct{}

func (unavailableMCPTape) Range(context.Context, tv.EntryRange, ...storage.RangeBy) (tv.EntryView, error) {
	return tv.EntryView{}, errors.New("PRIVATE-tape-failure")
}

func initializeMCPStorage(sc *godog.ScenarioContext, m *mcpScenario) {
	sc.Step(`^MCP construction rejects a missing "([^"]+)" provider$`, func(missing string) error {
		config := anramcp.Config{Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.providers}
		switch missing {
		case "roles":
			config.Roles = nil
		case "grants":
			config.Grants = nil
		case "typed nil roles":
			config.Roles = (*mcpProviders)(nil)
		case "typed nil grants":
			config.Grants = (*mcpProviders)(nil)
		default:
			return errors.New("unknown missing provider fixture")
		}
		if _, err := anramcp.NewService(config); err == nil {
			return errors.New("MCP accepted missing provider")
		}
		return nil
	})
	sc.Step(`^the embedding runtime uses JSONL Tape storage$`, m.useJSONLTape)
	sc.Step(`^the MCP service restarts with the injected providers$`, m.restartService)
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
		Source: m.agent.Source(), Owner: "vesta", Tape: tape, Roles: m.providers, Grants: m.providers,
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
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
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, Roles: m.providers, Grants: m.providers,
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return nil
}
