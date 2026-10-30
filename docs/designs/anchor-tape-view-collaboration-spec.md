# Anchor-backed TapeView collaboration

Status: implemented. This replaces the publication object in the earlier
[BUSY design](anchor-busy-collaboration-spec.md) and its implementation guide.

## Borrowable object

The lender selects a bounded window of its tape. There is no separate summary,
publication record, coverage object, or copied answer. Internally, completed
windows use tape-go's `view.TapeView`. The generated wire `TapeView` carries a
source identity, a committed snapshot head, and a half-open scope. It describes
context; it carries no grant ID or permission.

The default selection is the latest successfully completed turn. If its
start anchor is S and its successful close anchor is C:

- The native window scope and granted range are `[S+1,C)`.
- `snapshotHead` is C, the durable commit boundary.
- The range contains the user/model/tool exchange, excluding both control anchors.
- Earlier completed windows remain resolvable by their exact descriptor.
- A failed, cancelled, or abandoned turn does not replace this selection.

For example, a run with start 18, user 19, tool call 20, tool result 21, assistant
22, and successful close 23 yields scope `[19,23)` at snapshot 23. Appending a
new start and more entries cannot change that window.

This is the latest turn's context, not the whole accumulated tape. The
lender controls eligibility. No borrower-provided range, purpose, query, or
reference can expand a completed window into unfinished work.

## Execution and recovery

`anra.dev/anchor/v1` defines `tape.initialized`, `turn.started`, and
`turn.closed`. The start records its turn identity, owner epoch, and
optional `baseView`. The close records the matching start and its outcome.
There is no `context.published` record and no answer embedded in the close.

Only one reasoning turn enters a tape at a time. Its start must commit
before the model runs; its close must commit before the answer is acknowledged.
An append with an uncertain outcome fences further execution. Shutdown drains
active reasoning before closing storage.

Recovery replays anchors to rebuild the source identity, native window index,
latest successful view, and failed/aborted intervals. An orphan start receives
one abandoned close. Its incomplete history/state never enters later reasoning
or a committed context window. Existing immutable grants remain readable after
restart, and the source tape incarnation stays stable.

The old publication wire/record format has no compatibility decoder. Existing
publication-format tapes and grant stores require migration or fresh storage.

## Wire contract and grants

`TapeView` is defined in `proto/anra/a2a/tape/v1/tape.proto`:

```json
{
  "source": {"agentId": "alice", "tapeId": "stable-tape-uuid"},
  "snapshotHead": "23",
  "scope": {"seqS": "19", "seqE": "23"}
}
```

Sequences remain arbitrary-precision canonical decimal strings. Generated
validation requires a nonempty, ordered scope ending at `snapshotHead`.

`BorrowRequest.view` optionally pins an exact descriptor for protocol clients.
The lender verifies its source, tape incarnation, committed head, and exact
scope. Without a descriptor it selects the latest eligible window. `bounds`
can only constrain eligibility; an incompatible range yields denial.

Cards advertise `viewBorrowVersion: 1` in the TapeView extension. A protocol
client refuses to send an exact-view request to a lender lacking that version.
The canonical internal Peer adapter declares the version it implements.

`Borrow` issues the existing `View` grant envelope with requester-bound ID,
source, ranges, snapshot head, and expiry. A TapeView hint never transfers that
grant. Before reading an explicitly requested snapshot, the client verifies
exact source, scope, and head equality. Read pagination also validates those
identities and consumes each granted entry exactly once, in order.

Request retries remain idempotent. Changing any request field under an existing
request ID conflicts. Another requester needs a separate grant and cannot read
or release someone else's. Expiry, release, and local revocation end access.
The journal persists those decisions independently of the source tape.

## Content export

`a2a.ContextExporter` reads the entries already stored in the granted range.
It whitelists user, assistant, tool-call, and tool-result content. Structured
model content is encoded as
`application/vnd.anra.context-entry+json;version=1`. Plain text entries retain
the existing text exporter representation.

ADK event IDs, authors, execution metadata, state deltas, arbitrary custom
extensions, thought parts, and execution anchors are not exported. Tool calls
and results are data in the borrower's tool response; borrowing never executes
them. Completed prompts and tool arguments/results are part of the context the
lender shares with trusted peers.

The ordinary 8 KiB exported-entry and page/body limits still apply. The previous
2 KiB publication-answer eligibility limit is removed. An unsupported or
oversized entry fails export rather than silently truncating or changing scope.

## Nonblocking collaboration

When reasoning is occupied, another request receives `STATUS_BUSY`, optional
`view`, and `RETRY_HINT_AFTER_NEW_VIEW`. That request is not admitted, appended,
queued, or automatically retried. A generic A2A caller receives clear unaccepted
text without extension hints.

Borrow selection and reads use committed local data through the independent
TapeView service. They do not acquire reasoning admission or call the lender's
model. The caller can borrow completed context and continue its own reasoning
while the lender remains occupied.

`Delegator` retains roots, deadlines, and immutable sibling paths. Its
turn-local BUSY memory prevents repeated reasoning calls to an unchanged
peer. Reading the same or an older view keeps suppression. A newer committed
snapshot, or a different source incarnation, permits one further attempt. A new
turn starts fresh BUSY memory. Deadlines and local model-turn limits still
bound execution; there is no hop budget or async task scheduler.

The built-in `borrow_peer_context` tool accepts only `peer`, `requestId`,
`purpose`, and `query`. Selection stays with the lender. Supplied context hints
are visible through discovery and borrowed under the recipient's own identity.

## Implementation and checks

- `pkg/model/anchor`: execution record codec; no publication data model.
- `pkg/agent/execution.go`: native TapeView index, admission, close, and recovery.
- `pkg/a2a/view.go`: exact snapshot selection, capability declaration, and content export.
- `pkg/a2a/service.go` and `pkg/model/journal`: grants and their durable lifecycle.
- `pkg/model/delegator`: per-turn path and BUSY progress.
- `pkg/agent/tools/{peer,borrow}.go`: ADK tool definitions using discovery,
  Delegator transitions, and shared A2A messaging/borrowing clients.
- Generated tape/collaboration protobufs: snapshot descriptors and metadata validation.

Acceptance is executable in `tests/e2e/features/collaboration.feature`. Scenarios
exercise real HTTP, temporary disk storage, deterministic model fixtures, tool
execution, long answers, immutable windows across writes/restarts, denied scope
expansion, requester isolation, grant termination, cyclic reasoning, recovery,
fencing, and shutdown. Run `mise run test`; use the E2E race check after changing
concurrent admission or view capture.
