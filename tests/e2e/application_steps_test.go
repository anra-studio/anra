package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/cucumber/godog"
	cp "github.com/scbizu/anra/gen/anra/a2a/collaboration/v1"
	pv "github.com/scbizu/anra/gen/anra/peer/v1"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/config"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/anra/pkg/peer"
	"github.com/scbizu/anra/pkg/registry"
	"google.golang.org/protobuf/proto"
	"path/filepath"
)

type hostedServer struct {
	cancel context.CancelFunc
	done   chan error
	node   *peer.Node
}

func (h *hostedServer) stop() error {
	h.cancel()
	err := <-h.done
	if h.node != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = errors.Join(err, h.node.Close(ctx))
	}
	return err
}

type applicationScenario struct {
	cli       *cliScenario
	directory string
	registry  *hostedServer
	peers     []*hostedServer
	env       map[string]*string
	answer    string
	err       error
}

func initializeApplication(sc *godog.ScenarioContext, cli *cliScenario) {
	a := &applicationScenario{cli: cli}
	initializeHTTP(sc, a)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*a = applicationScenario{cli: cli}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		err := a.stopPeers()
		if a.registry != nil {
			err = errors.Join(err, a.registry.stop())
		}
		for key, value := range a.env {
			if value == nil {
				err = errors.Join(err, os.Unsetenv(key))
			} else {
				err = errors.Join(err, os.Setenv(key, *value))
			}
		}
		return ctx, err
	})
	sc.Step(`^an application host with configured agents$`, a.start)
	sc.Step(`^the application runs agent "([^"]*)" with "([^"]*)"$`, func(id, input string) (runErr error) {
		settings, err := a.settings(id)
		if err != nil {
			return err
		}
		node, err := peer.Open(cli.s.ctx, settings)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, node.Close(context.Background())) }()
		a.answer, err = node.Run(cli.s.ctx, input)
		return err
	})
	sc.Step(`^the application serves agents "([^"]*)" and "([^"]*)"$`, func(first, second string) error {
		for _, id := range []string{first, second} {
			if err := a.serve(id); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the application asks agent "([^"]*)" with "([^"]*)"$`, func(id, input string) error {
		settings, err := a.settings(id)
		if err != nil {
			return err
		}
		result, err := peer.Ask(cli.s.ctx, settings, input)
		if err != nil {
			return err
		}
		if result.Status != cp.Status_STATUS_COMPLETED {
			return fmt.Errorf("application got %s", result.Status)
		}
		a.answer = result.Answer
		return nil
	})
	sc.Step(`^the application answer is "([^"]*)"$`, func(want string) error {
		if a.answer != want {
			return fmt.Errorf("application answer = %q, want %q", a.answer, want)
		}
		return nil
	})
	sc.Step(`^the application updates Alice's public metadata$`, func() error {
		client, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		entry, err := client.Lookup(cli.s.ctx, "alice")
		if err != nil {
			return err
		}
		value := proto.Clone(entry.Peer).(*pv.Peer)
		value.Description = "Alice reviews research"
		value.Skill = append(value.Skill, &pv.Skill{Id: "review", Name: "Review", Description: "Review research"})
		return a.peers[0].node.UpdatePeer(cli.s.ctx, value)
	})
	sc.Step(`^Alice's published card matches the committed Peer$`, func() error {
		client, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		entry, err := client.Lookup(cli.s.ctx, "alice")
		if err != nil {
			return err
		}
		card, err := a2a.Discover(cli.s.ctx, http.DefaultClient, entry.Peer.Address+"/.well-known/agent-card.json")
		if err != nil {
			return err
		}
		if card.Description != "Alice reviews research" || card.Description != entry.Peer.Description || len(card.Skills) != len(entry.Peer.Skill) {
			return fmt.Errorf("published card lost metadata: %v", card)
		}
		for i, skill := range card.Skills {
			if skill.ID != entry.Peer.Skill[i].Id || skill.Name != entry.Peer.Skill[i].Name {
				return fmt.Errorf("published skill differs: %v", skill)
			}
		}
		return nil
	})

	sc.Step(`^the application stops its peers$`, a.stopPeers)
	sc.Step(`^the application directory retains offline agents$`, func() error {
		directory, err := discovery.NewClient(a.directory)
		if err != nil {
			return err
		}
		records, err := directory.List(cli.s.ctx)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return errors.New("application directory lost its Peers")
		}
		for _, record := range records {
			if record.Presence.State != rv.PresenceState_PRESENCE_STATE_OFFLINE {
				return fmt.Errorf("peer %s state %s", record.Peer.Id, record.Presence.State)
			}
		}
		return nil
	})
	sc.Step(`^the application rejects agent "([^"]*)" at readiness$`, func(id string) (runErr error) {
		settings, err := a.settings(id)
		if err != nil {
			return err
		}
		node, err := peer.Open(cli.s.ctx, settings)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, node.Close(context.Background())) }()
		listener, err := net.Listen("tcp", settings.Endpoint)
		if err != nil {
			return err
		}
		rejected := errors.New("host rejected readiness")
		err = node.Serve(cli.s.ctx, listener, func(net.Addr) error { return rejected })
		if !errors.Is(err, rejected) {
			return fmt.Errorf("readiness error = %v, want rejection", err)
		}
		ctx, cancel := context.WithTimeout(cli.s.ctx, time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/.well-known/agent-card.json", nil)
		if err != nil {
			return err
		}
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
			return errors.New("peer HTTP endpoint remained open after readiness rejection")
		}
		return nil
	})
	sc.Step(`^the application opens an agent with an invalid identity$`, func() error {
		settings, err := a.settings("alice")
		if err != nil {
			return err
		}
		settings.ID = "../outside"
		node, err := peer.Open(cli.s.ctx, settings)
		a.err = err
		if node != nil {
			return errors.Join(errors.New("invalid identity opened storage"), node.Close(context.Background()))
		}
		return nil
	})
	sc.Step(`^the application rejects the identity$`, func() error {
		if !errors.Is(a.err, config.ErrInvalidAgentID) {
			return fmt.Errorf("identity error = %v", a.err)
		}
		return nil
	})
}

