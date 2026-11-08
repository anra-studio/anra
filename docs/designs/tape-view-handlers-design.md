# Tape Handlers over Native Views

The dependency is pinned to `github.com/scbizu/tape-go v0.2.0-anra.5`, using the upstream `Tape`, `view.OverView`, `view.Registry`, and `view.Handler` directly. Anra removes its local `pkg/tape` wrapper and retains only the borrowing domain's handler and sidecar. The native Tape continues to manage context through the same storage interface. Crops illustrates another possible View behavior; this design does not implement a general crops journal.

## Domain Relationships and Interfaces

| Concept | Responsibilities and invariants |
| --- | --- |
| Tape | Native upstream type that holds context, storage, read/write windows, and the Registry |
| Registry | Registers and looks up capabilities by `Handler.Action()`; its zero value is usable, it is concurrency-safe, and it rejects duplicate actions and replacement |
| OverView | Binds a native ordered collection of EntryViews to a Registry; one Handle call dispatches all views |
| Grant handler | Issues candidate leases from native views, checks committed lease validity, and delegates release |
| Borrow Journal | Owns request idempotency, pending decisions, atomic commits of requests and leases, release records, and recovery |
| Sidecar | Separate storage for auxiliary authorization records; it contributes no context and is not a second Tape |

`Tape.OverView(ctx, scopes...)` loads all EntryViews in the supplied order through native Range operations and associates them with Tape.Registry. `OverView.Handle(ctx, action, payload)` passes the original context, the complete `[]*EntryView`, and the payload to the registered handler in one call. Registry construction, duplicate-action checks, and dispatch are all implemented upstream. Binding alone neither proves that a View is committed nor grants read access.

`borrow.Service` validates the committed Ref's Source, scope ordering, and physical bounds. It passes every Scope to the same native OverView and checks the returned view count, owner, session, and individual ranges. `grant.Service` derives the complete Ref from the views it actually receives, rejecting views that span owners or sessions, empty views, overlapping ranges, and invalid ordering before issuing one candidate lease. `grant.Request` carries only Caller and TTL. Gaps between scopes are neither read nor authorized.

The host first binds initialized sidecar storage through `borrow.NewJournal`, creates `grant.New(grant.Config{Leases: journal, ...})`, registers it in the native Registry, and then assembles the borrowing service with `borrow.New`. The borrowing service checks consistency across Source, Owner, the Registry handler, and the Journal. A candidate lease permits reads and can be released only after it is committed to the Journal together with its request decision. Calling the handler directly does not create an effective authorization. See [flow boundaries](flow-boundaries-design.md) for the complete responsibility split.

```mermaid
flowchart TD
    Host[Host] --> ContextStorage[Context storage]
    Host --> SidecarStorage[Separate authorization storage / .lease.db]
    ContextStorage --> Tape[Native tape-go Tape]
    Tape --> Registry[Handler Registry]
    Tape --> Bound[Native OverView / all ordered views]
    Agent[Agent committed View provider] --> Flow[borrow.Service / review policy and export]
    A2A[A2A / MCP] --> Flow
    Flow -->|All Scopes + Caller / TTL| Bound
    Bound -->|Complete view collection in one call| Grant[Grant handler]
    Registry --> Grant
    Flow --> Journal[borrow.Journal / atomic commit]
    Grant -->|Authorization checks and release| Journal
    Journal --> Sidecar[Authorization sidecar / state projection]
    Sidecar --> SidecarStorage
    Flow -->|Read original ranges after authorization| Tape
```

## Authorization Flow

A request first looks up its recorded decision by Caller + Request ID and replays it if found. New requests invoke `borrow.Config.Review`. When authorization is required, the service resolves a committed View, obtains a complete candidate lease through a multi-scope OverView.Handle call, and asks the Journal to append the request and granted decision in one operation.

Pending and denied are request outcomes; they do not fabricate authorized Views. `Approve` approves only an existing pending request without rerunning Review. If no eligible View can be selected, it leaves the request pending and returns an unavailable error. `Reject` explicitly denies a pending request. Both operations return a conflict for completed requests. Borrow owns the request state machine, while grant determines validity from the clock without appending separate expiration records. Release is idempotent, and terminated leases cannot be reactivated.

Multiple borrowing services, A2A, and MCP share one Journal and grant service. The Journal lock covers state refresh, comparison with the expected snapshot, lifecycle checks, and record append, so competing approvals can commit only once. Read authorization and release are ordered at the same commit boundary: reads authorized after termination commits fail, while reads already authorized may finish.

## Sidecar and Resource Ownership

The host selects and initializes separate sidecar storage. Bbolt may use `<agent>.lease.db`; JSONL uses a separate directory. The sidecar inside the Journal only adapts storage Get, Range, and Store operations. It does not construct `te.Tape`, initialize or close storage, or expose context Views. Its internal use of entry.Seq addresses sidecar records only; these positions are neither context coordinates nor the sequence of another context Tape.

The first `anra:lease-source/v1` record fixes the Source. Subsequent `anra:lease-decision/v1` records store Caller, the ProtoJSON Request and Decision, and Released. Each append serves as both the source of the new state and its audit record. There is no generic Crop envelope, separate authorization table, or duplicate audit store. Recovery validates the complete new record sequence, Source, caller, lease ID uniqueness, and legal transitions before publishing the in-memory projection. Corrupt, missing, or unrecognized records return errors.

One shared Journal exclusively accesses each sidecar storage instance. Do not create multiple independent Journals over the same storage instance or allow external writers to bypass the Journal. The host stops requests before closing sidecar and context storage. Tape.Close retains the native context storage's close behavior. borrow.Service, Journal, and grant.Service have no Close methods.

The new format neither reads nor migrates historical `.grants.db` files or the previously replaced `.crops.db` files. Existing leases must be requested again; context data remains unchanged.

## Validation

Chinese E2E scenarios exercise real HTTP borrowing and persistent storage. They cover grant and release recovery with both sidecar backends, context isolation, rejection across Sources, missing handlers and duplicate registration, cancellation without an append, and deterministic concurrent decisions across services sharing a Journal. Existing MCP, A2A, multimodal, and multi-scope pagination scenarios verify authorization consumption. The real multi-scope lease scenario also checks that the sidecar persists only one authorization record containing every range. Pure algorithm tests cover borrowing Ref bounds, ordering, and arbitrary-precision sequence numbers. Upstream maintains the general Registry and OverView tests.

Before the flow package split, after upgrading to tape-go `v0.2.0-anra.5`, the full `mise run test` suite passed: 252 scenarios and 1,218 steps, including native multi-scope dispatch, authorization persistence, race detection, builds, pure logic tests, and Protobuf checks. Modified Go files were formatted with gofumpt.
