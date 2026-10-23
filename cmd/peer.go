package cmd

import (
	"errors"
	"strings"

	"github.com/spf13/viper"
)

func peerToken(settings *viper.Viper) (string, error) {
	token := settings.GetString("peer-token")
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("ANRA_PEER_TOKEN must contain a non-empty bearer token")
	}
	return token, nil
}
