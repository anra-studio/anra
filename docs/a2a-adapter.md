# A2A adapter

The A2A adapter exposes Anra peers through A2A messages, Agent Cards, and
protocol extensions. Anra owns peer identity, skills, runtime behavior, and
context-sharing rules. Tape-go remains the local append-only storage/runtime
foundation. Each peer has its own runtime, owner, and tape.

## Adapter boundary

The registry stores canonical `Peer` metadata: `id`, `description`, `address`,
and repeated `skill`, with observed presence recorded separately. The adapter
generates an Agent Card from that metadata when publishing or constructing an
A2A client. Registration does not translate a Peer into an Agent Card or store
one in the registry. See the [registry contract](registry.md).

| Concern | Owner | A2A adapter responsibility |
| --- | --- | --- |
| Peer identity, address, and skills | Peer model | Translate `Peer` into an Agent Card with `a2a.Card` |
| Registration, discovery, and presence | Registry and discovery packages | Consume resolved Peer metadata to construct protocol clients |
| Reasoning and turn admission | Agent runtime | Translate A2A messages into runtime calls and results into responses |
| Delegation and collaboration progress | Delegator model | Encode and decode model-owned metadata in A2A extensions |
| Context selection, windows, and grants | Anra context-sharing logic | Expose borrowing operations through the TapeView extension |

Protocol paths, SDK types, extension URIs, and wire encoding belong at this
boundary. Reusable runtime and context-sharing rules should be callable by
other hosts or adapters, including a web UI.

Borrowing and committed-view selection belong to `pkg/flow/borrow`; lease
issuance and lifetime belong to `pkg/flow/grant`. Collaboration orchestration
lives in `pkg/flow/clbr`, and root `flow.Set` groups these flows for the host.
Public content, exporters and codecs live in `pkg/model/view`. A2A and MCP use
the same borrowing journal and grant handler; `pkg/a2a` owns only protocol
handling and clients.

`pkg/a2a` uses the official `a2a-go/v2 v2.5.0` SDK for core A2A JSON-RPC. The
optional `urn:anra:a2a:tape-view:v1` extension uses a generated Connect service
advertised as a single endpoint in the Agent Card. Its protobuf service and
generated Connect v2 bindings live under `proto/` and `gen/`. Borrowing does not
create A2A tasks.

## Messaging

Core text communication uses the official `a2aclient.NewFromCard`. Anra callers
use `NewMessageClient(ctx, httpClient, peer)` to construct that client from
canonical Peer metadata; external callers holding an Agent Card use
`NewMessageClientFromCard`.

`NewExecutor(runtime, &flows.Clbr)` bridges text-only user messages to `Anra.Run` and emits direct
agent Messages; non-text input and task continuation are rejected. Task-based
executors and custom task stores can be supplied directly to `NewPeerHandler`
through SDK interfaces. The historical tape-backed task persistence profile is
not restored by this release. Core message IDs are not a durable runtime
deduplication key; the borrowing extension's request IDs are.

For a messaging-only server, `NewMessageHandler(card, core, a2a.TrustedPeerIdentity)`
serves card discovery and caller-identified A2A endpoints without a borrowing
service or grant journal. The CLI mounts both messaging and borrowing with
`NewPeerHandler`.

## Borrowing: lender setup

Create one grant handler for each source Tape and inject a borrowing service
into both adapters. `Views` resolves only committed Refs; the native Agent
implements it. The host initializes separate context and lease-sidecar storage.
Only context storage is wrapped as a Tape; `.lease.db` holds auxiliary grant
state, not another context Tape.

```go
// Imports: tape and tapeview are tape-go's pkg/tape and pkg/tape/view.
// ctx, researcher and leaseStorage are initialized by the host.
journal, err := borrow.NewJournal(
    ctx,
    borrow.JournalConfig{
        Source:  researcher.Source(),
        Owner:   "researcher-local",
        Storage: leaseStorage,
    },
)
// Handle err before constructing the grant flow.
grants, err := grant.New(
    grant.Config{
        Source: researcher.Source(),
        Owner:  "researcher-local",
        Leases: journal,
    },
)
// Handle err. The host retains ownership of leaseStorage.
handlers := tapeview.NewRegistry()
if err := handlers.Register(grants); err != nil {
    return err
}
contextTape := &tape.Tape{
    TapeStorage: researcher.Tape(),
    OwnerID:     "researcher-local",
    Registry:    handlers,
}
// Handle err before constructing the service.
borrowing, err := borrow.New(
    borrow.Config{
        Source:  researcher.Source(),
        Tape:    contextTape,
        Views:   researcher,
        Journal: journal,
        Grants:  grants,
    },
)
// Handle err before mounting adapters.
handler, err := a2a.NewViewHandler(borrowing, a2a.TrustedPeerIdentity)
// Handle err, then mount handler at a2a.ViewPath + "/" and a2a.ViewRESTPath.
```

