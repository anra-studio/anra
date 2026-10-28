package discovery

import (
	"errors"
	"fmt"
	"net/url"
)

// BaseURL validates the card location and its advertised messaging origin.
func BaseURL(value Record) (string, error) {
	u, err := url.Parse(value.CardURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/.well-known/agent-card.json" {
		return "", errors.New("discovery: invalid card URL")
	}
	if value.Card == nil || len(value.Card.SupportedInterfaces) == 0 {
		return "", errors.New("discovery: card has no interfaces")
	}
	for _, endpoint := range value.Card.SupportedInterfaces {
		if endpoint == nil {
			return "", errors.New("discovery: empty card interface")
		}
		remote, err := url.Parse(endpoint.URL)
		if err != nil || remote.Scheme != u.Scheme || remote.Host != u.Host || remote.User != nil {
			return "", fmt.Errorf("discovery: card interface is outside its origin")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}
