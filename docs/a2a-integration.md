# Peer A2A and tape borrowing

Anra owns peer communication and context sharing. Tape-go remains the local
append-only storage/runtime foundation. Each peer has its own runtime, owner,
and tape; there is no coordinator requirement or shared team tape.

`pkg/a2a` uses the official `a2a-go/v2 v2.5.0` SDK for core A2A JSON-RPC. The
optional `urn:anra:a2a:tape-view:v1` extension uses a generated Connect service
advertised as a single endpoint in the Agent Card. Its protobuf service and
generated Connect v2 bindings live under `proto/` and `gen/`. Borrowing does not
create A2A tasks.

## Lender setup

Initialize the source tape under the lender's **local** owner using tape-go's
bbolt backend, then open a dedicated bbolt database for grants. `Config.Tape`
is an initialized `*bbolt.Bbolt`; `Config.Journal` is an open `*bolt.DB` from
`go.etcd.io/bbolt`.
`JournalOwner` namespaces grant buckets and is server configuration, never a
value from a peer request body. The caller initializes/closes the source tape
and opens/closes the grant database.

The example imports `tapev1 "github.com/scbizu/anra/gen/anra/a2a/tape/v1"`.

```go
researcherTape, err := bbolt.NewBboltStorage("researcher-session", "researcher.db")
// Check err, then defer researcherTape.Close().
err = researcherTape.Init(owner.WithOwnerId(ctx, "researcher-local"))
grantJournal, err := bolt.Open("grants.db", 0600, &bolt.Options{Timeout: time.Second})
// Check err, then defer grantJournal.Close().
// Keep the source session ID stable across restarts and check initialization errors.
views, err := a2a.NewService(a2a.Config{
    Source: &tapev1.Source{AgentId: "researcher", TapeId: persistentTapeIncarnationID},
    Owner: "researcher-local",
    Tape: researcherTape,
    Journal: grantJournal,
    JournalOwner: "researcher-grants",
    Policy: a2a.BestRangePolicy{
        Eligible: permittedAnchorCandidates,
        Ranker: decisionProvider,
        TTL: time.Hour,
    },
})
// Check err after each constructor/configuration call.
executor, err := a2a.NewExecutor(researcherAnra)
core := a2asrv.NewHandler(executor)
err = a2a.Advertise(card, publicBaseURL + a2a.ViewPath)
handler, err := a2a.NewPeerHandler(card, core, views, authenticatePeer)
```

The caller supplies a native SDK Agent Card with its name, skills, input/output
modes, security schemes/requirements, and a JSONRPC interface at
`publicBaseURL + a2a.CorePath`. `NewPeerHandler` serves:

| Path | Behavior |
| --- | --- |
| `/.well-known/agent-card.json` | Public native SDK Agent Card |
| `/a2a` | Authenticated core A2A JSON-RPC, including SDK streaming |
| `/anra.a2a.tape.v1.TapeViewService/` | Authenticated protobuf/Connect extension RPCs |

`authenticatePeer` verifies credentials (for example, bearer tokens or mTLS)
and returns a stable requester identity. Authentication is also propagated into
SDK `CallContext.User`. The public card must describe the chosen authentication
scheme. Authentication establishes identity; `Policy` separately decides who
may borrow which context.

`Eligible` returns only permitted, exportable candidate anchor summaries and
source ranges. `Ranker` receives those summaries and the query. It can adapt JEV
or another decision API to `[]Score{Index, Score, Confidence}`. Highest score wins,
then highest confidence, then original candidate order. Empty results deny;
provider errors propagate without selecting fallback private context. This local adapter seam can wrap tape-go's decision classifier; A2A permission
policy remains in Anra.

## Borrower setup and model consumption

Discover configured, trusted peer card URLs. An optional directory can provide
these same references. `BestPeer` ranks visible native card skill descriptions
and returns one best peer or no match. This release does not implement a network
registry or automatic registration.

```go
card, err := a2a.Discover(ctx, credentialScopedHTTPClient, peerCardURL)
client, err := a2a.NewClient(credentialScopedHTTPClient, card)
borrower, err := agent.New("borrower-local", borrowerTape,
    agent.WithRunner(modelRunner),
    agent.WithTool(a2a.BorrowTool{
        Client: client,
        Name: "borrow_researcher_context",
        Description: "Borrow relevant storage research from the researcher peer",
    }),
)
output, err := borrower.Run(ctx, "Find the peer's storage concurrency findings")
```

