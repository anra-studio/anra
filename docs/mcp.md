# Read-only handoff context over MCP

`pkg/mcp` implements the [Anra MCP design](https://github.com/anra-studio/anra/blob/d7b0a38d0e9d7afd29a763e71679814c6e2b76c8/docs/designs/anra-mcp-spec.md)
tracked by [Issue #13](https://github.com/anra-studio/anra/issues/13). The design
currently lives on the documentation branch. This implementation is based on
`main` and does not change the Peer or CLI endpoints.

The service uses the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
for protocol negotiation, JSON-RPC, and stateless Streamable HTTP with JSON
responses. Mount `Service.Handler()` at the embedding runtime's `/mcp` endpoint.
Every HTTP request, including discovery, requires `Authorization: Bearer …`.
After Origin checks, the HTTP authentication middleware rejects multiple
Authorization fields with HTTP 401 and a Bearer challenge, regardless of their
order or values. A single
field keeps case-insensitive scheme matching and accepts multiple ASCII spaces
after the scheme. The trimmed credential is passed to the external
`GrantVerifier`; no token68 character restriction is imposed. Only a successful
authentication adds the Grant to the current request context and dispatches to
the protected MCP transport.
Remote deployments terminate TLS. When configured, the handler enforces the
Origin allowlist on every method; the SDK's localhost Host/DNS rebinding check
also remains enabled.
MCP session identifiers, role names, and claimed external session
IDs never establish identity.

`Config.TrustedOrigins` is an exact allowlist of serialized HTTP(S) origins,
for example `[]string{"https://console.example"}`. When unset or empty
(`len(TrustedOrigins) == 0`), the service does not restrict Origin; requests
carrying Origin are allowed with valid Bearer credentials. A nonempty allowlist
requires requests carrying Origin to contain exactly one value matching an
entry. Built-in validator tags check HTTP(S) URLs and reject wildcards once at
startup. With an allowlist configured, nonmatching, empty or repeated Origin
headers fail with HTTP 403, the static `permission_denied` envelope and
`Cache-Control: no-store`. Matching is byte-for-byte: configuration and request
values are never parsed or normalized for comparison. Use the browser's
serialized scheme, host and port. A trailing slash, path, credentials, query,
fragment or differently cased host in configuration does not match that Origin.
Requests without Origin remain usable by native clients in both modes. Every
request still requires valid Bearer credentials; Origin validation is independent
of Bearer authentication.

The embedding runtime owns the listener, TLS, and browser trust configuration.
Local deployments should bind loopback. Configure browser origins explicitly
from trusted deployment settings. The handler never derives trust from Host,
Forwarded or X-Forwarded-* headers. Reverse proxies must preserve Origin and
Authorization, reject untrusted hostnames, prevent direct access to the backend,
and terminate TLS securely. Do not strip Origin to bypass this check. Origin
is checked before authentication. The backend can reject only duplicate fields
that reach it: a proxy that merges or drops Authorization fields can hide the
original multiplicity. Enforce duplicate rejection at that proxy as well.
Origin trust does not grant CORS access or replace connection authentication. See the
[MCP transport security requirements](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#security-warning).

## Read-only dependencies and lifecycle

MCP is a stateless reader. It opens no database, creates no files, writes no
Tape entries, and owns no dependency lifecycle. `Service` has no close method.
The embedding runtime owns the listener and HTTP shutdown, Tape initialization
and recovery, Role publication, and credential issuance and revocation. Stop
HTTP requests before closing the dependencies; drain the Agent before closing
its Tape. No MCP state is recovered or copied when constructing a new service.

`Config.Tape` accepts `TapeReader`, the `Range` portion of tape-go's existing
`storage.EntryStorage`. Both bbolt and JSONL Tape implementations satisfy it.
MCP cannot call `Store` through this interface. The caller supplies the matching
Source and owner. Tape is the sole source of truth for handoff anchors and their
fixed views; the initialization anchor need not be the first entry.

Two required read-only interfaces supply the remaining data:

- `Config.Roles` accepts `RoleProvider.GetRole(ctx, RoleRef)`. The provider returns
  the `RoleDefinition` for exactly `(id, version)`, with stable contents for that
  revision. A `Profile` holds the role's purpose, instructions and the tools it is
  proficient with, each with its required permissions; `Profile.String()` renders
  them into the instructions. MCP validates the definition with its validator tags
  and reports an invalid one as `failed_precondition`. The provider returns
  `ErrInvalidReference` for a missing revision, which MCP reports as
  `invalid_reference`; other provider errors become `unavailable`. Encoded
  results exceeding `MaxResponseBytes` fail with `failed_precondition`.
- `Config.Grants` accepts `GrantVerifier.VerifyGrant(ctx, credential)`. The
  embedding runtime verifies credentials using its own authorization component,
  existing journal, or an in-memory map. Unknown or revoked credentials return
  `(nil, nil)` or `ErrPermissionDenied`; other errors become `unavailable`.
  Verification runs on every HTTP request, including discovery. `GrantVerifier`
  owns expiry, revocation, Role and Source checks; MCP checks the exact
  anchor sequence and tool permission on tool invocation.

`Grant.AnchorSeq` and tool input use `required,number,startsnotwith=0`: a nonzero
ASCII decimal string without leading zeros, with no integer-width limit. The
handler validates tool input explicitly; the SDK's low-level `AddTool` does not
enforce its advertised input schema. Independent validators need no private MCP
rule registrations for the exported types.

Both providers must support concurrent calls and return values that remain
immutable for the duration of the request. MCP does not cache Role revisions or
grants. Provider implementations own retention and revocation across service
restarts. An in-memory provider survives MCP reconstruction when retained by
the embedding runtime; it does not imply persistence across process restarts.

For example, given an existing Tape, Role provider and credential verifier:

```go
service, err := mcp.NewService(mcp.Config{
    Source: a.Source(), Owner: "vesta", Tape: a.Tape(),
    Roles: roleProvider, Grants: grantVerifier,
})
if err != nil {
    return err
}
mux.Handle("/mcp", service.Handler())
```

Before dispatching external work, the controller publishes the Role revision
through its existing Role owner, appends a handoff anchor (for example
`anra.dev/anchor/intr/v1`, which adds role and binding) through Tape, and
arranges the matching credential with its authorization component. A grant is
`{role, anchorSeq, view, permissions, expiresAt}`: the view is the Tape range
`seqS → seqE` the handoff committed. The `GrantVerifier` guarantees these
values are exactly those the anchor committed; MCP never parses the anchor
summary. Credential delivery belongs to the integration. Never put credentials
in prompts, Tape entries, artifacts, or logs. MCP provides no publication,
commit, issuance or revocation methods.

On each tool read MCP checks that the request's `anchorSeq` equals the grant's
and that the grant carries the tool's permission. It returns the granted Role
revision or reads the granted view, like a borrow: the caller receives the
range and reads the entries itself. Tape integrity and immutability belong to
Tape and the grant issuer, not MCP. Reads never acquire reasoning Admission or
select the current Tape head, so later entries cannot expand the view.

## Tool contract

Both tools accept exactly `{"anchorSeq":"21"}`. Sequences are positive canonical
decimal strings of arbitrary precision. Unknown arguments, including role,
session, and scope overrides, fail validation. Results contain identical JSON
in MCP `structuredContent` and the text `content` item.

`read_role` returns the revision referenced by the handoff, never the current
configuration:

```json
{"id":"vesta","version":"role-revision-42","profile":{"purpose":"Maintain Anra","instructions":"Help maintain Anra","tools":[{"name":"read_files","permissions":["read"]}]}}
```

`read_tape_view` returns `{"view": …, "entries": […]}`: the granted view in
protojson form and the entries in its range. Entries contain `seq`, `kind`,
`timestamp`, and and `content`. Plain text content is a JSON
string; supported ADK content is a JSON object retaining structured function
calls and responses. `pkg/model/projection` projects Tape entries into public
model events, shared with A2A. Execution metadata, owners, extensions and
private thought parts are excluded; thought-only entries are skipped.

Anchors are part of the view because they record decision and interruption
points. Native Anra anchors are exported as their `anchor.Record` (schema, type,
turn ID, base view, start sequence, outcome) without the private owner epoch. Other anchors, including integration handoffs,
are exported with their summary verbatim as `content`, so handoff payloads must
not contain information the grantee may not read. Unsupported entry kinds fail
with `failed_precondition`.

`MaxResponseBytes` defaults to 1 MiB (configurable from 1 KiB to 16 MiB) and
bounds the encoded role and view results. Each result repeats its encoding as
text and structured JSON. Requests are capped at 16 KiB. Limits fail
atomically, with no truncation. Responses enforce `Cache-Control: no-store`.

## Errors

Authenticated tool failures set `isError: true` and carry
`{"error":{"code":"…","message":"…"}}` in both result forms. Messages are
static and never include private references, paths, credentials, or storage
diagnostics.

| Code | Meaning |
| --- | --- |
| `permission_denied` | Wrong anchor, missing tool permission, or missing/expired/revoked credential |
| `invalid_reference` | Malformed arguments or missing Role revision |
| `failed_precondition` | Unsupported entry or exceeded limits |
| `unavailable` | Tape read failure (including undecodable stored entries) or provider failure |

Authentication failures return HTTP 401 with the generic `permission_denied`
envelope and a Bearer challenge. Storage failures during authentication return
HTTP 503 with `unavailable`. Cross-anchor requests are denied before resolving
the target, so nonexistent and private targets remain indistinguishable.
MCP protocol errors, such as unknown methods or tools, use standard SDK JSON-RPC
errors.

The executable acceptance contract is [mcp.feature](../tests/e2e/features/mcp.feature).
Run `mise run e2e` during development and `mise run test` for all repository checks.
Both commands run the BDD suite with the race detector enabled.
