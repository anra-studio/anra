# Implementing anchor-backed BUSY collaboration

> Historical publication design. Superseded by the implemented
> [anchor-backed TapeView specification](anchor-tape-view-collaboration-spec.md).
> Current code lends committed conversation windows; it has no publication
> records, publication exporter, or intermediate publication tool.

Historical implementation reference for [issue #5](https://github.com/anra-studio/anra/issues/5).
The [technical specification](anchor-busy-collaboration-spec.md) defines behavior;
this guide maps it to the inspected Anra code. Function/type names proposed here
are design sketches; consult the implemented APIs for exact signatures.
The first implementation publishes final answers only, as permitted by the spec.
The optional intermediate publication tool remains deferred.

## 1. Start from the actual workspace

The design was prepared against an actively edited workspace. Record the
implementation base commit and inspect outstanding changes before editing.
Preserve unrelated CLI/config work. Do not restore old flags, environment
bindings, provider wrappers, or a forwarding endpoint to make an old test pass.

The owner must call `Anra.Shutdown(ctx)` before closing storage: the A2A
executor can outlive a cancelled HTTP handler. Shutdown cancels the active run
and waits for its terminal cleanup without holding the admission lock.

Read:

- `AGENTS.md`: committed tests are executable BDD E2Es only.
- `pkg/agent/runtime.go`: the full-run mutex and ADK turn loop.
- `pkg/agent/events.go`: the adapter seam for legacy and ADK tape entries.
- `pkg/a2a/executor.go`, `messaging.go`, and `peer_tools.go`: text-only peer calls.
- `pkg/a2a/service.go`, `selection.go`, and `connect.go`: grants, export, and RPCs.
- `pkg/model/journal`: durable grant/request/release records.
- `cmd/serve.go`: currently mounts messaging, not the full borrowing service.
- `proto/anra/a2a/tape/v1/tape.proto` and `proto/README.md`: wire contract/workflow.

Verify the pinned dependency APIs. In the inspected tape-go version:

- `Store` returns the persisted entry and assigns its ID when the input ID is zero.
- `Anchors` enumerates persisted anchors by owner/session.
- `NewAnchor` produces an entry; use its returned stored ID for references.
- `anchor:custom` and `anchor:handoff` have distinct meanings.
- `TapeStorage` has no conditional claim-if-idle API.
- `TapeAdapter.ContextWindow` skips anchors, but its session-loading fallback
  can still project entries without `adk:event` into ordinary model events.
- The existing borrowing reader requires complete contiguous sequence coverage;
  its default exporter does not support custom anchors.

## 2. Suggested code changes

| Area | Work |
| --- | --- |
| `pkg/agent/execution.go` | Decode/replay control anchors, manage admission, persist start/close, recover orphans. |
| `pkg/agent/publication.go` | Validate and append publications; expose immutable projection snapshots. |
| `pkg/agent/runtime.go` | Wrap ADK runs with admission/termination; retain the native ADK loop. |
| `pkg/agent/events.go` | Exclude control anchors and aborted turn intervals from ADK history/state projection. |
| `pkg/agent/tools.go` | Register the fixed publication tool when intermediate publication is enabled. |
| `pkg/a2a/executor.go` | Convert typed busy errors into negotiated collaboration results. |
| `pkg/a2a/messaging.go` | Exchange and validate typed results and bounded delegation metadata. |
| `pkg/a2a/peer_tools.go` | Discover peers, call Delegator transitions, and return structured tool outcomes. |
| `pkg/model/delegator/delegator.go` | Own root/path/deadline transitions and turn-local BUSY progress; expose metadata snapshots to adapters. |
| `pkg/a2a/service.go` / `selection.go` | Select publications, pin grants to their entry snapshots, retain old grant reads. |
| `pkg/a2a/publication.go` | Whitelisted public projection/export of publication anchors. |
| `proto/.../tape.proto` | Add `AnchorRef` and optional `BorrowRequest.publication = 5`. |
| `cmd/serve.go` / bootstrap | Recover before serving; own the stable source and grant-journal lifecycle; mount borrowing. |
| `cmd/ask.go` / CLI docs | Report BUSY distinctly from an answer; keep model-directed collaboration. |
| `cmd/send.go`, `cmd/outbound.go` | Remove the forwarding-only command/endpoint. |

These are cohesive responsibilities, not a requirement to create every suggested
file or a new configuration wrapper for each function. Keep interfaces small.

## 3. Phase 1: codecs and replay

Implement the versioned custom-anchor codec first. Use canonical tape sequence
types internally and generated protobuf references at the RPC boundary. Keep
local owner IDs separate from external source IDs.

The projection needs only:

```go
// Sketch: concrete fields must use tape-go sequence types.
type executionProjection struct {
    source       SourceIdentity
    active       *turnRecord
    publications []publicationRecord
    outcomes     map[string]terminalRecord
}
```

An active record contains the turn UUID, owner epoch, persisted start
sequence, and base publication reference. Do not duplicate a durable scheduler
state in another database. A map/index is a replayable optimization.

Replay rules:

1. Ignore unrelated custom anchors; do not reinterpret handoff anchors.
2. Validate the anchor schema (`anra.dev/anchor/v1`) before applying a recognized record.
3. A start must not overlap another unclosed start.
4. A publication must refer to an existing start and valid earlier coverage.
5. A close must match its start and turn. At most one authoritative close
   exists for a start; contradictory duplicates are corruption.
6. Apply the optional terminal publication in the same replay step as closing.
7. Keep aborted intervals for the ADK projection filter.

Persist/reuse the stable source identity before creating externally visible
references. If an existing grant journal already binds a source, reconcile that
identity explicitly; never generate a replacement tape ID that invalidates old
grants silently. A mismatched source is a startup error, not a migration guess.

Use temporary local unit tests to explore malformed records, large sequences,
replay, and ambiguous storage failures. Remove them before commits. Retain
observable replay/restart guarantees in BDD scenarios.

## 4. Phase 2: replace the blocking full-run mutex

Introduce one short control mutex and a cached projection. Do not merely move
the current mutex into a new wrapper that still spans the remote call.

Pseudocode:

```text
admit(ctx):
    lock control
    if recovering, fenced, or storage outcome uncertain:
        unlock; return unavailable
    if projection.active exists:
        copy immutable busy snapshot
        unlock; return typed BUSY(snapshot)
    create turn UUID and epoch-bound start payload
    append start with ID zero; obtain persisted start reference
    if append outcome cannot be established:
        fence; unlock; return storage error
    install active record in projection
    unlock
    return admitted turn

run(ctx, input):
    validate input/runtime configuration
    turn = admit(ctx)
    if not admitted: return its result
    run the existing ADK iterator with turn context
    capture answer or model/tool/cancellation failure
    derive an optional bounded final-answer publication on success
    close under a fresh bounded cleanup context
    if close commit fails: fence; return error
    return the answer/error only after terminal commit
```

Use a single terminal-handling path so early returns from ADK cannot forget the
close. Ensure error wrapping preserves `errors.Is` for BUSY, cancellation, and
storage errors. A return value assembled before deferred cleanup must not report
success when the cleanup discovers a close failure.

`BusyError` is an application outcome, not a transport outage. It should expose
an immutable details snapshot through a narrow interface that `pkg/a2a` can
inspect without importing `pkg/agent`. Avoid creating an import cycle or a
scheduler package. Do not put sensitive turn data in `Error()` text.

There may be a brief delay for a local anchor transaction. There must be no
wait on another turn's completion channel, model call, or network request.
The admission gate must be shared by all reasoning entry points. Retire or guard
any alternative entry point capable of appending user/model entries around it.

## 5. Phase 3: publish and export completed findings

Final-answer publication is built into the terminal anchor lifecycle. Eligible
completed answers within the export size limits publish by default. The trusted
peer transport identifies callers, and TapeView Borrow issues requester-bound
grants for the publication projection. No runtime sharing callback is required.
Unfinished reasoning and raw control records remain private.

Then add intermediate `publish_context` if needed for useful delegation. This is
a built-in tool, not a configurable injected capability. The tool must:

- Validate the requested summary and bounded source coverage.
- Check that coverage exists, belongs to this source, precedes the new anchor,
  and contains completed facts eligible for publication.
- Reject permission expansion and references to another turn's private
  work merely because the model supplied a sequence number.
- Append a publication anchor and return its actual persisted reference.
- Return failure without a reference when append fails.

Store the public summary and provenance coverage inside the versioned publication
payload. The exporter projects those fields only. For a publication P:

```text
granted range  = [P, P.Next())
snapshot head  = P
exported seq   = P
exported kind  = actual anchor:custom
public payload = summary + source coverage
```

Preserve the original entry timestamp. Do not pass raw control JSON or arbitrary
extensions to the exporter. The range contains the publication anchor only;
`covers` is descriptive provenance, not an implicit grant of the covered source
entries. Do not implement a filter that silently drops lifecycle entries from a
larger raw range: it would break the existing reader's contiguous coverage check.

Keep typed public payload serialization stable. Register/advertise its media
type, enforce existing payload limits, and leave previously issued text grants
readable with their original projections.

## 6. Phase 4: publication-aware Borrow

Add the optional proto field, generate bindings, and update validation before
changing the service. Preserve field numbers 1..4 and all existing limits.
`AnchorRef.source` is required and `seq` is a positive decimal string.

The service needs a local publication resolver/selector that does not acquire
reasoning admission or invoke a model. Do not assume the existing general
`BestRangePolicy.Ranker` is automatically safe: custom ranking can call external
services. The initial publication selector should be deterministic and local.

Pseudocode for a new publication request:

```text
validate request and caller
check the durable request journal
if an identical recorded request exists: return recorded decision
if a changed request ID exists: conflict
resolve the requested publication, or select one eligible publication
if none is eligible: record/return denied, with no view
check caller authorization and optional bounds against [P, P.Next())
validate export using the publication exporter
create a requester-bound grant with source, range [P, P.Next()), head P, expiry
commit the decision using the existing journal rules
return the recorded decision
```

The existing service currently uses the live head for `Policy.Select` and
`decide`. Refactor this deliberately: publication-backed decisions use P as their
snapshot ceiling. Keep live-head handling only for an explicitly supported
legacy raw-range policy. Require the advertised `publicationBorrowVersion: 1`
capability before a client sends the optional reference, and verify the granted
source/range/head match that reference before reading. Do not retry through an
older lender that could ignore the additive field. Do not change stored decisions during a retry.

Borrowing, reading, and release remain independent of the execution gate. Copy
publication state under a short lock and release it before grant-journal work.
A publication is immutable, so a copied reference remains valid after the owner
publishes something newer. If the owner closes a turn concurrently, a
request may use its captured older publication; it must not silently switch
snapshots halfway through approval.

On `serve`, open the grant journal, bind it to the stable source, advertise the
TapeView extension/media types, and use the existing full peer handler. Close
the service/journal before closing the underlying tape. Do not expose arbitrary
filesystem paths through protocol metadata.

## 7. Phase 5: typed A2A results and caller behavior

Add collaboration extension metadata to the existing SDK messages. Keep
`ContextID`/`TaskID` out of local tape identity selection. Preserve official SDK
transport and native core message types.

Define the metadata contract in
`proto/anra/a2a/collaboration/v1/collaboration.proto`. Generate
`DelegationMetadata`, `ResponseMetadata`, `Status`, and `RetryHint`; import the
existing TapeView `AnchorRef`. Use Protovalidate for structural validation and
ProtoJSON for SDK metadata objects. No new RPC service is needed.

The local client result pairs generated metadata with standard A2A text:

```go
type CollaborationResult struct {
    *collaborationv1.ResponseMetadata
    Answer string
}
```

Parse/validate by status. A completed answer must be nonempty; a busy result must
not be treated as an answer. Errors stay errors. Cap metadata sizes and deadline
caps; do not log arbitrary metadata that might contain private context.

Remove the handwritten delegation DTO, status constants, JSON codecs, and
structural validation branches. Change all callers to generated types directly.
Wire enums use their canonical ProtoJSON names; there is no lowercase wire
compatibility layer. The CLI and tools render readable labels. Keep deadline
capping, ancestry, and turn-local BUSY state in the Delegator
model. Keep identity in the transport adapter. Tools call model transitions
rather than changing protobuf fields, context keys, or BUSY maps themselves.

The model API has these boundaries:

- `Accept` validates and owns inbound metadata; `Begin` creates fresh turn
  progress state and caps its inherited deadline.
- `FromContext` resolves the turn model, creating a root for external
  clients such as the CLI. `Metadata` returns an owned transport snapshot.
- `Next` checks suppression and deadlines, then forks one path and appends the
  sender. Sibling calls retain the same root and independent paths.
- `Observe` records BUSY outcomes. `Borrowed` permits another reasoning attempt
  only after a newer snapshot has actually been read.

Wire `NewExecutor` to detect the typed busy error and obtain a committed snapshot.
Include a committed publication hint only for an identified peer. A busy hint
can be omitted. Generic A2A callers receive a clear unaccepted
message; Anra peers negotiate and consume the structured status.

Change the message client to return a typed result. Audit every caller: `ask`,
peer tools, and tests must not continue assuming every response is answer text.
Have `send_peer_message` return BUSY as a structured tool result so ADK stores it
and continues the caller's model loop.

Add turn-local attempted-peer state keyed by `(peer, publication)` for
BUSY outcomes. A second reasoning attempt at the unchanged state returns local
`no_new_context` without HTTP. It does not become successful merely because the
new prompt differs. A new root turn can try again. Do not invent an
unbounded backoff loop, polling goroutine, or queue.

Propagate a root correlation ID, capped deadline, and ancestry on each edge.
Deadline expiry and cancellation are ordinary context errors and stop an edge
before sending. There is no hop counter or ancestry-length limit. Keep the local
model-turn limit and encoded metadata size bound.

For context fallback, extend the fixed borrowing tool to resolve a named peer's
advertised TapeView endpoint. Accept only peer, request ID, purpose, and query;
the lender selects its latest eligible publication without entering reasoning.
Use the returned grant's source and snapshot to detect progress after a successful
read. An unchanged publication must not clear BUSY suppression. The current
single `WithBorrowClient` dependency does not implement this discovery by itself.
Borrow under the caller's own identity and record returned grants/provenance in
its tape. Forwarding a reference never forwards permission.

## 8. Phase 6: close recovery and valid ADK history

Run recovery before card registration or HTTP readiness. The file must already
be exclusively owned. Append an abandoned close for each valid orphan from a
previous epoch; do not mutate/delete the orphan start or the intervening entries.
If recovery cannot commit, do not publish a healthy server.

Construct the TapeAdapter only after recovering the projection. For each failed,
cancelled, or abandoned turn, exclude its raw ADK event interval from new
model/session state. Preserve the physical entries for diagnostics and provenance.
Preserve explicitly committed publication anchors for borrowing. Keep completed
turn history in order.

Filter both ordinary event content and session state deltas. An existing hydrated
adapter may already hold partial state; rebuild it after a failed turn
before starting another one. Test unmatched function calls explicitly: preserving
bad tool-call history can make the next provider request invalid even if the
admission gate has been reopened correctly.

Legacy tapes without execution anchors retain their supported legacy message
projection. Do not auto-publish their historical content or fabricate completion
records for arbitrary old intervals. Recovery of malformed pre-feature tool
history is a separate migration concern, not a promised exactly-once replay.

Fault points to investigate locally:

| Failure point | Expected durable outcome |
| --- | --- |
| Before start append | No admitted turn and no model call. |
| After start commit, before first model call | Recoverable open start; later abandoned close. |
| During peer call | BUSY remains readable; deadline/cancellation can end the active turn. |
| After intermediate publication | That publication survives even if later work aborts. |
| After final answer entry, before close | No successful acknowledgement; recovery does not publish the unfinished tail. |
| After close commit, before HTTP response | Completed tape; caller delivery is uncertain, with no exactly-once guarantee. |
| During terminal/recovery append | Fence admission until storage reconciliation succeeds. |

Use injected storage errors only as temporary investigation aids where needed.
The durable acceptance suite must cover observable process kill/restart,
cancellation, and recovery through public APIs.

## 9. Phase 7: remove the forwarding bypass

Remove the custom `/anra/send` route, its client, and the forwarding-only CLI
command. Remove `Exchange` only after checking for supported callers. Do not
silently change a recipient flag into model instructions.

Update examples to ask the source agent a question. A deterministic model fixture
must prove that Alice can choose no peer, or explicitly select Bob and synthesize
his response. Agent discovery remains name/card-based; no fixed peer address or
shared token is introduced.

Replace the old forwarding acceptance scenarios with model-driven scenarios
that preserve their observable guarantees: two serving agents, restart,
source/destination tape provenance, missing destination behavior, and concurrent
opposite-direction roots. Do not just delete the assertions that expose the old
path. New control anchors change raw entry counts: update tape inspection to
assert both valid lifecycle records and the expected projected conversation,
rather than weakening it to ignore every new entry.

## 10. BDD implementation checklist

Suggested feature: `tests/e2e/features/collaboration.feature`.
Use fixtures with channels/barriers at model requests and peer calls. The agent
must be demonstrably occupied before making a second request. Bound every wait
by the scenario context; a timeout is a failed assertion, not an expected success.

```gherkin
Scenario: A busy agent lends published context without another reasoning run
  Given Alice has published permitted findings at an anchor
  And Alice is reasoning and waiting for Bob
  When Bob requests additional reasoning from Alice
  Then Alice returns BUSY without accepting another turn
  When Bob borrows the indicated publication
  Then the granted view contains only those published findings
  And borrowing finishes while Alice's reasoning remains occupied
  When Bob answers using the available context
  Then Alice can finish her original turn
```

Additional scenarios must cover every item in spec section 12, especially:

- Independent roots consulting one another, not only ancestor callbacks.
- Exactly one winning admission under simultaneous requests.
- No hint/no export when publication permission is absent.
- Stable grant ranges/head/bytes after new writes and restart.
- Different requester, expiry, release, and revocation.
- Local suppression of repeated busy delegation and bounded metadata/deadlines.
- Abandoned-start recovery, unchanged source identity, and a valid next model call.
- Removal of plain forwarding and preservation of source-agent reasoning.

Measure prompt completion/no-second-turn using fixture observations and
model-call counts; do not depend on model text claiming it behaved correctly.
Inspect persisted anchors through public storage reads after stopping the owner
process. Keep sequence arithmetic on the canonical tape type.

## 11. Checks and review order

During implementation:

```sh
mise run e2e
```

For schema changes, follow the existing pinned generation workflow:

```sh
mise run proto-lint
mise run proto-build
mise run proto-generate
mise run proto-check-generated
```

For final review:

```sh
mise run test
git diff --check
```

Run the existing breaking-schema check against the appropriate base when the
protobuf contract changes, as documented in `proto/README.md`. Review generated
changes as generated output; do not hand-edit bindings. Keep all committed test
code under `tests/e2e/`. Run useful temporary package tests explicitly, then
remove them and verify they are not staged.

Review in this order:

1. Tape record schema and recovery invariants.
2. Publication export/provenance and requester permission boundaries.
3. Nonblocking admission under cyclic and independent concurrent calls.
4. Valid ADK history after abort/restart.
5. Wire compatibility, payload limits, and typed outcomes.
6. CLI migration, documentation, and the full E2E contract.

## 12. Completion checklist

- [x] Anchors use ordinary entry sequences; references include a stable source.
- [x] No control lock or write transaction spans model/peer work.
- [x] Busy arrivals append no new user turn and start no model call.
- [x] Close failures fence the runtime; old starts cannot cause permanent BUSY.
- [x] Publications are immutable, bounded, exportable, and borrowed through requester-bound grants.
- [x] Publication grants are exactly one anchor entry, with the matching snapshot.
- [x] Borrowing works while the reasoning gate is occupied and needs no model call.
- [x] Control metadata and unfinished ADK state are not leaked or replayed as answers.
- [x] Repeated BUSY cannot produce unbounded HTTP retries or tool loops.
- [x] CLI requests go through reasoning, with no forwarding-only bypass.
- [x] Existing grant guarantees and deterministic BDD checks pass.
- [x] No scheduler, global peer credential, or injected tool mechanism was added.
