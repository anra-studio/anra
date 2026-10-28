// Package peer composes an agent's model, durable storage, discovery, and A2A
// service. CLI and web applications can own the same lifecycle through this API.
package peer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/anra/pkg/config"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/model"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
)

// Node owns an agent and its tape. Stop serving before calling Close.
// If shutdown times out, Close leaves storage open so cleanup can be retried.
type Node struct {
	agent    *agent.Anra
	tape     *bbolt.Bbolt
	settings config.Settings
	closeMu  sync.Mutex
	closed   bool
}

// Open initializes a configured agent, including recovery of interrupted work.
// The caller must Close the node before opening the same agent database again.
func Open(ctx context.Context, settings config.Settings) (*Node, error) {
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	llm, err := model.New(settings.Provider, settings.Model)
	if err != nil {
		return nil, err
	}
	id := settings.ID
	directory, err := discovery.NewClient(settings.Registry)
	if err != nil {
		return nil, err
	}
	options := []agent.Option{
		agent.WithLLM(llm),
		agent.WithTimeout(settings.Timeout),
		agent.WithSystemInstruction(settings.SystemInstruction()),
		agent.WithRegistry(directory, a2a.TrustedHTTPClient(id)),
	}
	tape, err := bbolt.NewBboltStorage("default", filepath.Join(settings.DataDir, id+".db"))
	if err != nil {
		return nil, err
	}
	if err := tape.Init(owner.WithOwnerId(ctx, id)); err != nil {
		return nil, errors.Join(fmt.Errorf("open agent tape: %w", err), tape.Close())
	}
	a, err := agent.New(id, tape, options...)
	if err != nil {
		return nil, errors.Join(err, tape.Close())
	}
	return &Node{agent: a, tape: tape, settings: settings}, nil
}

// Agent exposes the runtime for callers that need its lower-level capabilities.
func (n *Node) Agent() *agent.Anra { return n.agent }

// Run executes a local request against this node's persistent conversation.
func (n *Node) Run(ctx context.Context, input string) (string, error) {
	return n.agent.Run(ctx, input)
}

// Close drains reasoning before closing storage. It is safe to call repeatedly.
func (n *Node) Close(ctx context.Context) error {
	n.closeMu.Lock()
	defer n.closeMu.Unlock()
	if n.closed {
		return nil
	}
	if err := n.agent.Shutdown(ctx); err != nil {
		return err
	}
	err := n.tape.Close()
	n.closed = true
	return err
}
