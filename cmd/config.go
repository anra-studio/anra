package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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

// applyConfig resolves flags, environment, and the selected TOML profile through
// Viper, then applies the resolved values through Cobra's parsers. This also lets
// config or environment values satisfy required flags such as --peer.
func applyConfig(command *cobra.Command, settings *viper.Viper) error {
	if err := settings.BindPFlags(command.Flags()); err != nil {
		return err
	}
	names := []string{"config", "agent", "data-dir", "session", "runner", "model", "system", "listen", "peer", "timeout"}
	for _, name := range names {
		if command.Flag(name) != nil {
			settings.MustBindEnv(name)
		}
	}
	if path := settings.GetString("config"); path != "" {
		if err := loadAgentConfig(settings, path); err != nil {
			return err
		}
	}
	for _, name := range names {
		flag := command.Flag(name)
		if flag == nil || flag.Changed || !settings.IsSet(name) {
			continue
		}
		if err := command.Flags().Set(name, settings.GetString(name)); err != nil {
			return fmt.Errorf("setting %s: %w", name, err)
		}
	}
	return nil
}

func loadAgentConfig(settings *viper.Viper, path string) error {
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
		settings.SetDefault("agent", config.DefaultAgent)
	}
	id := settings.GetString("agent")
	selected, ok := config.Agents[id]
	if !ok {
		return fmt.Errorf("agent %q is not configured in %q", id, path)
	}
	merged := config.Defaults.settings()
	for name, value := range selected.settings() {
		if value != nil {
			merged[name] = value
		}
	}
	values := make(map[string]any)
	for name, value := range merged {
		if value == nil {
			continue
		}
		text := *value
		if name == "data-dir" && text != "" && !filepath.IsAbs(text) {
			text = filepath.Join(filepath.Dir(path), text)
		}
		values[name] = text
	}
	return settings.MergeConfigMap(values)
}
