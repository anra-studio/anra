package cmd

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/scbizu/anra/pkg/a2a"
	"github.com/spf13/viper"
)

func peerToken(settings *viper.Viper) (string, error) {
	token := settings.GetString("peer-token")
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return "", errors.New("ANRA_PEER_TOKEN must contain a non-empty bearer token")
	}
	return token, nil
}

func newPeerClient(ctx context.Context, base string, settings *viper.Viper) (*a2a.MessageClient, error) {
	token, err := peerToken(settings)
	if err != nil {
		return nil, err
	}
	return a2a.NewMessageClient(ctx, peerHTTP(token), base)
}

func peerHTTP(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type bearerTransport struct{ token string }

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(clone)
}