The host drains both HTTP adapters before closing sidecar and context storage.
Neither flow opens or closes storage. Multiple services for the same source
share the Journal and registered grant service.
The same service can be passed to `mcp.Config.Borrowing`; see the
[MCP contract](mcp.md).
Local controllers and remote peers use `Borrow` with the configured `Review`
policy or the default one-hour grant selection. Controllers can request an
explicit committed Ref through `BorrowRequest.View`; there is no preapproved
issuance bypass. Approval cannot supply an arbitrary Tape range; commitment remains
owned by Views. Missing eligible views are denied and never yield empty leases.

## Borrowing: borrower setup and model consumption

Resolve typed Peer metadata through discovery, check `RequireOnline`, and use
`NewPeerClient(httpClient, peer)` for the recognized borrowing skill. External
A2A clients can discover the Agent Card and use `NewClient`. HTTP client identity,
endpoint trust and timeouts remain caller-owned. `TrustedPeerIdentity` is only
for trusted networks; authenticated deployments supply their credential verifier.

`Client.BorrowContext` composes Borrow and ReadView and returns a decision and
one complete Quote. It verifies decision request correlation and an explicitly
requested Ref. ReadView checks the lease ID, caller, exact Ref and complete
ordered sequence coverage. Gaps are skipped; missing, reordered or duplicate
entries fail. These checks are shared by direct clients and the native
`borrow_peer_context` tool.

The tool accepts peer/requestId/purpose/query, retains the decision in ProtoJSON,
and presents the shared borrowed Quote as a tool result. Content is JSON text
inside ADK's map conversion so opaque numeric lexemes remain intact. The tool
copies provenance from the shared Quote instead of constructing new attribution.
Native messages never acquire the roles or execution authority of borrowed
historical messages; storing and replaying the tool result preserves this boundary.
`RankPeers` and the existing Ranker/Scorer seam remain available for relevance
ordering of already permitted peers.

## Protocol and content

Core A2A uses the official SDK. The optional `urn:anra:a2a:tape-view:v1`
extension uses generated Connect RPCs plus Vanguard REST:

| Operation | Meaning |
| --- | --- |
| Borrow | Select a complete committed Ref and record granted, denied or pending |
| Get | Retrieve the authenticated requester's recorded decision |
| Read | Read the complete Quote of the requester's currently active lease |
| Release | Persist terminal lease release for the authenticated requester |

`a2a.ViewPath` is the generated TapeViewService path; `a2a.ViewRESTPath` is
`/v1/tape/`. Each request activates the extension and receives an acknowledgement.
Identity comes from authentication, never request body fields. Release and local
Revoke share the same terminal record. Approve, Reject and Revoke have no remote RPC. `borrow.Service.Approve` completes
an existing pending request with a lease; `Reject` commits denial. Neither calls
Review again. Invalid TTL, unavailable committed context and failed issuance leave
the request pending. Completed requests cannot be approved or rejected again.

`borrow.Journal` owns request transitions and their sidecar records. Its shared
lock protects state checks and one append, including approvals from different
services. The grant flow has no pending-request state or nullable expected mode;
it derives a candidate lease from explicit native views. Borrow commits that
candidate with the request decision before exposing it to the caller.

Public values live under `gen/anra/model/view/v1`. Ref is `source + scopes`,
Lease is `id + caller + ref + expiresAt`, and Quote is channel, provenance and
entries. Quote supports collaboration channels beyond borrowing; the borrowing
service assigns `borrowed` to its own quotes. ReadRequest contains only lease_id;
ReadResponse contains one complete Quote. There are no pagination inputs or
continuation outputs. The borrowing flow opens a public JSON stream with
`Read(ctx, caller, leaseID)`; this unary adapter consumes it through `view.Decode`
before responding, so failures cannot produce a successful partial Quote.
There is no independent snapshot-head/end field or
one-range limit. All sequences are arbitrary-precision canonical decimal strings.
`Ref.Scopes` is nonempty and uses ordered, non-overlapping half-open ranges.
Anra resolves each exact completed window and rejects arbitrary cuts or scope
reordering. Bounds may reject an entire candidate, never trim its scopes.

