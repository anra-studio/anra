package agent

import (
	"errors"
	"net/http"

	"github.com/anra-studio/anra/pkg/a2a"
	agenttools "github.com/anra-studio/anra/pkg/agent/tools"
	"github.com/anra-studio/anra/pkg/discovery"
	tapeagent "github.com/scbizu/tape-go/pkg/agent"
	tapetools "github.com/scbizu/tape-go/pkg/agent/tools"
	"google.golang.org/adk/tool"
)

// WithRegistry configures the network services used by the built-in peer tools.
func WithRegistry(directory *discovery.Client, client *http.Client) Option {
	return func(a *Anra) error {
		if directory == nil || client == nil {
			return errors.New("agent: peer services must not be nil")
		}
		a.runtime.registry, a.runtime.peerHTTP = directory, client
		return nil
	}
}

// WithBorrowClient connects the built-in context borrowing tool to its peer.
func WithBorrowClient(client *a2a.Client) Option {
	return func(a *Anra) error {
		if client == nil {
			return errors.New("agent: nil borrow client")
		}
		a.runtime.borrowClient = client
		return nil
	}
}

func (a *Anra) builtinTools() ([]tool.Tool, error) {
	bash, err := tapetools.NewBashTool(tapeagent.NewCommandRegistry(tapeagent.BuiltinBashCommand()))
	if err != nil {
		return nil, err
	}
	peers, err := agenttools.NewPeerTools(a.runtime.registry, a.runtime.peerHTTP, a.role)
	if err != nil {
		return nil, err
	}
	tools := []tool.Tool{bash, peers["discover_peers"], peers["send_peer_message"]}
	if a.runtime.borrowClient == nil {
		borrow, err := agenttools.NewPeerBorrowTool(a.runtime.registry, a.runtime.peerHTTP, a.role)
		if err != nil {
			return nil, err
		}
		tools = append(tools, borrow)
	}
	if a.runtime.borrowClient != nil {
		borrow, err := agenttools.NewBorrowTool(a.runtime.borrowClient)
		if err != nil {
			return nil, err
		}
		tools = append(tools, borrow)
	}
	return tools, nil
}
