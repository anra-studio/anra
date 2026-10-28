// Package model constructs the provider models used by Anra.
package model

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	deepseek "github.com/cohesion-org/deepseek-go"
	"github.com/scbizu/tape-go/pkg/provider/ds"
	adkmodel "google.golang.org/adk/model"
)

// New initializes a tape-go model for the agent runtime. DeepSeek credentials
// and the optional endpoint override come from DEEPSEEK_API_KEY and
// DEEPSEEK_API_BASE; an empty name uses tape-go's provider default.
func New(provider, name string) (adkmodel.LLM, error) {
	if provider != "deepseek" {
		return nil, fmt.Errorf("unknown provider %q (use deepseek)", provider)
	}
	key := os.Getenv("DEEPSEEK_API_KEY")
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("DEEPSEEK_API_KEY is required for provider deepseek")
	}
	var opts []deepseek.Option
	if apiBase := os.Getenv("DEEPSEEK_API_BASE"); apiBase != "" {
		u, err := url.Parse(apiBase)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("DEEPSEEK_API_BASE must be an HTTP(S) provider URL")
		}
		opts = append(opts, deepseek.WithBaseURL(strings.TrimRight(apiBase, "/")+"/"))
	}
	return ds.NewModel(key, name, opts...)
}
