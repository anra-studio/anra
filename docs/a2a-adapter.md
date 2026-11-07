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

Borrowing, committed-view resolution, quoted content and lease lifecycle belong
to `pkg/model/view`. A2A and MCP use the same service and grant record store;
`pkg/a2a` owns only protocol handling and clients.

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

`NewExecutor` bridges text-only user messages to `Anra.Run` and emits direct
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

Create one `view.Service` for each source Tape and inject it into every adapter.
`Views` resolves only committed Refs; the native Agent implements it. `Tape`
accepts the existing read-only range interface. `RecordPath` opens an owned
`grantRecord.Store`; `Records` supplies a caller-owned store instead. It binds
persisted decisions to the configured source incarnation.

```go
views, err := view.NewService(
    view.Config{
        Source:     researcher.Source(),
        Owner:      "researcher-local",
        Tape:       researcher.Tape(),
        Views:      researcher,
        RecordPath: "researcher.grants.db",
    },
)
// Handle err before mounting adapters.
handler, err := a2a.NewViewHandler(views, a2a.TrustedPeerIdentity)
// Handle err, then mount handler at a2a.ViewPath + "/" and a2a.ViewRESTPath.
```

The host drains handlers before closing the service and source Tape. The same
service can be passed to `mcp.Config.Views`; see the [MCP contract](mcp.md).
A local controller's `views.Grant` supplies approval for an explicit committed
Ref. Remote Borrow uses the configured approval function or the default grant
selection. Approval cannot supply an arbitrary Tape range; commitment remains
owned by Views. Missing eligible views are denied and never yield empty leases.

## Borrowing: borrower setup and model consumption

Resolve typed Peer metadata through discovery, check `RequireOnline`, and use
`NewPeerClient(httpClient, peer)` for the recognized borrowing skill. External
A2A clients can discover the Agent Card and use `NewClient`. HTTP client identity,
endpoint trust and timeouts remain caller-owned. `TrustedPeerIdentity` is only
for trusted networks; authenticated deployments supply their credential verifier.

`Client.BorrowContext` composes Borrow and ReadView. It verifies decision request
correlation and an explicitly requested Ref, then reads every page in scope order.
Every page must retain the lease ID, caller and exact Ref. Each authorized
sequence occurs exactly once, gaps between scopes are skipped, and incomplete
or non-progressing pagination fails. These checks are shared by direct clients
and the native `borrow_peer_context` tool.

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
| Read | Read a page of the requester's currently active lease |
| Release | Persist terminal lease release for the authenticated requester |

`a2a.ViewPath` is the generated TapeViewService path; `a2a.ViewRESTPath` is
`/v1/tape/`. Each request activates the extension and receives an acknowledgement.
Identity comes from authentication, never request body fields. Release and local
Revoke share the same terminal record. Resolve and Revoke have no remote RPC.

Public values live under `gen/anra/model/view/v1`. Ref is `source + scopes`,
Lease is `id + caller + ref + expiresAt`, and Quote is channel, provenance and
entries. Quote supports collaboration channels beyond borrowing; the borrowing
service assigns `borrowed` to its own quotes. ReadResponse carries the Quote and
nextCursor separately, since continuation belongs to the read operation.
There is no independent snapshot-head/end field or
one-range limit. All sequences are arbitrary-precision canonical decimal strings.
`Ref.Scopes` is nonempty and uses ordered, non-overlapping half-open ranges.
Anra resolves each exact completed window and rejects arbitrary cuts or scope
reordering. Bounds may reject an entire candidate, never trim its scopes.

The shared ContextExporter preserves SDK-backed model content, all multimodal
Parts, thought text/markers, and opaque business JSON precision. Empty Parts
entries remain present. Native anchors expose Body without OwnerEpoch; foreign
anchor bodies remain verbatim text. Execution state, arbitrary extensions and
provider signatures are excluded without changing stored events. The explicit
TextExporter remains usable for text-only services. There is no Summary or
Projection pass. Both adapters use the same configured exporter.

Payload bytes retain their exact JSON encoding; ProtoJSON represents those
bytes as base64. MCP's JSON Quote exposes content as raw structured JSON.
Entry pagination never splits or truncates media. Requests and responses have
no fixed body cap, including both Peer clients and shared HTTP transports.
Field constraints, lease checks, cursor binding and registry-specific metadata
size policy remain in effect. See the [protobuf contract](../proto/README.md).

## Persistence and operations

`pkg/model/grantRecord` owns the bolthold/bbolt authorization database. It keeps
source binding, one request state indexed by lease ID, immutable grants,
terminal release records and an append-only audit trail. Request IDs are scoped
by caller; replaying identical input returns the same decision without renewal,
while changed input conflicts. Pending resolution and release compare the old
state inside their transaction. There is no second in-memory grant projection.

`view.Service` orders Read authorization with Release/Revoke. Reads authorized
after termination fail; a request already authorized may finish. Expiry is
checked against the shared clock on every read. Source identity and complete
Scopes stay stable across restart and later Tape appends. Already delivered
content cannot be recalled. Historical schema/storage migration is not supplied.

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
