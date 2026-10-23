package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

type fileConfig struct {
	DefaultAgent string                 `toml:"default-agent"`
	Defaults     agentConfig            `toml:"defaults"`
	Agents       map[string]agentConfig `toml:"agents"`
}

// Pointers distinguish an omitted setting from an explicitly empty value.
// Keys mirror the command flags; credentials continue to come from the environment.
type agentConfig struct {
	DataDir *string `toml:"data-dir"`
	Session *string `toml:"session"`
	Runner  *string `toml:"runner"`
	Model   *string `toml:"model"`
	System  *string `toml:"system"`
	Listen  *string `toml:"listen"`
	Peer    *string `toml:"peer"`
	Timeout *string `toml:"timeout"`
}

func (c agentConfig) settings() map[string]*string {
	return map[string]*string{
		"data-dir": c.DataDir, "session": c.Session, "runner": c.Runner,
		"model": c.Model, "system": c.System, "listen": c.Listen,
		"peer": c.Peer, "timeout": c.Timeout,
	}
}

// applyConfig runs after Cobra parses explicit flags and before argument
// execution or required-flag validation. Applying settings through Cobra keeps
// flag parsing, validation, and command option bindings in one place.
func applyConfig(command *cobra.Command, path string) error {
	if path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read config %q: %w", path, err)
	}
	defer file.Close()
	var config fileConfig
	if err := toml.NewDecoder(file).DisallowUnknownFields().Decode(&config); err != nil {
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	if len(config.Agents) == 0 {
		return fmt.Errorf("config %q must define at least one agent", path)
	}
	for id := range config.Agents {
		if !agentID.MatchString(id) {
			return fmt.Errorf("config %q has invalid agent name %q", path, id)
		}
	}
	if config.DefaultAgent != "" {
		if _, ok := config.Agents[config.DefaultAgent]; !ok {
			return fmt.Errorf("config %q: default-agent %q is not configured", path, config.DefaultAgent)
		}
		if !command.Flag("agent").Changed {
			if err := command.Flags().Set("agent", config.DefaultAgent); err != nil {
				return err
			}
		}
	}
	id := command.Flag("agent").Value.String()
	selected, ok := config.Agents[id]
	if !ok {
		return fmt.Errorf("agent %q is not configured in %q", id, path)
	}
	settings := config.Defaults.settings()
	for name, value := range selected.settings() {
		if value != nil {
			settings[name] = value
		}
	}
	names := make([]string, 0, len(settings))
	for name := range settings {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		value := settings[name]
		flag := command.Flag(name)
		if value == nil || flag == nil || flag.Changed {
			continue
		}
		text := *value
		if name == "data-dir" && text != "" && !filepath.IsAbs(text) {
			text = filepath.Join(filepath.Dir(path), text)
		}
		if err := command.Flags().Set(name, text); err != nil {
			return fmt.Errorf("config %q, agent %q, %s: %w", path, id, name, err)
		}
	}
	return nil
}
