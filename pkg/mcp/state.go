package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	pb "github.com/anra-studio/anra/gen/anra/a2a/tape/v1"
)

// ErrStateNotFound reports an absent role revision or credential digest.
var ErrStateNotFound = errors.New("missing MCP state")

// GrantRecord is the durable authorization state for one credential digest.
type GrantRecord struct {
	Grant   Grant `json:"grant"`
	Revoked bool  `json:"revoked"`
}

// StateStore binds immutable role revisions and revocable credentials to one
// Source. Implementations must support concurrent requests. Missing records
// return ErrStateNotFound; other storage failures return an error.
//
// SaveRole and SaveGrant must insert atomically: identical contents are
// idempotent, conflicting contents return ErrFailedPrecondition. SaveGrant
// must never replace or revive a revoked record. RevokeGrant
// atomically marks an existing grant revoked and is idempotent. Credential
// keys are SHA-256 digests; implementations never receive the bearer token.
type StateStore interface {
	Source() *pb.Source
	SaveRole(context.Context, RoleDefinition) error
	Role(context.Context, RoleRef) (RoleDefinition, error)
	SaveGrant(ctx context.Context, digest string, grant Grant) error
	Grant(ctx context.Context, digest string) (GrantRecord, error)
	RevokeGrant(ctx context.Context, digest string) error
	Close() error
}

func tokenHash(token string) string {
	d := sha256.Sum256([]byte(token))
	return hex.EncodeToString(d[:])
}
