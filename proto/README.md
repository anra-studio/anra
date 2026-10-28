# Anra protobuf contracts

`anra/a2a/tape/v1/tape.proto` defines Anra's permissioned tape-view extension:
`Borrow`, `Get`, `Read`, and `Release`. The official A2A core remains in
`github.com/a2aproject/a2a-go/v2`; do not copy its schema or regenerate its
messages here. External A2A callers can still fetch the official JSON Agent Card;
Anra discovery resolves typed Peer metadata.

`anra/a2a/collaboration/v1/collaboration.proto` defines the delegation and
response metadata for `urn:anra:a2a:collaboration:v1`. It imports TapeView's
`AnchorRef` and adds no RPC service. These generated messages travel as ProtoJSON
objects in the official A2A Message's extension metadata. Answer text remains in
standard A2A text parts.

`anra/peer/v1/peer.proto` defines canonical Peer/Skill metadata. The
`anra/registry/v1/registry.proto` contract defines runtime presence and the
Register, Heartbeat, Disconnect, Lookup, List and Forget RPCs. It is independent
of the A2A SDK. `pkg/registry` persists ownership and metadata; `pkg/discovery`
uses the generated client, and `pkg/a2a.Card` produces the A2A representation.
Presence defaults to `PRESENCE_STATE_UNKNOWN`; the schema intentionally exempts
that enum's zero value from Buf's usual `UNSPECIFIED` naming rule. See the
[registry contract](../docs/registry.md) for persistence and mutation semantics.

The layout follows Gate: versioned packages under `proto/`, generated messages
and Connect bindings under `gen/`, transport adapters under `pkg/a2a`, and root
Buf v2 configurations. mise pins Go, Buf, protoc-gen-go, and the Connect v2
generator. The Connect runtime and generator use `v2.0.0-rc.1`, matching Gate;
update both together. Generated files are checked in and must not be hand edited.

## Tooling

From the repository root:

```sh
mise trust
mise install
mise run proto-generate
mise run test
```

Individual checks:

```sh
mise run proto-lint
mise run proto-build
mise run proto-check-generated
mise run proto-deps # Only when intentionally updating schema dependencies.
AGAINST='.git#branch=main' mise run proto-breaking
```

`proto-check-generated` generates into a temporary directory and compares it
with `gen/`, without modifying the checkout. `test` includes lint, schema build,
generation consistency, a build of all packages, and the BDD end-to-end suite.
Run `mise run e2e` for just the executable acceptance scenarios. See the
[testing policy](../docs/testing.md) for local unit checks. Installation and the first schema
build need network access. Generators run locally; Protovalidate's BSR schema
dependency is pinned by the checked-in `buf.lock` and then cached. `proto-deps`
intentionally refreshes that lock; it is not part of routine generation.

Breaking checks require a baseline that already contains this Buf module.
The initial schema introduction has no baseline on the old main branch; run
breaking checks on subsequent changes against a commit containing the schema.
Add fields with new tags, reserve removed names and tags, and use a new versioned
package for incompatible wire or semantic changes.

## Transport and semantics

`NewPeerHandler` mounts the service at `/anra.a2a.tape.v1.TapeViewService/`.
Call `Advertise(card, publicBaseURL + ViewPath)` to publish its service URL
as the extension's single `endpoint` parameter. `NewClient` uses the generated
Connect binding. Standalone servers can mount `NewViewHandler` themselves at
the same service path.

Generated clients use `tapev1connect.NewTapeViewServiceClient` with
`connect.NewClient(connecthttp.NewTransport(httpClient, baseURL))`. Every call
must include `A2A-Extensions: urn:anra:a2a:tape-view:v1`, set using
`connect.NewClientContext`. Acknowledgement comes back in response metadata.
The HTTP client is responsible for credentials, redirects, and endpoint trust.
`NewClient` handles opt-in, acknowledgement, domain conversions, and public
error mapping, and is used by `BorrowTool`.

The schema is a separate extension service, not a new core A2A transport.
The generated Connect service invokes `Service`, its permission policy, and
grant journal. `Resolve` and `Revoke` remain lender-local methods. A caller's
identity is never accepted from protobuf request fields.

Sequence fields are arbitrary-precision canonical decimal strings, including
`"0"` for an empty snapshot. Ranges are half-open; `bounds` is optional. `BorrowRequest.publication` optionally pins an `AnchorRef`
(source plus positive sequence); clients require `publicationBorrowVersion: 1`
in the card before sending it and verify exact grant provenance before reading. ProtoJSON
range fields are `seqS` and `seqE`. Local domain/journal JSON uses `SeqS` and
`SeqE`; it is independent of the protobuf wire format. Status is an enum with an invalid unspecified value.
Times use protobuf Timestamp. `Payload.json_data` contains the exact JSON bytes;
ProtoJSON represents those bytes as base64. It is deliberately not a Struct or
Value, so large integers and numeric formatting survive transport unchanged.

