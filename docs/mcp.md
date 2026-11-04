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
fixed windows; the initialization anchor need not be the first entry.

Two required read-only interfaces supply the remaining data:

- `Config.Roles` accepts `RoleProvider.GetRole(ctx, RoleRef)`. The provider returns
  the complete JSON object for exactly `(id, version)`, with stable contents for
  that revision. The provider owns JSON object validation. `RoleDefinition` uses
  only built-in validator tags: `required,json` checks presence and JSON syntax,
  not whether the top-level value is an object. MCP does not revalidate provider
  definitions. The provider returns `(nil, nil)`
  for a missing revision, which MCP reports as `invalid_reference`; provider
  errors become `unavailable`. Definitions exceeding `MaxResponseBytes` fail
  with `failed_precondition`.
- `Config.Grants` accepts `GrantVerifier.VerifyGrant(ctx, credential)`. The
  embedding runtime verifies credentials using its own authorization component,
  existing journal, or an in-memory map. Unknown or revoked credentials return
  `(nil, nil)` or `ErrPermissionDenied`; other errors become `unavailable`.
  Verification runs on every HTTP request, including discovery. MCP additionally
  validates expiry, permissions, Role ID and Source, then checks the exact
  handoff sequence, target binding and TapeView on tool invocation.

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
through its existing Role owner, appends an `anra.dev/anchor/intr/v1`
`integration.handoff` anchor through Tape, and arranges the matching credential
with its authorization component. A grant binds Role ID, handoff sequence,
target RoleBinding, Source, snapshot head, scope, explicit permissions
(`read_role`, `read_context_window`), and expiration. Credential delivery belongs
to the integration. Never put credentials in prompts, Tape entries, artifacts,
or logs. MCP provides no publication, commit, issuance or revocation methods.

On each tool read MCP loads the handoff directly from Tape and verifies it
against the grant. It returns the selected Role revision or projects the fixed
window. Every sequence in `[seqS, seqE)` and the excluded
`snapshotHead == seqE` boundary must already be persisted. Missing ranges,
unsupported entries and incomplete tool calls fail reconstruction atomically.
A persisted handoff requires no secondary commit record and can receive a grant
later without another Tape write.

An explicitly selected persisted prefix can be read during an active turn.
Reads never acquire reasoning Admission or select the current Tape head. Later
model output and observations cannot expand the window. Tape's append-only
contract owns immutability; MCP maintains no duplicate handoff records,
private buckets, or hashes of anchors, windows or Role definitions.

## Tool contract

Both tools accept exactly `{"anchorSeq":"21"}`. Sequences are positive canonical
decimal strings of arbitrary precision. Unknown arguments, including role,
session, and scope overrides, fail validation. Results contain identical JSON
in MCP `structuredContent` and the text `content` item.

`read_role` returns the revision referenced by the handoff, never the current
configuration:

```json
{"id":"vesta","version":"role-revision-42","definition":{"instructions":"Help maintain Anra"}}
```

`read_context_window` returns the original `view`, projected `entries`, and
`isComplete: true`. Entries contain `seq`, `kind`, `timestamp`, and either
`content` or `boundary`. Plain text content is a JSON string; supported ADK
content is a JSON object retaining structured function calls and responses.
The shared domain package `pkg/model/projection` projects Tape entries directly
into public model events, independently of A2A and MCP. A2A serializes those
events into its existing `ExportedEntry` payload; MCP checks tool call completion
in the typed parts before serializing its response, without a payload
decode round trip or a dependency on `pkg/a2a`. The `anra.context.v1` projection
uses explicit protocol structures and recursively rejects unknown visible
fields, including nested function response parts and media fields. Tool `args`
and `response` objects are opaque
JSON: unknown business keys, empty objects, large integers, decimal precision,
and exponent notation are retained without a float64 conversion. Execution
metadata outside `content` and private thought parts remain excluded.

Native boundaries retain schema, transition type, turn ID, start sequence, and
outcome. Integration boundaries retain explicit origin application, binding,
session, and event references. Storage owners, execution epochs, state deltas,
arbitrary extensions, delegated task prompts, and private thoughts are excluded.

The initial projection supports plain user/assistant text, supported ADK content,
native v1 boundaries, and integration v1 boundaries. Unknown kinds/schemas,
legacy runner codecs, malformed events, or unsupported visible parts (including
top-level images and executable code) fail explicitly. Known inline/file media
inside function responses are preserved; unknown protocol structures fail.
Incremental function-call arguments (`partialArgs`/`willContinue` on a call)
are outside v1 and fail explicitly. Function results must match an
earlier call by ID and name, or an unambiguous name when IDs are absent. Calls
cannot cross boundaries or remain unresolved. Responses with `willContinue: true`
retain the open call; only a response with false or absent `willContinue`
closes it. Every chunk is preserved in order, and an unfinished stream fails
atomically with `failed_precondition`. No partial window is returned.
Thought-only windows yield `entries: []` and `isComplete: true`, distinct
from nonexistent handoffs or missing ranges.

`MaxResponseBytes` defaults to 1 MiB (configurable from 1 KiB to 16 MiB) and bounds
role definitions, the committed handoff, and the projected window entries. Each
tool result repeats its content as text and structured JSON; its serialized size
is not checked. Requests are capped at 16 KiB. The committed window is read with
Tape's native range operation; only serialized projected entries consume the
window budget. Private metadata and thought-only entries do not consume it.
Limits fail atomically, with no truncation. Responses enforce `Cache-Control: no-store`.

## Errors

Authenticated tool failures set `isError: true` and carry
`{"error":{"code":"…","message":"…"}}` in both result forms. Messages are
static and never include private references, paths, credentials, or storage
diagnostics.

| Code | Meaning |
| --- | --- |
| `permission_denied` | Wrong anchor/binding/Role/scope, missing tool permission, or missing/expired/revoked credential |
| `invalid_reference` | Malformed arguments, missing/non-handoff anchor, or missing Role revision |
| `failed_precondition` | Missing window entries or excluded boundary, unsupported/incomplete projection, or exceeded limits |
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
