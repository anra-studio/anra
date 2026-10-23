package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tapeagent "github.com/scbizu/tape-go/pkg/agent"
	"github.com/scbizu/tape-go/pkg/provider/ds"
	"github.com/scbizu/tape-go/pkg/runner"
	"github.com/spf13/cobra"
)

type modelOptions struct {
	runner, model, system string
}

func (o *modelOptions) flags(command *cobra.Command) {
	command.Flags().StringVar(&o.runner, "runner", "deepseek", "model runner: deepseek or echo")
	command.Flags().StringVar(&o.model, "model", "", "DeepSeek model (empty uses tape-go's default)")
	command.Flags().StringVar(&o.system, "system", "", "system instruction for the model")
}

func newRunner(agent agentOptions, o modelOptions, key string) (runner.Runner, error) {
	switch o.runner {
	case "echo":
		return echoRunner{id: agent.id}, nil
	case "deepseek":
		if strings.TrimSpace(key) == "" {
			return nil, errors.New("DEEPSEEK_API_KEY is required for --runner deepseek; use --runner echo for a local demo")
		}
		model, err := ds.NewModel(key, o.model)
		if err != nil {
			return nil, err
		}
		return tapeagent.NewADKRunner(model), nil
	default:
		return nil, fmt.Errorf("unknown runner %q (use deepseek or echo)", o.runner)
	}
}

// echoRunner is an explicit no-model demo, using the same tape runtime.
type echoRunner struct{ id string }

func (r echoRunner) RunTurn(_ context.Context, req runner.Request) (runner.Response, error) {
	return runner.Response{Output: r.id + ": " + req.Messages[len(req.Messages)-1].Text}, nil
}
