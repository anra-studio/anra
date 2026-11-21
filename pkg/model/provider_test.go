package model

import (
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeOpenAIBaseURLKeepsGatewayPath(t *testing.T) {
	t.Parallel()

	const gateway = "https://example.test/gateway/v1"
	for _, base := range []string{gateway, gateway + "/", gateway + "/#fixture"} {
		t.Run(base, func(t *testing.T) {
			normalized, err := normalizeOpenAIBaseURL(base)
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := url.Parse(normalized)
			if err != nil {
				t.Fatal(err)
			}
			if endpoint.Path != "/gateway/v1/" {
				t.Fatalf("normalized base path = %q, want /gateway/v1/", endpoint.Path)
			}
		})
	}
}

func TestOpenAIModelStartupSettings(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	if _, err := New("openai", ""); err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("missing key returned %v", err)
	}
	if _, err := New("unsupported", ""); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unknown provider returned %v", err)
	}

	t.Setenv("OPENAI_API_KEY", "fixture-key")
	t.Setenv("OPENAI_BASE_URL", "http://[invalid")
	if _, err := New("openai", ""); err == nil || !strings.Contains(err.Error(), "OPENAI_BASE_URL") {
		t.Errorf("malformed base URL returned %v", err)
	}

	t.Setenv("OPENAI_BASE_URL", "")
	model, err := New("openai", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Name(); got != "gpt-4o-mini" {
		t.Errorf("default model = %q, want gpt-4o-mini", got)
	}
}
