package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/scbizu/tape-go/pkg/provider/ds"
	"github.com/spf13/cobra"
	"google.golang.org/adk/model"
)

type modelOptions struct {
	provider, model, system, apiBase string
}

func (o *modelOptions) flags(command *cobra.Command) {
	command.Flags().StringVar(&o.provider, "provider", "deepseek", "model provider")
	command.Flags().StringVar(&o.model, "model", "", "model name (empty uses the provider default)")
	command.Flags().StringVar(&o.system, "system", "", "system instruction for the agent")
	command.Flags().StringVar(&o.apiBase, "api-base", "", "provider API base URL override")
}

func newModel(o modelOptions, key string) (model.LLM, error) {
	if o.provider != "deepseek" {
		return nil, fmt.Errorf("unknown provider %q (use deepseek)", o.provider)
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("DEEPSEEK_API_KEY is required for provider deepseek")
	}
	var opts []deepseek.Option
	if o.apiBase != "" {
		u, err := url.Parse(o.apiBase)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("--api-base must be an HTTP(S) provider URL")
		}
		opts = append(opts, deepseek.WithBaseURL(strings.TrimRight(o.apiBase, "/")+"/"))
	}
	return ds.NewModel(key, o.model, opts...)
}
