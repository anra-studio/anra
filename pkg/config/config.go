// Package config loads and validates startup settings independently of any UI.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// ErrInvalidAgentID is returned for an identity that cannot safely name an agent.
var ErrInvalidAgentID = errors.New("agent name must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")

var agentID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// File is the TOML configuration shared by all application shells.
type File struct {
	path   string
	Lobby  Lobby            `toml:"lobby"`
	Agents map[string]Agent `toml:"agents"`
}

// Lobby identifies the shared Agent Card directory.
type Lobby struct {
	Address string `toml:"address"`
}

// Agent is one [agents.<name>] table. DataDir, Provider, Timeout, and
// Endpoint are required; credentials come from the environment, never the file.
type Agent struct {
	DataDir  string `toml:"data-dir"`
	Provider string `toml:"provider"`
	Model    string `toml:"model"`
	Timeout  string `toml:"timeout"`
	Role     string `toml:"role"`
	Desc     string `toml:"desc"`
	Endpoint string `toml:"endpoint"`
}

// Settings is a resolved startup snapshot. DataDir is relative to the config
// file when loaded, and Timeout is parsed and validated.
type Settings struct {
	ID       string
	Registry string
	Timeout  time.Duration
	DataDir  string
	Provider string
	Model    string
	Role     string
	Desc     string
	Endpoint string
}

// Load strictly decodes a TOML file. Agent resolves and validates the selected
// entry; changing the file does not mutate settings already returned.
func Load(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("config path is required")
	}
	config := File{path: path}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	defer file.Close()
	if err := toml.NewDecoder(file).DisallowUnknownFields().Decode(&config); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	for id := range config.Agents {
		if !agentID.MatchString(id) {
			return nil, fmt.Errorf("config %q has invalid agent name %q", path, id)
		}
	}
	return &config, nil
}

// Agent resolves one named agent without opening storage or contacting services.
func (config *File) Agent(id string) (Settings, error) {
	path := config.path
	if len(config.Agents) == 0 {
		return Settings{}, fmt.Errorf("config %q must define at least one agent", path)
	}
	if config.Lobby.Address == "" {
		return Settings{}, fmt.Errorf("config %q must set [lobby].address: agents find peers only through the lobby", path)
	}
	if !agentID.MatchString(id) {
		return Settings{}, ErrInvalidAgentID
	}
	entry, ok := config.Agents[id]
	if !ok {
		return Settings{}, fmt.Errorf("agent %q is not configured in %q", id, path)
	}
	settings := Settings{
		ID: id, Registry: config.Lobby.Address,
		DataDir: entry.DataDir, Provider: entry.Provider, Model: entry.Model,
		Role: entry.Role, Desc: entry.Desc, Endpoint: entry.Endpoint,
	}
	if strings.TrimSpace(settings.DataDir) == "" {
		return Settings{}, fmt.Errorf("agent %q data-dir is required", id)
	}
	if strings.TrimSpace(settings.Provider) == "" {
		return Settings{}, fmt.Errorf("agent %q provider is required", id)
	}
	if strings.TrimSpace(entry.Timeout) == "" {
		return Settings{}, fmt.Errorf("agent %q timeout is required", id)
	}
	if strings.TrimSpace(settings.Endpoint) == "" {
		return Settings{}, fmt.Errorf("agent %q endpoint is required", id)
	}
	if !filepath.IsAbs(settings.DataDir) {
		settings.DataDir = filepath.Join(filepath.Dir(path), settings.DataDir)
	}
	timeout, err := time.ParseDuration(entry.Timeout)
	if err != nil {
		return Settings{}, fmt.Errorf("agent %q timeout: %w", id, err)
	}
	settings.Timeout = timeout
	if settings.Timeout <= 0 {
		return Settings{}, fmt.Errorf("agent %q timeout must be positive", id)
	}
	return settings, nil
}

// SystemInstruction is the configured role and description joined with a colon.
// Anra does not add further instructions.
func (settings Settings) SystemInstruction() string {
	var parts []string
	for _, part := range []string{settings.Role, settings.Desc} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ":")
}

// Validate checks resolved settings supplied directly by an application shell.
func (settings Settings) Validate() error {
	if !agentID.MatchString(settings.ID) {
		return ErrInvalidAgentID
	}
	for _, field := range []struct{ name, value string }{
		{"data-dir", settings.DataDir}, {"provider", settings.Provider}, {"endpoint", settings.Endpoint},
	} {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("agent %q %s is required", settings.ID, field.name)
		}
	}
	if strings.TrimSpace(settings.Registry) == "" {
		return errors.New("lobby address is required")
	}
	if settings.Timeout <= 0 {
		return fmt.Errorf("agent %q timeout must be positive", settings.ID)
	}
	return nil
}
