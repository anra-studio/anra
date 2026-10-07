# Tape-view API contract

`anra/a2a/tape/v1/tape.proto` defines Anra's permissioned tape-view extension:
`Borrow`, `Get`, `Read`, and `Release`. The official A2A core remains in
`github.com/a2aproject/a2a-go/v2`; do not copy its schema or regenerate its
messages here. Discovery still fetches the official JSON Agent Card.

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
generation consistency, and all Go tests. Installation and the first schema
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
`"0"` for an empty snapshot. Ranges are half-open; `bounds` is optional. ProtoJSON
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
Validation applies to requests only. Service authorization, snapshot bounds,
journal integrity, export policy and client provenance checks remain separate.
Local `Config`, `BorrowRequest`, and `ReadRequest` use go-playground/validator/v10
tags through a shared validator initialized before concurrent use. A `maxbytes`
rule matches the protobuf byte limits, and a struct-level EntryRange rule uses
tape-go's arbitrary-precision sequence comparison. Configuration checks include
required dependencies and distinct source/journal owners. Local validation
errors support `errors.Is(err, ErrInvalidParams)` and `errors.As` to retrieve
`validator.ValidationErrors`.

HTTP transport limits encoded/decompressed RPC messages to 1 MiB. Borrowed pages
retain source, view, snapshot, and sequence provenance checks in `BorrowTool`.
The lender's selected turn/anchor range defines the complete view size. There
is no separate maximum-entry cap or truncation of that range; pagination only
splits its transport. `Eligible`/`Policy` supplies the turn boundaries.

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
names are removed in favor of the single API. Grant journals and local tool
result encoding are unchanged. Core A2A still uses the official SDK transport.
