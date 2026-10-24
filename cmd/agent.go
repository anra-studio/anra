package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/scbizu/anra/pkg/a2a"
	"github.com/scbizu/anra/pkg/agent"
	"github.com/scbizu/anra/pkg/discovery"
	"github.com/scbizu/tape-go/pkg/tape/owner"
	"github.com/scbizu/tape-go/pkg/tape/storage/bbolt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/adk/model"
)

var agentID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type agentOptions struct {
	id, dataDir, session string
}

func (o agentOptions) validate() error {
	if !agentID.MatchString(o.id) {
		return errors.New("--agent must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	if strings.TrimSpace(o.dataDir) == "" || strings.TrimSpace(o.session) == "" {
		return errors.New("--data-dir and --session must not be empty")
	}
	return nil
}

func messageArgs(_ *cobra.Command, args []string) error {
	if strings.TrimSpace(strings.Join(args, " ")) == "" {
		return errors.New("a non-empty message is required")
	}
	return nil
}

func validateTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	return nil
}

// withAgent owns tape initialization and closure for one command invocation.
func withAgent(ctx context.Context, common agentOptions, llm model.LLM, config modelOptions, settings *viper.Viper, execute func(*agent.Anra) error) (runErr error) {
	options := []agent.Option{agent.WithSystemInstruction("You are Anra agent " + common.id + ".\n" + config.system)}
	if registry := settings.GetString("registry"); registry != "" {
		directory, err := discovery.NewClient(registry)
		if err != nil {
			return err
		}
		options = append(options, agent.WithPeers(directory, a2a.TrustedHTTPClient(common.id)))
	}
	tape, err := bbolt.NewBboltStorage(common.session, filepath.Join(common.dataDir, common.id+".db"))
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, tape.Close()) }()
	if err := tape.Init(owner.WithOwnerId(ctx, common.id)); err != nil {
		return fmt.Errorf("open agent tape: %w", err)
	}
	if llm != nil {
		options = append(options, agent.WithLLM(llm))
	}
	a, err := agent.New(common.id, tape, options...)
	if err != nil {
		return err
	}
	return execute(a)
}

func printAnswer(ctx context.Context, a *agent.Anra, args []string, out io.Writer) error {
	answer, err := a.Run(ctx, strings.Join(args, " "))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, answer)
	return err
}
