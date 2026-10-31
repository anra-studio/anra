# Read-only handoff context over MCP

`pkg/mcp` implements the [Anra MCP design](https://github.com/anra-studio/anra/blob/d7b0a38d0e9d7afd29a763e71679814c6e2b76c8/docs/designs/anra-mcp-spec.md)
tracked by [Issue #13](https://github.com/anra-studio/anra/issues/13). The design
currently lives on the documentation branch. This implementation is based on
`main` and does not change the Peer or CLI endpoints.

The service uses the [official Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk)
for protocol negotiation, JSON-RPC, and stateless Streamable HTTP with JSON
responses. Mount `Service.Handler()` at the embedding runtime's `/mcp` endpoint.
Every HTTP request, including discovery, requires `Authorization: Bearer …`.
Remote deployments terminate TLS. When configured, the handler enforces the
Origin allowlist on every method; the SDK's localhost Host/DNS rebinding check
also remains enabled.
MCP session identifiers, role names, and claimed external session
IDs never establish identity.

`Config.TrustedOrigins` is an exact allowlist of serialized HTTP(S) origins,
for example `[]string{"https://console.example"}`. When unset or empty
(`len(TrustedOrigins) == 0`), the service does not restrict Origin; requests
carrying Origin are allowed with valid Bearer credentials. A nonempty allowlist
enables strict validation: requests carrying Origin must contain exactly one
valid serialized origin matching an entry. Untrusted, malformed, empty or
repeated Origin headers then fail with HTTP 403, the static `permission_denied`
envelope and `Cache-Control: no-store`. Entries include scheme, host and any
explicit port; paths, credentials, queries, fragments and wildcards are invalid
configuration. Requests without Origin remain usable by native clients in both
modes. Every request still requires valid Bearer credentials; Origin validation
is independent of Bearer authentication.

The embedding runtime owns the listener, TLS, and browser trust configuration.
Local deployments should bind loopback. Configure browser origins explicitly
from trusted deployment settings. The handler never derives trust from Host,
Forwarded or X-Forwarded-* headers. Reverse proxies must preserve Origin and
Authorization, reject untrusted hostnames, prevent direct access to the backend,
and terminate TLS securely. Do not strip Origin to bypass this check. Origin
trust does not grant CORS access or replace connection authentication. See the
[MCP transport security requirements](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports#security-warning).

## Local controller lifecycle

One service owns one source's private state database and borrows the initialized
Anra Tape. Its configured Source must match the persisted `tape.initialized`
record. The controller owns the HTTP listener and the Tape; stop HTTP requests
before closing the service, and drain the agent before closing its Tape.

The following APIs are local Go methods, never MCP tools:

1. `SaveRole(ctx, RoleDefinition)` publishes the complete reproducible definition
   as a JSON object under `(id, version)`. Include everything needed to rebuild
   that revision, without live aliases or credentials. Saving identical contents
   is idempotent; overwriting a revision fails.
2. `CommitHandoff(ctx, Handoff)` validates the `anra.dev/anchor/intr/v1`
   `integration.handoff` record and its explicit protobuf TapeView. All sequences
   in `[seqS, seqE)`, and the excluded `snapshotHead == seqE` boundary, must
   already exist on disk. It validates the persisted range and appends the
   handoff anchor to Tape before returning its sequence. This operation does
   not write the state database; Tape alone owns handoffs and their windows.
3. `Authorize(ctx, Grant)` requires an exact match of Role ID, handoff sequence,
   target RoleBinding, source, snapshot head, and scope. Permissions are an
   explicit subset of `read_role` and `read_context_window`, with a required
   future expiration. It returns a cryptographically random credential once;
   only its SHA-256 digest is stored.
4. `Revoke(ctx, credential)` durably and idempotently ends access. Each HTTP
   request reloads its grant and checks expiry and revocation.

For example, after creating an agent and selecting an explicit persisted view:

```go
service, err := mcp.NewService(mcp.Config{
    Source: a.Source(), Owner: "vesta", Tape: a.Tape(),
    StatePath: "/private/runtime/vesta-mcp.db",
})
// Handle err; close service after the HTTP server has stopped.

revision := mcp.RoleRef{ID: "vesta", Version: "role-revision-42"}
err = service.SaveRole(ctx, mcp.RoleDefinition{
    RoleRef: revision,
    Definition: json.RawMessage(`{"instructions":"Help maintain Anra"}`),
})
// Handle err before committing the handoff.

anchorSeq, err := service.CommitHandoff(ctx, mcp.Handoff{
    Schema: mcp.HandoffSchema, Type: "integration.handoff", Role: revision,
    Target: mcp.Target{Name: "lody", BindingID: "vesta-lody"},
    Task: "Implement the requested feature", Context: mcp.WindowRef{View: fixedView},
    Origin: &mcp.Origin{Name: "raft", SessionID: "origin-session"},
})
// Handle err before issuing credentials or dispatching external work.

credential, err := service.Authorize(ctx, mcp.Grant{
    RoleID: "vesta", BindingID: "vesta-lody", AnchorSeq: anchorSeq,
    Window: mcp.WindowRef{View: fixedView},
    Permissions: []string{mcp.ReadRole, mcp.ReadContextWindow},
    ExpiresAt: time.Now().Add(time.Hour),
})
// Handle err. Deliver credential through secure runtime configuration only.
mux.Handle("/mcp", service.Handler())
```

Definition publication, Tape handoff commit, grant issuance, and secure
credential delivery must all succeed before dispatch claims context is
available. Launching sessions, resolving application bindings, OAuth issuance,
and injecting credentials into Lody or other applications remain integration
responsibilities. This service does not assume those capabilities exist.
Never put credentials in prompts, Tape entries, artifacts, or logs.

An explicitly selected persisted prefix can be committed during an active turn.
Reads never acquire reasoning Admission or select the current Tape head. Later
model output and background observations cannot expand the window. Tape's
append-only contract owns immutability; the service does not maintain duplicate
handoff records or hash anchors, windows, or Role definitions. Missing records,
unsupported entries, and incomplete tool calls fail reconstruction.

On every read the service validates the grant and tool permission, loads the
handoff anchor from Tape by `AnchorSeq`, and verifies its Role, binding and exact
TapeView against the grant. It then loads that Role revision or projects entries
directly from the fixed TapeView range. There is no secondary commit record.
A persisted handoff can be authorized after restart even if credential issuance
was never completed. The controller must retain the returned anchor reference
and complete authorization before dispatching.

The state database retains immutable Role definitions and source identity;
authorization is solely token-hash-to-grant records, including expiration and
revocation. It is created with mode `0600`. Tape handoffs, Role revisions, grants,
and revocations survive restarts in their respective stores.

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

`MaxResponseBytes` defaults to 1 MiB (configurable from 1 KiB to 16 MiB) and counts
both structured and text tool-result representations. Requests are capped at
16 KiB. Reconstruction also caps raw input at 16 times the configured result
limit and 10,000 records, including the boundary. Limits fail atomically, with
no truncation. Responses enforce `Cache-Control: no-store`.

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
| `unavailable` | Transient storage/service failure |

Authentication failures return HTTP 401 with the generic `permission_denied`
envelope and a Bearer challenge. Storage failures during authentication return
HTTP 503 with `unavailable`. Cross-anchor requests are denied before resolving
the target, so nonexistent and private targets remain indistinguishable.
MCP protocol errors, such as unknown methods or tools, use standard SDK JSON-RPC
errors.

The executable acceptance contract is [mcp.feature](../tests/e2e/features/mcp.feature).
Run `mise run e2e` during development and `mise run test` for all repository checks.
Both commands run the BDD suite with the race detector enabled.
