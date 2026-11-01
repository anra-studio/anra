package e2e_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
	anramcp "github.com/anra-studio/anra/pkg/mcp"
	"github.com/cucumber/godog"
	te "github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/storage/jsonl"
	tv "github.com/scbizu/tape-go/pkg/tape/view"
	"google.golang.org/protobuf/proto"
)

func initializeMCPStorage(sc *godog.ScenarioContext, m *mcpScenario) {
	sc.Step(`^the embedding runtime uses JSONL Tape storage$`, m.useJSONLTape)
	sc.Step(`^the embedding runtime supplies its state store$`, m.useSuppliedState)
	sc.Step(`^the MCP service restarts with the supplied state store$`, m.useSuppliedState)
	sc.Step(`^a service using that state store rejects another Source$`, func() error {
		source := proto.Clone(m.agent.Source()).(*pb.Source)
		source.TapeId = "another-source"
		service, err := anramcp.NewService(anramcp.Config{Source: source, Owner: "vesta", Tape: m.tape, State: m.suppliedState})
		if service != nil {
			_ = service.Close()
		}
		if !errors.Is(err, anramcp.ErrInvalidReference) {
			return fmt.Errorf("mismatched state source: %v", err)
		}
		return nil
	})
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
	if err := m.service.Close(); err != nil {
		return err
	}
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: tape, StatePath: m.statePath(),
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return nil
}

func (m *mcpScenario) useSuppliedState() error {
	m.server.Close()
	m.server = nil
	if err := m.service.Close(); err != nil {
		return err
	}
	m.service = nil
	var err error
	if m.suppliedState == nil {
		m.suppliedState, err = anramcp.OpenState(m.statePath(), m.agent.Source())
		if err != nil {
			return err
		}
	}
	m.service, err = anramcp.NewService(anramcp.Config{
		Source: m.agent.Source(), Owner: "vesta", Tape: m.tape, State: m.suppliedState,
		Now: func() time.Time { return time.Unix(0, m.clock.Load()) },
	})
	if err != nil {
		return err
	}
	m.serve()
	return nil
}