`NewViewHandler` installs `connectrpc.com/validate`'s `NewServerInterceptor`.
The pinned `v0.8.0-rc.1` release supports the project's Connect v2 runtime.
Request rules in `tape.proto` require identifiers, purpose and query; preserve
the existing byte limits; constrain page size to 0..100; and require positive
canonical decimal bounds in increasing order. The range CEL rule compares
decimal lengths and lexicographic order without narrowing to machine integers.
Absent bounds and page size zero remain valid.

Invalid requests return `invalid_argument` with field violation details before
the service handler or permission policy runs. The domain client preserves
`errors.Is(err, ErrInvalidParams)` and the original Connect error/details.
Structural rules for requests and responses live in `tape.proto`: required
identifiers and nested messages, canonical sequences, valid decision statuses,
one-range limits, and the relationship between status and view presence.
Protovalidate executes these rules from generated protobuf descriptors; no
separate validation-code generator is needed. Direct Service calls, peer
responses, and persisted grant messages use the same rules.

Go retains checks for JSON syntax and Timestamp seconds/nanos validity, plus
request correlation, authorization, snapshot bounds, journal integrity, export
policy, and client provenance. Non-protobuf `Config` dependencies and distinct
source/journal owners use `go-playground/validator/v10`; `Config.Source` uses its
protobuf rules. Validation errors support `errors.Is(err, ErrInvalidParams)`
and `errors.As` for `protovalidate.ValidationError` or configuration
`validator.ValidationErrors`.

HTTP transport limits encoded/decompressed RPC messages to 1 MiB. Borrowed pages
retain source, view, snapshot, and sequence provenance checks in `BorrowTool`.
The lender's selected turn/anchor range defines the complete view size. There
is no separate maximum-entry cap or truncation of that range; pagination only
splits its transport. `Eligible`/`Policy` supplies the turn boundaries.

## Collaboration metadata

`DelegationMetadata` defines the root request ID, required Timestamp deadline,
ancestors, and optional context references. Its schema limits identifiers and
context references; ancestry has no hop-count limit. The receiver validates this
metadata before reasoning admission. `pkg/model/delegator` owns runtime delegation
state: it creates roots, caps deadlines, and forks the path for each edge.
Generated metadata stays private to the model;
transport adapters receive owned snapshots.

`ResponseMetadata` defines `Status`, `RetryHint`, and an optional publication.
Only `STATUS_BUSY` may carry publication or retry hints. Status must be a defined,
nonzero enum value. Canonical ProtoJSON uses `STATUS_COMPLETED`, `STATUS_BUSY`,
and `RETRY_HINT_AFTER_NEW_PUBLICATION`. Unknown fields,
unknown enum values, invalid references, and invalid field combinations are
rejected. There is no decoder for the former lowercase wire statuses.

Both incoming and outgoing metadata use ProtoJSON plus Protovalidate descriptors;
the A2A adapter caps each metadata object at 8 KiB. `CollaborationResult` is only
a local pairing of generated response metadata with text extracted from A2A
parts. The CLI and model tools render readable lowercase status labels.
`no_new_context` remains a local tool outcome derived from the Delegator's BUSY
memory, not a protobuf status or wire outcome. `Begin` starts fresh memory for
each invocation; outgoing siblings share that invocation's progress without
sharing a mutable path. A successful read of a newer lender-selected snapshot
clears suppression through `Borrowed`.
Deadline expiry and cancellation are ordinary context errors. The transport's
8 KiB metadata bound still applies to the encoded ancestry and context hints.

| Domain error | Connect code |
| --- | --- |
| Invalid parameters | `invalid_argument` |
| Extension inactive | `failed_precondition` |
| Request ID conflict | `already_exists` |
| View unavailable | `not_found` |
| Storage failure | `internal` |
| Unsupported representation | `unimplemented` |
| Authentication failure | `unauthenticated` |
| Oversized RPC message | `resource_exhausted` |

Provider/internal errors are masked. Cancellation and deadlines retain their
Connect codes. The domain client preserves `errors.Is` for known service errors
and preserves the original Connect error for code inspection.

References: [Buf generation configuration](https://buf.build/docs/configuration/v2/buf-gen-yaml/)
and [Connect Go guide](https://connectrpc.com/docs/go/getting-started/).

## Migration from the removed tape-view transport

The handwritten tape-view JSON-RPC transport and `/tape-views` route have been
removed. `Client`, `NewClient`, `Advertise`, and `NewViewHandler` now use Connect
exclusively; `ViewPath` names the generated service path. Re-advertise peer cards
with `publicBaseURL + ViewPath`. Old `/tape-views` endpoints are rejected by the
client; there is no compatibility fallback. The intermediate `ConnectClient`,
`NewConnectClient`, `AdvertiseConnect`, `connectEndpoint`, and `ConnectViewPath`
names are removed in favor of the single API. Grant journal encoding remains compatible. Tool decisions and pages use
ProtoJSON. Core A2A still uses the official SDK transport.