The HTTP client supplies credential handling, timeouts and endpoint trust;
validate card endpoints and redirects before forwarding credentials. The tool
accepts `requestId`, `purpose`, and `query`. It returns the decision and, for a
grant, all pages checked against its source, snapshot and sequence range. Anra persists the result as a local tool response,
with `agentId`, `tapeId`, `viewId`, source sequences and snapshot head intact.
Peer entries are context data inside that response; their roles never become
local assistant messages or locally executed tool calls.

`NewClient` uses the generated Connect binding, and `Advertise` declares its
service URL as the extension's `endpoint`. The handwritten tape-view JSON-RPC
transport and `/tape-views` route have been removed; update old peer cards to
`publicBaseURL + a2a.ViewPath`. No legacy fallback is provided. Core A2A continues
to use the official SDK. See [API schema and mise/Buf workflow](../proto/README.md)
for generation, error mappings, and schema evolution.

For pending decisions, retry the identical borrow request or use `Client.Get`.
The lender resolves pending requests with the local `Service.Resolve` method.
The borrower can release a grant with `Client.Release`; the lender can revoke it
with `Service.Revoke`. Neither resolve nor revoke is a remote borrower RPC.
The borrowing tool leaves the grant open until explicit release or expiry.

Core text communication uses the official `a2aclient.NewFromCard`. `NewExecutor`
bridges text-only user messages to `Anra.Run` and emits direct agent Messages;
non-text input and task continuation are rejected. Task-based executors and
custom task stores can be supplied directly to `NewPeerHandler` through SDK
interfaces. The historical tape-backed task persistence profile is not restored
by this release. Core message IDs are not a durable runtime deduplication key;
the extension's request IDs are.

## Wire contract and limits

Clients activate `A2A-Extensions: urn:anra:a2a:tape-view:v1` on every extension
request and require the acknowledgement header. Activation is not authorization.

The Connect server uses validate-go's server interceptor with Protovalidate
rules from `tape.proto`. Invalid required fields, byte lengths, page sizes, and
sequence bounds are rejected before the handler runs, with `invalid_argument`
and field violation details. View size remains the complete selected range;
there is no independent entry-count cap. Authorization and grant integrity
are enforced by `Service` separately from request validation.

`Service`, `Client`, policies, and exporters use generated protobuf messages
from `gen/anra/a2a/tape/v1` directly. Local requests use the same Protovalidate
rules as Connect requests; validation errors retain `ErrInvalidParams` and
expose `protovalidate.ValidationError`. Configuration uses
`go-playground/validator/v10`. Conversion is limited to tape-go ranges and the
private journal representation, whose existing JSON format remains compatible.

| Method | Parameters | Result |
| --- | --- | --- |
| `Borrow` | `requestId`, `purpose`, `query`, optional `bounds` | granted view, denied, or pending |
| `Get` | `requestId` | Existing decision for this requester |
| `Read` | `viewId`, optional `cursor`, optional `pageSize` | Entry page with source provenance |
| `Release` | `viewId` | `{ "released": true }`, idempotent |

The canonical wire schema is `proto/anra/a2a/tape/v1/tape.proto`. Each method
has a generated request and response message; responses wrap `Decision` or
`Page`. All sequence fields are canonical unsigned decimal strings with
arbitrary precision. v1 supports at most one half-open range `[seq_s, seq_e)`,
plus an empty approved range list. Status uses `DecisionStatus`; times use
protobuf Timestamp. Exported entries contain only public projections and
allowed payload metadata, never local owner IDs or CustomEntry extensions.
`Payload.json_data` preserves raw JSON bytes and is base64 in ProtoJSON. The borrowing tool also uses ProtoJSON for decisions and pages. Journal JSON
retains its independent storage format.

Bounds can only narrow selection. Grants capture source identity, tape session,
head, exporter version and content fingerprints. Appends do not extend a grant.
Each read verifies the requester, closure, expiry, source and granted content.
A cursor is an opaque encoding of the grant ID, snapshot and offset; it can only
page within an independently authorized grant, never expand its range.

