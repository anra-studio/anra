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

// AccessProvider owns credential verification and tool authorization. Methods
// must support concurrent calls. Unknown, expired or revoked credentials and
// unauthorized targets return ErrPermissionDenied; other errors indicate backend
// unavailability. Neither method issues or renews a view lease.
type AccessProvider interface {
	// Authenticate checks credential validity on every HTTP request, including
	// initialization and discovery requests that name no tool or handoff.
	Authenticate(ctx context.Context, credential string) error

	// Authorize checks current credential validity, the exact handoff anchor and
	// tool permission, returning a non-nil, validated, immutable target on success.
	// The host resolves the handoff's binding; tool arguments cannot choose the
	// caller, lease or role revision. The shared borrowing flow checks lease
	// ownership, expiry and termination on every content read. Role authorization
	// remains independent of the view lease's lifetime.
	Authorize(ctx context.Context, credential, anchorSeq, tool string) (*ReadTarget, error)
}
