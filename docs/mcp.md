# Read-only handoff context over MCP

MCP exposes `read_role(anchorSeq)` and `read_tape_view(anchorSeq)` through the
official Go MCP SDK's stateless Streamable HTTP transport. It shares one
`pkg/flow/borrow.Service` with the peer borrowing adapter. MCP does not select
Tape ranges, open storage, issue leases, or maintain another authorization store.
See the approved [shared View design](designs/shared-view-design.md).

## Hosting and ownership

The host initializes the Agent's context storage and a separate grant sidecar,
creates `borrow.NewJournal` and a `grant.New` handler backed by that Journal,
and registers the handler in native `tape.Tape.Registry`.
See the complete [lender setup](a2a-adapter.md#borrowing-lender-setup).

```go
// contextTape has initialized source storage and the registered grant handler.
borrowing, err := borrow.New(
    borrow.Config{
        Source:  a.Source(),
        Tape:    contextTape,
        Views:   a,
        Journal: journal,
        Grants:  grants,
    },
)
// Handle err before constructing either adapter.
contextService, err := mcp.NewService(
    mcp.Config{
        Borrowing: borrowing,
        Roles:     roleProvider,
        Access:    accessProvider,
    },
)
// Handle err before mounting contextService.Handler().
peerHandler, err := a2a.NewViewHandler(borrowing, authenticatePeer)
// Handle err before mounting the Connect and REST routes.
```

`borrow.JournalConfig.Storage` borrows initialized sidecar storage. Its file contains
authorization state; it is not wrapped as a context Tape. The host owns opening,
initialization and closing for both storage instances. Drain both HTTP adapters
before closing them. MCP, the borrowing service and the handler own none of
these lifetimes. Both bbolt and JSONL sidecar storage are supported.

## Ref, Lease, and Quote

The common protobuf values live in `gen/anra/model/view/v1`:

- `Ref` contains only `Source` and nonempty `Scopes`. Sequences are positive
  canonical decimal strings of arbitrary precision. Scopes are half-open,
  ordered, and non-overlapping; the native committed-view resolver enforces
  exact eligible windows. There is no separate snapshot or finalized sequence
  field: the view's end is the last scope's `seqE`.
- `Lease` binds its public `id`, authenticated `caller`, immutable `ref`, and
  `expiresAt`. A reference alone grants no access. Borrow retries do not renew
  leases. Empty and freely selected uncommitted ranges cannot receive a lease.
- `view.Quote` contains the borrowed channel, provenance, and public entries.
  Provenance is `{caller, leaseId, ref}`. Entries do not repeat it individually.

The controller requests an exact committed Ref through the shared service's
`Borrow(ctx, caller, request)` operation, supplying it as `BorrowRequest.View`.
Every new request follows the configured `Review` policy, just like peer
borrowing. A granted decision supplies the lease; denied and pending decisions
do not. There is no separate preapproved lease issuance operation. With no
`Review` configured, eligible committed views receive a one-hour lease.
Recorded requests replay their original decision without approval or renewal.

Handoff describes context transfer; it does not itself authorize a read. The host
resolves credentials and handoff references to an existing lease and role revision.
The lease ID is public; it is never the bearer credential.

`AccessProvider` owns credential policy and handoff resolution. `Authenticate`
checks validity, expiry and revocation on every HTTP request, including discovery.
`Authorize(ctx, credential, anchorSeq, tool)` rechecks current credential validity
and the exact anchor and tool permission, then returns a validated, immutable
`ReadTarget` containing only Caller, LeaseID and Role. Unknown, expired or revoked
credentials and unauthorized targets return `ErrPermissionDenied`; other errors
indicate backend unavailability. MCP defines no separate Grant, permission list
or credential lifetime. The shared borrowing flow checks current lease ownership,
expiry and termination before reading any content.

`RoleProvider` publishes complete immutable role revisions. A missing revision
returns `ErrInvalidReference`; other failures become `unavailable`. Providers
own definition validation and must support concurrent calls. Tape lease
termination does not remove independently valid role-tool permissions.

## Tool results

Both tools accept only `{"anchorSeq":"21"}`. Unknown fields and JSON decoding
errors return `invalid_reference`. The advertised schema describes positive
canonical decimal strings; the host authorizes the exact anchor and tool through
`AccessProvider.Authorize`. An unauthorized anchor returns `permission_denied`.

`read_role` returns the bound typed RoleDefinition/Profile revision. It never
selects a newer role definition. `read_tape_view` calls
`borrowing.ReadAll(ctx, target.Caller, target.LeaseID)` and returns the shared Quote:

```json
{
  "channel": "borrowed",
  "provenance": {
    "caller": "external-runtime",
    "leaseId": "public-lease-id",
    "ref": {
      "source": {"agentId": "vesta", "tapeId": "incarnation-id"},
      "scopes": [{"seqS": "3", "seqE": "5"}, {"seqS": "9", "seqE": "11"}]
    }
  },
  "entries": []
}
```

A nonempty view returns all authorized entries; the example omits entry bodies.
Each entry carries `seq`, `kind`, `timestamp`, `mediaType`, and its public
structured `content`. Results contain the same JSON in MCP text content and
structuredContent. Reads do not call Borrow, renew leases, select the current
Tape end, or enter reasoning Admission.

MCP and peer share the public exporter. It preserves multimodal Part order,
authorized thought text and markers, and opaque tool JSON number lexemes.
Empty Parts entries remain present. Execution state, arbitrary extensions,
Part metadata and provider thought signatures are excluded without changing
stored ADK events. Native anchors use `BuiltIn.Export()` without OwnerEpoch;
unknown anchor schemas yield `null` content while preserving the entry sequence.
Unsupported or undecodable content fails the whole read.
There is no Summary or intermediate Projection model.

Peer pagination traverses each scope in order and skips unauthorized gaps.
Source, caller, lease and complete Ref stay fixed across every page. A missing
entry or failed segment is an error, not a successful partial Quote. MCP returns
the complete Quote. Neither adapter imposes fixed request or result body caps;
MCP explicitly disables the SDK's default request-body cap.

## Authentication and public errors

Origin restrictions run before Bearer authentication. When TrustedOrigins is
configured, a supplied Origin must be one exact allowlisted value; absent
Origin remains usable by native clients. Every HTTP request, including tool
discovery, requires a credential. Repeated Authorization fields always fail,
regardless of order or values. A single field accepts case-insensitive Bearer
and multiple ASCII spaces; no token68 character restriction is added.
Successful authentication places the credential in that request's context only.
Each tool call asks the host to authorize its target again; HTTP authentication
alone does not authorize context access.

Authentication failure is HTTP 401 with a Bearer challenge and the public
permission_denied error envelope. Authentication backend failure is HTTP 503
with unavailable; rejected Origin is HTTP 403. All responses enforce no-store.
Tool failures use the same static `{error: {code, message}}` object in both
result forms and set isError. Unknown, foreign, expired, released or revoked
leases produce permission_denied at tool read; storage failures become
unavailable and unsupported content becomes failed_precondition. Credentials,
private references and storage diagnostics are never embedded in these errors.

Remote hosts terminate TLS and retain the SDK's Host/DNS rebinding protection.
The backend rejects only duplicate Authorization fields it actually receives;
proxies must also enforce rejection before merging or discarding fields.
Borrowed channel/provenance metadata expresses consumption policy. Native
consumers persist Quote in a read-only borrowed_context tool result and keep
actual tool execution permissions separate; the metadata does not guarantee
that arbitrary models ignore every prompt injection.

This model change has no historical storage or protocol compatibility path.