| Limit | Value |
| --- | --- |
| Default / maximum page size | 32 / 100 entries |
| View size | Complete range selected by the lender's turn/anchor policy; no independent entry-count cap |
| Maximum encoded entry | 8 KiB |
| Request / response body | 1 MiB |
| Maximum grant TTL | 24 hours |
| Request ID | 256 bytes |
| Purpose / query | 8 KiB each |

The default `TextExporter` exports user/assistant readable summaries as typed
`text/plain` JSON strings and never copies arbitrary custom extensions. Tool,
anchor, lifecycle and other custom entries require an explicit deterministic
`Exporter`; unsupported entry kinds fail the whole grant. If using another
exporter, update the advertised `entryMediaTypes` to its actual supported types.
Changing exporter behavior requires a new exporter name.

| Connect code | Meaning |
| --- | --- |
| `invalid_argument` | Malformed message or invalid parameters |
| `failed_precondition` | Extension inactive |
| `already_exists` | Request ID reused with different input |
| `not_found` | View unavailable (unknown, wrong requester, closed, expired or changed source) |
| `internal` | Storage failure or masked internal/provider failure |
| `unimplemented` | Unknown RPC or unsupported entry representation |
| `unauthenticated` | Peer authentication failed |
| `resource_exhausted` | Oversized RPC message |

Errors never include another requester's grant details. `Get` and repeated
`Borrow` recover the recorded decision even after expiry/release; `Read` enforces
current availability. JSON-RPC envelopes, notifications and batches are no
longer part of the tape-view API.

## Persistence and operations

`Service` retains configuration and database handles, with no in-memory grant
projection. Construction performs no journal reads, and restart requires no
replay. `Borrow`/`Get` look up the requester/request-ID key; `Read`/`Release`
resolve a view-ID key to its current grant. Each lookup validates only the
addressed record, its index and source identity. Unrelated records and the
audit history are not scanned on access.

Each update atomically commits the current record, view-ID index and an
append-only audit record in one bbolt transaction, before responding. It checks
the previous state inside that transaction, rejecting stale decisions or
closures with `ErrConflict`. Pending/denied/granted decisions, request-ID
idempotency and revocations persist across restart. The service serializes its
local policy operations; services sharing an open database use transactional
state comparisons. Separate processes cannot open the same bbolt file for
writing while it is locked.

Grant lookups use on-disk B-tree indexes, requiring O(log N) database work and
one decoded grant per operation. The service does not retain O(N) grant state.
Complete bounded source-view fingerprint checks still run on each `Read`;
that selected range is loaded temporarily. SHA-256 fingerprints reuse the JSON
bytes already produced by entry validation, avoiding a second serialization of
the complete view. Request retries and state transitions compare protobuf requests and decisions
with `proto.Equal`, without marshaling or hashing. Journal-only scalar metadata
is compared directly. Persisted fingerprint values retain their existing
format. tape-go's `TapeStorage` interface
has no keyed grant lookup or shared transaction API, so the journal uses the
bbolt handle directly rather than a finder.

The indexed journal uses the `anra:grant-journal/v2` bucket with per-owner
`requests`, `views`, and `history` sub-buckets. This changes `Config.Journal`
from a tape-go storage interface to `*bolt.DB`. Existing tape-format grant
journals are rejected on access; they must be explicitly migrated before use,
rather than silently losing saved decisions or revocations.

Provision `AgentID` and the opaque tape incarnation ID durably. Keep them across
normal restart; rotate the incarnation ID and start a fresh journal when replacing
or resetting a source tape. Session, head and content checks detect missing or
changed source entries; a byte-identical copy with the same configured identity
cannot be distinguished through tape-go's current storage interface. Already
delivered context cannot be recalled by closing a grant.

Run `go test ./pkg/a2a` for the executable two-peer workflow, official SDK core
and streaming compatibility, model consumption/replay, bbolt restart,
idempotency, authentication, bounds, corruption and failure checks.

References: [A2A extensions](https://a2a-protocol.org/latest/topics/extensions/),
[A2A discovery](https://a2a-protocol.org/latest/topics/agent-discovery/),
[design and ownership](designs/a2a-tape-view-extension-design.md).

Client validation boundaries and the simplification experiments are recorded in
[A2A client ablation](designs/a2a-client-ablation.md).
