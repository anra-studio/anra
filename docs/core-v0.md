# Anra Core V0

This repository implements the approved Core V0 design from Raft task #16.
The reviewed design revision is SHA-256
`f2c9769743df392014dcaea8d8074cb93f2d0a99eee04728a8c8262bec6f40c5`;
its implementation plan revision is SHA-256
`2e73cc5f0164e511e07fd8177decf42415bcf3cc6d32e570b71efbb96e78aad5`.

Its durable state is an append-only lifecycle trace projected into pending,
active, closed, blocked, and failed work. One Core is bound to one principal;
one activation is active at a time; pending events are selected in Tape
sequence order. The full loop integration waits for tape-go's runtime-neutral
one-turn Runner façade.

The façade is supplied by `github.com/scbizu/tape-go/pkg/runner` at tape-go
commit `3b2ddbc850af2880930ab3bc688ba707bf1eb7af`. Anra may only use its
neutral `Runner.RunTurn`, `Request`, `Response`, `Message`, and `ToolSpec`
types. `pkg/agent`, Google ADK, and provider-specific types must not enter an
Anra package.

The working design and implementation plan are maintained with task #17.
The initial repository commits are `6978030` (package scaffold) and `968ef1e`
(LifecycleEntry CustomEntry codec).
