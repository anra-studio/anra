package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/cucumber/godog"
	rv "github.com/scbizu/anra/gen/anra/registry/v1"
	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/discovery"
)

type teamScenario struct {
	c          *cliScenario
	registry   *cliScenario
	agents     map[string]*cliScenario
	concurrent []*cliScenario
	crashed    string
}

func initializeTeam(sc *godog.ScenarioContext, c *cliScenario) *teamScenario {
	t := &teamScenario{c: c}
	initializeADK(sc, t)
	initializeRecovery(sc, t)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*t = teamScenario{c: c, agents: make(map[string]*cliScenario)}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		err := t.stopAgents()
		if t.registry != nil {
			err = errors.Join(err, t.registry.stop())
		}
		return ctx, err
	})
	sc.Step(`^a running team Peer directory$`, func() error {
		t.registry = &cliScenario{s: c.s}
		return t.registry.startProcess("registry", "registry", "--listen", "127.0.0.1:0", "--heartbeat-timeout", "1s", "--data-dir", filepath.Join(c.s.root, "registry"))
	})
	sc.Step(`^team agents "([^"]*)" and "([^"]*)" are serving$`, func(a, b string) error {
		return t.startAgents(a, b)
	})
	sc.Step(`^I read the team directory$`, func() error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(c.s.ctx, 5*time.Second)
		defer cancel()
		records, err := client.List(ctx)
		c.output, c.err = "", err
		for _, record := range records {
			c.output += record.Peer.Id + "\t" + record.Peer.Address + "\n"
		}
		return nil
	})
	sc.Step(`^the team directory lists "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		if c.err != nil {
			return c.err
		}
		for _, name := range []string{a, b} {
			if !strings.Contains(c.output, name+"\t") {
				return fmt.Errorf("directory does not list %s: %s", name, c.output)
			}
		}
		return nil
	})
	sc.Step(`^running agent "([^"]*)" sends "([^"]*)" to "([^"]*)"$`, func(from, text, to string) error {
		t.send(c, from, to, text)
		return c.err
	})
	sc.Step(`^running agent "([^"]*)" attempts to greet "([^"]*)"$`, func(from, to string) { t.send(c, from, to, "hello") })
	sc.Step(`^I stop the team agents$`, t.stopAgents)
	sc.Step(`^I restart the team directory at the same address$`, func() error {
		address := strings.TrimPrefix(t.registry.peer, "http://")
		if err := t.registry.stop(); err != nil {
			return err
		}
		return t.registry.startProcess("registry", "registry", "--listen", address, "--heartbeat-timeout", "1s", "--data-dir", filepath.Join(c.s.root, "registry"))
	})
	sc.Step(`^both team agents eventually register again$`, func() error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(c.s.ctx, 4*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			records, err := client.List(ctx)
			if err != nil {
				return err
			}
			if len(records) == 2 && records[0].Presence.State == rv.PresenceState_PRESENCE_STATE_ONLINE && records[1].Presence.State == rv.PresenceState_PRESENCE_STATE_ONLINE {
				return nil
			}
			select {
			case <-ctx.Done():
				return errors.New("agents did not register after directory restart")
			case <-ticker.C:
			}
		}
	})
	sc.Step(`^I stop team agent "([^"]*)"$`, func(id string) error { return t.agents[id].stop() })
	sc.Step(`^I restart team agent "([^"]*)"$`, func(id string) error {
		return t.agents[id].startProcess(id, "serve", "--config", c.configPath(), "--agent", id)
	})
	sc.Step(`^team agent "([^"]*)" is still registered$`, func(id string) error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		entry, err := client.Lookup(c.s.ctx, id)
		if err != nil {
			return err
		}
		return discovery.RequireOnline(entry)
	})
	sc.Step(`^another server attempts to register as "([^"]*)"$`, func(id string) error {
		path := filepath.Join(c.s.root, "duplicate.toml")
		text := fmt.Sprintf("[lobby]\naddress = %q\n\n[agents.%[2]s]\ndesc = '%[2]s'\ndata-dir = 'duplicate'\nprovider = 'openai'\ntimeout = '1m'\nendpoint = '127.0.0.1:0'\n", t.registry.peer, id)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			return err
		}
		c.execute("serve", "--agent", id, "--config", path)
		return nil
	})
	sc.Step(`^both team agents send greetings simultaneously$`, func() {
		t.concurrent = []*cliScenario{{s: c.s}, {s: c.s}}
		var group sync.WaitGroup
		for i, from := range []string{"alice", "bob"} {
			group.Add(1)
			go func(i int, from string) {
				defer group.Done()
				to := "alice"
				if from == "alice" {
					to = "bob"
				}
				t.send(t.concurrent[i], from, to, "hello from "+from)
			}(i, from)
		}
		group.Wait()
	})
	sc.Step(`^both team agents finish their reasoning without blocking$`, func() error {
		for i, want := range []string{"bob: hello from alice", "alice: hello from bob"} {
			p := t.concurrent[i]
			if p.err != nil || strings.TrimSpace(p.output) != want && strings.TrimSpace(p.output) != "peer busy; continued locally" {
				return fmt.Errorf("concurrent send = %q, error %v; want %q", p.output, p.err, want)
			}
		}
		return nil
	})
	sc.Step(`^team agent "([^"]*)" crashes$`, func(id string) error {
		p := t.agents[id]
		if err := p.server.Process.Kill(); err != nil {
			return err
		}
		<-p.done
		p.server = nil
		t.crashed = id
		return nil
	})
	sc.Step(`^its presence eventually becomes unknown$`, func() error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(c.s.ctx, 4*time.Second)
		defer cancel()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			entry, err := client.Lookup(ctx, t.crashed)
			if err == nil && entry.Presence.State == rv.PresenceState_PRESENCE_STATE_UNKNOWN {
				return nil
			}
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return errors.New("crashed Peer did not become unknown")
			case <-ticker.C:
			}
		}
	})
	sc.Step(`^another runtime attempts to heartbeat or disconnect agent "([^"]*)"$`, func(name string) error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		_, err = client.Heartbeat(c.s.ctx, &rv.HeartbeatRequest{Id: name, RuntimeId: "another-runtime", Token: "another-token"})
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			return fmt.Errorf("unexpected heartbeat result: %v", err)
		}
		_, err = client.Disconnect(c.s.ctx, &rv.DisconnectRequest{Id: name, RuntimeId: "another-runtime", Token: "another-token"})
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			return fmt.Errorf("unexpected disconnect result: %v", err)
		}
		return nil
	})
	sc.Step(`^the operator forgets team agent "([^"]*)"$`, func(id string) error {
		c.execute("registry", "forget", id, "--address", t.registry.peer)
		return c.err
	})
	sc.Step(`^team agent "([^"]*)" is forgotten$`, func(id string) error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		_, err = client.Lookup(c.s.ctx, id)
		if connect.CodeOf(err) != connect.CodeNotFound {
			return fmt.Errorf("forgotten lookup: %v", err)
		}
		return nil
	})
	sc.Step(`^the busy agent keeps online presence across a heartbeat window$`, func() error {
		client, err := discovery.NewClient(t.registry.peer)
		if err != nil {
			return err
		}
		deadline := time.Now().Add(1200 * time.Millisecond)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			entry, err := client.Lookup(c.s.ctx, "alice")
			if err != nil {
				return err
			}
			if err := discovery.RequireOnline(entry); err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return nil
			}
			select {
			case <-ticker.C:
			case <-c.s.ctx.Done():
				return c.s.ctx.Err()
			}
		}
	})

	sc.Step(`^the team cards require no bearer credential$`, func() error {
		for name, p := range t.agents {
			card, err := a2a.Discover(c.s.ctx, http.DefaultClient, p.peer+"/.well-known/agent-card.json")
			if err != nil {
				return err
			}
			if len(card.SecuritySchemes) != 0 || len(card.SecurityRequirements) != 0 {
				return fmt.Errorf("agent %s advertises authentication requirements", name)
			}
		}
		return nil
	})
	return t
}

