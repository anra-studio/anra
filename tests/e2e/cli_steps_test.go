package e2e_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/scbizu/tape-go/pkg/tape/entry"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/scbizu/tape-go/pkg/tape/view"
)

var cliBinary string

func buildCLI(t *testing.T) {
	t.Helper()
	cliBinary = filepath.Join(t.TempDir(), "anra")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", cliBinary, "../../cmd/anra")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
}

type cliScenario struct {
	s                      *scenario
	server                 *exec.Cmd
	done                   chan error
	peer, serverID, output string
	err                    error
	env                    map[string]string
}

func initializeCLI(sc *godog.ScenarioContext, s *scenario) {
	c := &cliScenario{s: s}
	initializeConfig(sc, c)
	initializeEnvironment(sc, c)
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*c = cliScenario{s: s}
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		return ctx, c.stop()
	})
	sc.Step(`^I run the local CLI agent "([^"]*)" with "([^"]*)"$`, c.runLocal)
	sc.Step(`^I request CLI help for "([^"]*)"$`, func(command string) {
		args := []string{"--data-dir", c.dataDir()}
		if command != "root" {
			args = append(args, command)
		}
		c.execute("", append(args, "--help")...)
	})
	sc.Step(`^the CLI help lists "([^"]*)"$`, func(want string) error {
		if c.err != nil || !strings.Contains(c.output, want) {
			return fmt.Errorf("CLI help = %q, error = %v; want %q", c.output, c.err, want)
		}
		return nil
	})
	sc.Step(`^I run the CLI agent "([^"]*)" with "([^"]*)" and flags (before the command|after the message)$`, func(id, text, placement string) error {
		flags := []string{"--agent", id, "--data-dir", c.dataDir()}
		var args []string
		if placement == "before the command" {
			args = append(flags, "run", "--runner", "echo", text)
		} else {
			args = append([]string{"run", text}, flags...)
			args = append(args, "--runner", "echo")
		}
		c.execute("", args...)
		return c.err
	})
	sc.Step(`^the CLI answer is "([^"]*)"$`, func(want string) error {
		if c.err != nil || strings.TrimSpace(c.output) != want {
			return fmt.Errorf("CLI output = %q, error = %v; want %q", c.output, c.err, want)
		}
		return nil
	})
	sc.Step(`^the CLI agent "([^"]*)" is serving with the echo runner$`, c.start)
	sc.Step(`^the CLI agent "([^"]*)" sends "([^"]*)" to the serving agent$`, c.send)
	sc.Step(`^I restart the serving CLI agent$`, func() error {
		if err := c.stop(); err != nil {
			return err
		}
		return c.start(c.serverID)
	})
	sc.Step(`^I stop the serving CLI agent$`, c.stop)
	sc.Step(`^the CLI agent "([^"]*)" has this conversation on its tape$`, c.conversation)
	sc.Step(`^the CLI agent "([^"]*)" has an empty tape$`, func(id string) error {
		return c.conversation(id, &godog.Table{})
	})
	sc.Step(`^the CLI agent sends a greeting with the wrong peer token$`, func() {
		c.execute("wrong-token", "send", "--agent", "alice", "--data-dir", c.dataDir(), "--peer", c.peer, "hello")
	})
	sc.Step(`^the CLI command fails$`, func() error {
		if c.err == nil {
			return fmt.Errorf("CLI succeeded: %s", c.output)
		}
		return nil
	})
	sc.Step(`^I run the CLI with invalid configuration "([^"]*)"$`, c.invalid)
	sc.Step(`^the CLI command fails with "([^"]*)"$`, func(want string) error {
		if c.err == nil || !strings.Contains(c.output, want) {
			return fmt.Errorf("CLI output = %q, error = %v; want failure containing %q", c.output, c.err, want)
		}
		return nil
	})
	sc.Step(`^no CLI tape directory is created$`, func() error {
		_, err := os.Stat(c.dataDir())
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("CLI storage exists or could not be inspected: %v", err)
		}
		return nil
	})
}

func (c *cliScenario) dataDir() string { return filepath.Join(c.s.root, "cli") }

func cliEnv(token string) []string {
	var env []string
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "DEEPSEEK_API_KEY=") && !strings.HasPrefix(value, "ANRA_") {
			env = append(env, value)
		}
	}
	return append(env, "ANRA_PEER_TOKEN="+token)
}

func (c *cliScenario) environment(token string) []string {
	env := cliEnv(token)
	for key, value := range c.env {
		env = append(env, key+"="+value)
	}
	return env
}