func (a *applicationScenario) start() error {
	a.env = make(map[string]*string)
	for key, value := range map[string]string{"OPENAI_API_KEY": "e2e-model-key", "OPENAI_BASE_URL": a.cli.s.modelServer.URL} {
		if old, ok := os.LookupEnv(key); ok {
			a.env[key] = &old
		} else {
			a.env[key] = nil
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.cli.s.ctx)
	a.registry = &hostedServer{cancel: cancel, done: make(chan error, 1)}
	ready := make(chan string, 1)
	go func() {
		a.registry.done <- registry.Serve(ctx, listener, registry.Config{Path: filepath.Join(a.cli.s.root, "application-registry.db"), HeartbeatTimeout: time.Second}, func(addr net.Addr) error { ready <- "http://" + addr.String(); return nil })
	}()
	select {
	case a.directory = <-ready:
	case err := <-a.registry.done:
		// Preserve the result for cleanup after an unsuccessful startup.
		a.registry.done <- err
		return fmt.Errorf("start application registry: %w", err)
	case <-a.cli.s.ctx.Done():
		return a.cli.s.ctx.Err()
	}
	return a.cli.writeAgentConfig(a.directory)
}

func (a *applicationScenario) settings(id string) (config.Settings, error) {
	file, err := config.Load(a.cli.configPath())
	if err != nil {
		return config.Settings{}, err
	}
	return file.Agent(id)
}

func (a *applicationScenario) serve(id string) error {
	settings, err := a.settings(id)
	if err != nil {
		return err
	}
	node, err := peer.Open(a.cli.s.ctx, settings)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", settings.Endpoint)
	if err != nil {
		return errors.Join(err, node.Close(context.Background()))
	}
	ctx, cancel := context.WithCancel(a.cli.s.ctx)
	host := &hostedServer{cancel: cancel, done: make(chan error, 1), node: node}
	a.peers = append(a.peers, host)
	ready := make(chan struct{}, 1)
	go func() {
		host.done <- node.Serve(ctx, listener, func(net.Addr) error { ready <- struct{}{}; return nil })
	}()
	select {
	case <-ready:
		return nil
	case err := <-host.done:
		host.done <- err
		return fmt.Errorf("start application peer %s: %w", id, err)
	case <-a.cli.s.ctx.Done():
		return a.cli.s.ctx.Err()
	}
}

func (a *applicationScenario) stopPeers() error {
	var err error
	for _, host := range a.peers {
		err = errors.Join(err, host.stop())
	}
	a.peers = nil
	return err
}
