package main

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
)

func message(args []string) string { return strings.Join(args, " ") }

func messageArgs(_ *cobra.Command, args []string) error {
	if strings.TrimSpace(message(args)) == "" {
		return errors.New("a non-empty message is required")
	}
	return nil
}
