package main

import (
	"errors"

	"github.com/scbizu/anra/pkg/config"
	"github.com/spf13/cobra"
)

func loadSettings(command *cobra.Command) (config.Settings, error) {
	path, err := command.Flags().GetString("config")
	if err != nil {
		return config.Settings{}, err
	}
	if path == "" {
		return config.Settings{}, errors.New("--config is required")
	}
	file, err := config.Load(path)
	if err != nil {
		return config.Settings{}, err
	}
	id, err := command.Flags().GetString("agent")
	if err != nil {
		return config.Settings{}, err
	}
	settings, err := file.Agent(id)
	if errors.Is(err, config.ErrInvalidAgentID) {
		return config.Settings{}, errors.New("--agent must start with a letter or digit and contain only letters, digits, '.', '_' or '-'")
	}
	return settings, err
}