func (t *teamScenario) startAgents(a, b string) error {
	config := fmt.Sprintf("[lobby]\naddress = %q\n", t.registry.peer)
	for _, id := range []string{a, b} {
		config += fmt.Sprintf("[agents.%[1]s]\ndesc = '%[1]s'\ndata-dir = 'cli'\nprovider = 'openai'\ntimeout = '1m'\nendpoint = '127.0.0.1:0'\n", id)
	}
	if err := os.WriteFile(t.c.configPath(), []byte(config), 0o600); err != nil {
		return err
	}
	for _, id := range []string{a, b} {
		p := &cliScenario{s: t.c.s}
		t.agents[id] = p
		if err := p.startProcess(id, "serve", "--config", t.c.configPath(), "--agent", id); err != nil {
			return err
		}
	}
	return nil
}

func (t *teamScenario) send(c *cliScenario, from, to, text string) {
	if _, err := os.Stat(t.c.configPath()); err != nil {
		if err := t.c.writeAgentConfig(t.registry.peer); err != nil {
			c.output, c.err = err.Error(), err
			return
		}
	}
	c.execute("ask", "--agent", from, "--config", t.c.configPath(), "ask "+to+": "+text)
}

func (t *teamScenario) stopAgents() error {
	var err error
	for _, p := range t.agents {
		err = errors.Join(err, p.stop())
	}
	return err
}
