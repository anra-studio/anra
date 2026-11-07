package mcp

import "context"

// RoleProvider reads complete role definitions by immutable revision.
// Implementations must support concurrent calls and keep each revision stable.
// Return ErrInvalidReference for an absent revision; other errors indicate
// the provider is unavailable. A nil error requires a non-nil definition that
// passes validation, matches the requested revision, and stays immutable for
// the duration of the request.
type RoleProvider interface {
	GetRole(context.Context, RoleRef) (*RoleDefinition, error)
}

// GrantVerifier validates a bearer credential using caller-owned authorization.
// Implementations support concurrent calls and check credential expiry and
// revocation on every request. Unknown or revoked credentials return
// ErrPermissionDenied; other errors indicate backend unavailability. A nil error
// requires a valid, non-nil Grant whose Caller, LeaseID, Role and AnchorSeq are
// exactly those committed by the handoff. The lease ID is public, never the
// credential. MCP checks the handoff and tool permission; the shared view service
// checks lease ownership, expiry and termination on every content read.
type GrantVerifier interface {
	VerifyGrant(ctx context.Context, credential string) (*Grant, error)
}