The shared ContextExporter preserves SDK-backed model content, all multimodal
Parts, thought text/markers, and opaque business JSON precision. Empty Parts
entries remain present. Native anchors expose their public body without OwnerEpoch; unknown
anchor schemas produce null public content while retaining entry positions. Execution state, arbitrary extensions and
provider signatures are excluded without changing stored events. The explicit
TextExporter remains usable for text-only services. There is no Summary or
Projection pass. Both adapters use the same configured exporter.

Payload bytes retain their exact JSON encoding; ProtoJSON represents those
bytes as base64. MCP's JSON Quote exposes content as raw structured JSON.
Complete reads preserve media without splitting or truncation. Requests and responses have
no fixed body cap, including both Peer clients and shared HTTP transports.
Field constraints, lease checks, complete scope coverage and registry-specific metadata
size policy remain in effect. See the [protobuf contract](../proto/README.md).

## Persistence and operations

`github.com/scbizu/tape-go v0.2.0-anra.5` provides native `Tape.OverView`,
`view.Registry` and `view.Handler`; see the [integration model](designs/tape-view-handlers-design.md).
The `pkg/flow/grant` handler implements the native Handle contract and owns lease
issuance and validity checks. `pkg/flow/borrow.Journal` is the sole persistence
owner for request decisions, leases and release state. The host injects sidecar
storage through `borrow.JournalConfig.Storage`; bbolt and JSONL use the same
journal implementation. The grant authorizer consumes the Journal through its
small `grant.Leases` contract.

Native `OverView` loads every selected scope in order and dispatches the complete
view collection once. The grant handler derives the lease Ref from that collection,
checking owner, session and scope ordering. All scopes remain one lease and one
sidecar decision; gaps are never included in an enclosing range. The command carries only caller and TTL. Issuance returns a candidate lease;
only the borrowing journal can make it readable by atomically recording the
request outcome.

The sidecar's first record binds the Source. Subsequent `anra:lease-decision/v1`
records preserve the request, decision and release state. One append supplies
both the new state and its audit record. It uses tape-go storage operations
without constructing a second Tape, exposing context views, or changing source
sequences. The journal checks the entire recovered tail before publishing its
request and lease indexes.

Request IDs are scoped by caller. Reusing a valid request returns its original
decision without renewal, even when inputs change. The client still rejects a
granted response whose view differs from an explicitly requested view. Pending
approval and release check the previous outcome inside the shared journal's
critical section. Competing approvals cannot overwrite a completed outcome.

Use `.lease.db` for the new sidecar format. Historical `.grants.db` and the
superseded `.crops.db` format are not read or migrated; their leases must be
reissued. Context Tape data remains unchanged.

The shared journal orders lease snapshots with Release/Revoke. Reads authorized
after termination fail; a request already authorized may finish. Expiry is
checked against the grant flow's clock on every read. Source identity and complete
Scopes stay stable across restart and later Tape appends. Already delivered
content cannot be recalled.

## Nonblocking collaboration

The collaboration extension transports generated request/response metadata as
ProtoJSON over official A2A messages. Delegator owns roots, bounded deadlines,
immutable sibling paths and turn-local BUSY suppression. A busy response does
not admit another Turn and may contain a committed Ref hint. The hint conveys
no lease; its recipient borrows under its own identity.

The native Agent selects its latest successful completed window by default.
For start S and successful close C its single scope is `[S+1,C)`. The generic
Ref can contain multiple committed scopes; the end is derived from the last
scope, never stored separately. Lease reads do not enter reasoning Admission,
call the lender's model or reselect newer scopes. An unchanged or older borrowed
Ref leaves BUSY suppression in place; a newer end or source incarnation permits
one further attempt. There is no hop budget or automatic polling scheduler.

See the approved [shared View design](designs/shared-view-design.md) and run
`GOFLAGS=-race mise run test` for the real HTTP/Tape/record-store acceptance suite.
