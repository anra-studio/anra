package mcp

import "context"

// RoleProvider reads complete, valid JSON objects by immutable revision.
// Implementations must support concurrent calls and keep each revision stable.
// Return (nil, nil) for an absent revision, or an error when the provider is
// unavailable. Returned definitions must match the requested revision and
// remain valid and immutable for the duration of the request.
type RoleProvider interface {
	GetRole(context.Context, RoleRef) (*RoleDefinition, error)
}

// GrantVerifier validates a bearer credential using caller-owned authorization.
// Implementations must support concurrent calls and check revocation on every
// call. Return (nil, nil) or ErrPermissionDenied for unknown or revoked credentials;
// other errors indicate unavailability. Returned grants must remain valid and
// immutable for the duration of the request. MCP also validates expiry, Source,
// permissions, and the exact Tape handoff binding; it never issues credentials.
type GrantVerifier interface {
	VerifyGrant(ctx context.Context, credential string) (*Grant, error)
}
