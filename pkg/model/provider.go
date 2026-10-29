// Package model constructs the provider models used by Anra.
package model

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/option"
	"github.com/scbizu/tape-go/pkg/provider/openai"
	adkmodel "google.golang.org/adk/model"
)

// New initializes a tape-go model for the agent runtime. OpenAI-compatible credentials
// and the optional endpoint override come from OPENAI_API_KEY and
// OPENAI_BASE_URL; an empty name uses tape-go's provider default.
func New(provider, name string) (adkmodel.LLM, error) {
	if provider != "openai" {
		return nil, fmt.Errorf("unknown provider %q (use openai)", provider)
	}
	key := os.Getenv("OPENAI_API_KEY")
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("OPENAI_API_KEY is required for provider openai")
	}
	var opts []option.RequestOption
	if apiBase := os.Getenv("OPENAI_BASE_URL"); apiBase != "" {
		u, err := url.Parse(apiBase)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("OPENAI_BASE_URL must be an HTTP(S) provider URL")
		}
		opts = append(opts, option.WithBaseURL(strings.TrimRight(apiBase, "/")+"/"))
	}
	return openai.NewModel(key, name, opts...)
}
