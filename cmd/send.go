package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/spf13/cobra"
)

func newSendCommand(common *agentOptions) *cobra.Command {
	var peer string
	var timeout time.Duration
	command := &cobra.Command{
		Use:     "send [flags] message",
		Short:   "Send a message from a local agent to a peer URL",
		Args:    messageArgs,
		PreRunE: func(*cobra.Command, []string) error { return validateTimeout(timeout) },
		RunE: func(command *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(command.Context(), timeout)
			defer cancel()
			modelRunner, err := newPeerRunner(ctx, peer)
			if err != nil {
				return err
			}
			return withAgent(ctx, *common, modelRunner, modelOptions{}, func(a *agent.Anra) error {
				return printAnswer(ctx, a, args, command.OutOrStdout())
			})
		},
	}
	command.Flags().StringVar(&peer, "peer", "", "peer base URL (required)")
	command.MarkFlagRequired("peer")
	command.Flags().DurationVar(&timeout, "timeout", time.Minute, "command timeout")
	return command
}

type bearerTransport struct {
	token string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}

type peerRunner struct{ client *a2aclient.Client }

func newPeerRunner(ctx context.Context, base string) (runner.Runner, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("--peer must be an HTTP(S) base URL, for example http://127.0.0.1:8080")
	}
	token, err := peerToken()
	if err != nil {
		return nil, err
	}
	h := &http.Client{Transport: bearerTransport{token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	card, err := a2a.Discover(ctx, h, strings.TrimRight(base, "/")+a2asrv.WellKnownAgentCardPath)
	if err != nil {
		return nil, fmt.Errorf("discover peer: %w", err)
	}
	for _, endpoint := range card.SupportedInterfaces {
		if endpoint == nil {
			return nil, errors.New("peer advertised an empty interface")
		}
		remote, err := url.Parse(endpoint.URL)
		if err != nil || remote.Scheme != u.Scheme || remote.Host != u.Host {
			return nil, errors.New("peer advertised an endpoint outside its origin")
		}
	}
	client, err := a2aclient.NewFromCard(ctx, card, a2aclient.WithJSONRPCTransport(h))
	if err != nil {
		return nil, err
	}
	return peerRunner{client}, nil
}

// Explicit sending needs no local LLM: the peer supplies the response. Anra.Run
// persists the outgoing text and incoming answer on the sender's own tape.
func (r peerRunner) RunTurn(ctx context.Context, req runner.Request) (runner.Response, error) {
	input := req.Messages[len(req.Messages)-1].Text
	result, err := r.client.SendMessage(ctx, &sdk.SendMessageRequest{Message: sdk.NewMessage(sdk.MessageRoleUser, sdk.NewTextPart(input))})
	if err != nil {
		return runner.Response{}, err
	}
	message, ok := result.(*sdk.Message)
	if !ok {
		return runner.Response{}, errors.New("peer returned a task instead of a text answer")
	}
	var text []string
	for _, part := range message.Parts {
		if part == nil {
			return runner.Response{}, errors.New("peer returned an empty message part")
		}
		if _, ok := part.Content.(sdk.Text); !ok {
			return runner.Response{}, errors.New("peer returned a non-text answer")
		}
		text = append(text, part.Text())
	}
	if strings.TrimSpace(strings.Join(text, "\n")) == "" {
		return runner.Response{}, errors.New("peer returned an empty answer")
	}
	return runner.Response{Output: strings.Join(text, "\n")}, nil
}
