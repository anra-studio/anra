package cmd

import (
	"errors"
	"os"
	"strings"
)

func peerToken() (string, error) {
	token := os.Getenv("ANRA_PEER_TOKEN")
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("ANRA_PEER_TOKEN must contain a non-empty bearer token")
	}
	return token, nil
}