func (c *cliScenario) execute(token string, args ...string) {
	cmd := exec.CommandContext(c.s.ctx, cliBinary, args...)
	cmd.Env = c.environment(token)
	output, err := cmd.CombinedOutput()
	c.output, c.err = string(output), err
}

func (c *cliScenario) runLocal(id, text string) error {
	c.execute("e2e-peer-token", "run", "--agent", id, "--data-dir", c.dataDir(), "--runner", "echo", text)
	return c.err
}

func (c *cliScenario) send(id, text string) error {
	c.execute("e2e-peer-token", "send", "--agent", id, "--data-dir", c.dataDir(), "--peer", c.peer, text)
	return c.err
}

func (c *cliScenario) start(id string) error {
	return c.startProcess(id, "serve", "--agent", id, "--data-dir", c.dataDir(), "--runner", "echo", "--listen", "127.0.0.1:0")
}

func (c *cliScenario) startProcess(id string, args ...string) error {
	cmd := exec.CommandContext(c.s.ctx, cliBinary, args...)
	cmd.Env = c.environment("e2e-peer-token")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		stderr.Close()
		return err
	}
	c.server, c.serverID, c.done = cmd, id, make(chan error, 1)
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			select {
			case ready <- scanner.Text():
			default:
			}
		}
	}()
	done := c.done
	go func() { done <- cmd.Wait() }()
	select {
	case line := <-ready:
		_, base, ok := strings.Cut(line, " listening at ")
		if !ok {
			return fmt.Errorf("unexpected server status: %s", line)
		}
		c.peer = base
		return nil
	case err := <-c.done:
		c.server = nil
		return fmt.Errorf("server exited before startup: %v", err)
	case <-c.s.ctx.Done():
		return c.s.ctx.Err()
	}
}

func (c *cliScenario) stop() error {
	if c.server == nil {
		return nil
	}
	defer func() { c.server = nil }()
	if err := c.server.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case err := <-c.done:
		return err
	case <-time.After(6 * time.Second):
		_ = c.server.Process.Kill()
		<-c.done
		return errors.New("CLI server did not shut down gracefully")
	}
}

func (c *cliScenario) conversation(id string, table *godog.Table) error {
	return c.conversationAt(c.dataDir(), id, "default", table)
}

func (c *cliScenario) conversationAt(dir, id, session string, table *godog.Table) error {
	tape, err := bbolt.NewBboltStorage(session, filepath.Join(dir, id+".db"))
	if err != nil {
		return err
	}
	defer tape.Close()
	ctx := owner.WithOwnerId(c.s.ctx, id)
	if err := tape.Init(ctx); err != nil {
		return err
	}
	state, err := tape.Get(ctx)
	if err != nil {
		return err
	}
	want := len(table.Rows)
	if want > 0 {
		want--
	}
	if state.Scope.SeqE.IsZero() {
		if want == 0 {
			return nil
		}
		return errors.New("CLI tape is empty")
	}
	entries, err := tape.Range(ctx, view.EntryRange{SeqS: entry.SeqFromUint64(1), SeqE: state.Scope.SeqE.Next()})
	if err != nil {
		return err
	}
	if len(entries.Raw) != want {
		return fmt.Errorf("CLI tape has %d entries, want %d", len(entries.Raw), want)
	}
	for i, raw := range entries.Raw {
		role := map[entry.EntryKind]string{entry.EntryUser: "user", entry.EntryAssistant: "assistant"}[raw.GetKind()]
		row := table.Rows[i+1].Cells
		if role != row[0].Value || raw.GetSummary() != row[1].Value || raw.GetOwner() != id {
			return fmt.Errorf("CLI tape entry %d = %s %q, owner = %q; want %s %q owned by %s", i, role, raw.GetSummary(), raw.GetOwner(), row[0].Value, row[1].Value, id)
		}
	}
	return nil
}

func (c *cliScenario) invalid(problem string) error {
	common := []string{"--agent", "alice", "--data-dir", c.dataDir()}
	args := append([]string{"run"}, common...)
	token := "e2e-peer-token"
	switch problem {
	case "missing message":
		args = append(args, "--runner", "echo")
	case "invalid agent":
		args = append(args, "--agent", "../alice", "--runner", "echo", "hello")
	case "missing key":
		args = append(args, "hello")
	case "missing token":
		args = append(append([]string{"serve"}, common...), "--runner", "echo")
		token = ""
	case "missing peer":
		args = append(append([]string{"send"}, common...), "hello")
	case "unknown runner":
		args = append(args, "--runner", "unknown", "hello")
	case "unknown command":
		args = []string{"unknown"}
	default:
		return fmt.Errorf("unknown invalid configuration %q", problem)
	}
	c.execute(token, args...)
	return nil
}
