// Package discovery provides the client and wire contract for the trusted Agent Card directory.
package discovery

import (
	"time"

	sdk "github.com/a2aproject/a2a-go/v2/a2a"
)

const AgentsPath = "/agents"

// Record is public directory information; the lease credential is never exposed.
type Record struct {
	Name      string         `json:"name"`
	CardURL   string         `json:"cardUrl"`
	Card      *sdk.AgentCard `json:"card"`
	ExpiresAt time.Time      `json:"expiresAt"`
}

type registration struct {
	Record
	Lease string `json:"lease"`
}

type leaseResponse struct {
	TTL time.Duration `json:"ttl"`
}
