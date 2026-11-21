package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeFileRejectsInvalidAgentTables(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"malformed TOML": "[agents.alice",
		"wrong field type": `[agents.alice]
provider = 123`,
		"invalid agent ID": `[agents."../outside"]`,
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := decodeFile("team.toml", strings.NewReader(input)); got != nil || err == nil {
				t.Fatalf("decoded invalid TOML: %+v, %v", got, err)
			}
		})
	}
}

func TestResolveAgentSettings(t *testing.T) {
	t.Parallel()

	valid := Agent{
		DataDir:  "tapes",
		Provider: "openai",
		Timeout:  "2s",
		Endpoint: "127.0.0.1:0",
	}

	file := File{
		path: "config/team.toml",
		Lobby: Lobby{
			Address: "http://127.0.0.1:8080",
		},
		Agents: map[string]Agent{"alice": valid},
	}
	settings, err := file.Agent("alice")
	if err != nil {
		t.Fatal(err)
	}
	if settings.DataDir != "config/tapes" || settings.Timeout != 2*time.Second {
		t.Fatalf("resolved settings = %+v", settings)
	}
	if _, err := file.Agent("../alice"); !errors.Is(err, ErrInvalidAgentID) {
		t.Errorf("invalid selected ID returned %v", err)
	}
	if _, err := file.Agent("bob"); err == nil || !strings.Contains(err.Error(), "is not configured") {
		t.Errorf("unknown selected ID returned %v", err)
	}

	withoutLobby := file
	withoutLobby.Lobby = Lobby{}
	if _, err := withoutLobby.Agent("alice"); err == nil || !strings.Contains(err.Error(), "must set [lobby]") {
		t.Errorf("missing lobby returned %v", err)
	}

	for _, tc := range []struct {
		field *string
		value string
		want  string
	}{
		{
			field: &valid.DataDir,
			value: " ",
			want:  "data-dir is required",
		},
		{
			field: &valid.Provider,
			value: " ",
			want:  "provider is required",
		},
		{
			field: &valid.Endpoint,
			value: " ",
			want:  "endpoint is required",
		},
		{
			field: &valid.Timeout,
			value: " ",
			want:  "timeout is required",
		},
		{
			field: &valid.Timeout,
			value: "soon",
			want:  "invalid duration",
		},
		{
			field: &valid.Timeout,
			value: "0s",
			want:  "timeout must be positive",
		},
	} {
		t.Run(tc.want, func(t *testing.T) {
			original := *tc.field
			*tc.field = tc.value
			file.Agents["alice"] = valid
			*tc.field = original

			got, err := file.Agent("alice")
			if got != (Settings{}) || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resolved invalid settings: %+v, %v; want %q", got, err, tc.want)
			}
		})
	}
}
