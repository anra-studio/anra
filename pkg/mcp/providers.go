package mcp

import "context"

// RoleProvider reads complete, valid JSON objects by immutable revision.
// Implementations must support concurrent calls and keep each revision stable.
// Return ErrInvalidReference for an absent revision; other errors indicate
// the provider is unavailable. A nil error requires a non-nil definition. Returned definitions must match the requested revision and
// remain valid and immutable for the duration of the request.
type RoleProvider interface {
	GetRole(context.Context, RoleRef) (*RoleDefinition, error)
}

// GrantVerifier validates a bearer credential using caller-owned authorization.
// Implementations must support concurrent calls and check revocation on every
// call. Return ErrPermissionDenied for unknown, revoked or expired credentials;
// other errors indicate unavailability. A nil error requires a Grant that is
// valid and immutable for the request and whose Role, AnchorSeq and Window
// (a valid TapeView of this service's Source) are exactly those committed by
// the handoff anchor at AnchorSeq. MCP only checks AnchorSeq and permission;
// it never issues credentials or reads the handoff.
type GrantVerifier interface {
	VerifyGrant(ctx context.Context, credential string) (*Grant, error)
}
