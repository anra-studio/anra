# Anra agent team

Anra composes agents on tape-go, pinned to `v0.2.0-anra.2`. Each member owns an identity, role instructions,
a Runner, tools, and a Tape. Members communicate through A2A and can borrow
another member's Tape view when authorized.

## Member runtime

`agent.New(principal, storage, options...)` configures a member. Storage must
be an initialized tape-go `*bbolt.Bbolt`, or a decorator implementing
`storage.Unwrapper` whose underlying backend is bbolt. Other backends are
rejected. The caller owns initialization and closure.
`Run(ctx, input)` stores the user message and calls tape-go's neutral
`Runner.RunTurn`. The Runner returns either final output or tool calls. Tool
calls and results are stored on Tape before the next turn. Subsequent runs
rebuild conversation context from those records.

Capabilities implement `agent.Tool`; model providers implement tape-go's
`runner.Runner`. Anra uses Runner messages, tool calls, and results directly.
It does not maintain a separate Event, Activation, Decision, or Effect model.
The former `Submit` and `Inspect` lifecycle APIs have been removed; callers use
`Run` for execution and Tape storage for history queries.

Conversation reconstruction does not resume interrupted tool execution or
provide exactly-once side effects. A failed Run returns its error to the caller.

## Collaboration

`a2a.NewExecutor` exposes a member's Run method through A2A. Peer messaging and
permissioned Tape borrowing belong to `pkg/a2a`; members need no shared team
Tape or mandatory coordinator. A coordinating member can use tools to request
work from peers.

Tape storage, View handling, and memory checkpointing/retrieval belong to
tape-go. Its decision extension can be configured at the storage boundary;
Anra does not duplicate its memory policy or index.

See [A2A integration](a2a-integration.md) for the protocol and permission model.
