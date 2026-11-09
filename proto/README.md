# Anra protobuf contracts

`anra/model/view/v1/view.proto` owns shared Source, EntryRange, Ref, Lease,
BorrowRequest, Decision, Provenance, public entry payloads and Quote values.
Both MCP and A2A consume this model through `pkg/model/view`. Ref contains
Source and Scopes only; it has no stored snapshot-head or finalized-end field.

`anra/a2a/tape/v1/tape.proto` contains the optional TapeViewService and its RPC
response/request envelopes. Borrow accepts the shared business request; Read
returns a shared Quote. `anra/a2a/collaboration/v1/collaboration.proto` imports
the common Ref for delegation and BUSY hints, without granting access or adding
another RPC service. Core A2A remains in the official a2a-go/v2 SDK.

`anra/peer/v1/peer.proto` declares canonical Peer/Skill metadata. Registry RPCs
and presence live in `anra/registry/v1/registry.proto`. The registry persists
ownership and metadata; discovery uses its generated client. A2A Agent Cards
are produced at the adapter boundary, not stored as registry identities.

The module uses versioned packages under `proto/`, generated messages and
Connect bindings under `gen/`, pinned local generators, and Buf v2 configs.
Generated files are committed and never edited by hand.

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

TapeViewService supports Borrow, Get, Read and Release over Connect and generated
Vanguard REST routes. Identity and extension negotiation precede schema validation
and dispatch. Local Approve/Reject/Revoke and controller borrowing are shared-flow
operations, not remotely exposed management RPCs. Public types import only the
neutral model schema; tape-go View bindings and the `pkg/flow/grant` handler do not depend on A2A requests.

Sequences are nonzero canonical ASCII decimal strings of arbitrary precision.
EntryRange uses `[seqS,seqE)` and its CEL rule compares length and lexical order,
never uint64. Ref requires Source and one or more Scopes. The view owner enforces
canonical scope order, non-overlap and actual commitment. Native Anra resolves
whole completed windows; it rejects cropped or reordered references. A Ref is
not proof of permission. Lease binds its caller and Ref with ID and expiry.

A decision contains a lease exactly when granted. Empty/uncommitted views are
not granted. Borrow request IDs are caller-scoped and idempotent; changed input
replays its original decision. Reads check lease ownership, expiry, release/revocation and complete
scope contents. Multiple scopes are read in order, skipping gaps. Each complete
Quote retains its channel, caller, lease ID and Ref. Quote itself does not
restrict channel to borrowed. ReadRequest carries only lease_id; ReadResponse
contains one complete Quote. The removed pagination field numbers and names are
reserved. ReadView validates exact complete sequence coverage across all scopes.

Protobuf field constraints use Protovalidate; non-Protobuf construction inputs
use validator tags. Network adapters validate messages at their boundary; the
shared service enforces real commitment and authorization transitions. Trusted
view/exporter results are not recursively revalidated as a second format pass.
No custom MCP validation rules are needed by independent validators.

Payload carries mediaType and exact JSON bytes rather than Struct/Value, keeping
opaque business number lexemes intact. ProtoJSON encodes bytes as base64. The
shared JSON Quote uses raw content JSON; the ADK tool keeps content as text to
avoid its map conversion coercing numbers. No Summary or Projection schema is
present. There is no fixed peer/MCP body cap or per-entry cap. Complete reads
retain entire media entries; field constraints and the registry's metadata
message limit remain independent policies.

## Collaboration metadata

DelegationMetadata carries rootRequestId, deadline, immutable ancestors and
ungranted Ref hints. ResponseMetadata carries Status, RetryHint and optional Ref.
Generated enums use their ProtoJSON names; ordinary SDK answer text stays in
standard A2A parts. The Delegator model owns path forks, deadline propagation and
BUSY progress; the adapter only encodes and decodes these messages. Reading
newer completed scopes can clear suppression; receiving a hint does not.

## Errors

| Domain condition | Connect code |
| --- | --- |
| Invalid parameters | invalid_argument |
| Inactive extension | failed_precondition |
| Conflicting request ID | already_exists |
| Unknown, foreign or inactive lease | not_found |
| Storage failure | internal |
| Unsupported content | unimplemented |
| Missing authenticated identity | unauthenticated |

Private storage errors and credentials are not returned to remote callers.
The domain client retains errors.Is categories and the original Connect error.
MCP maps credential failure to 401/Bearer challenge, credential-backend failure
to 503, and lease rejection during a tool read to permission_denied.
See the [MCP contract](../docs/mcp.md) and [A2A adapter](../docs/a2a-adapter.md).

This revision directly replaces the old View/TapeView and grant-store formats;
it adds no migration, compatibility alias, old decoder or fallback transport.
