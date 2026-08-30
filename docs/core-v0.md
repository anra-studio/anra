# Anra Core V0

This repository implements the approved Core V0 design from Raft task #16.

Its durable state is an append-only lifecycle trace projected into pending,
active, closed, blocked, and failed work. One Core is bound to one principal;
one activation is active at a time; pending events are selected in Tape
sequence order. The full loop integration waits for tape-go's runtime-neutral
one-turn Runner façade.

The working design and implementation plan are maintained with task #17.
