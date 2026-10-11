# Anra

> Anra do not intend to accept any PRs , it is my own agent.

Anra is another multi-agent (or agent team) implementation upon the [tape-go](https://github.com/scbizu/tape-go), which is inspired by [tape](https://tape.systems).

Anra uses tape-go's bbolt backend for runtime history and a bbolt database for
indexed grant state and audit records. Grant queries run on demand without
startup replay or an in-memory grant projection. Source storage is passed
directly as `*bbolt.Bbolt`.

Peer communication and permissioned tape borrowing are implemented in `pkg/a2a`.
See [A2A integration](docs/a2a-integration.md) for setup, wire contract, and limits.
The tape-view API has [protobuf and Connect bindings](proto/README.md), generated
with pinned mise/Buf tools. Service and Client use the generated protobuf
messages directly. Run `mise install`, `mise run proto-generate`, and
`mise run test` from the repository root.

See [Agent team design](docs/agent-team.md) for the runtime and collaboration boundaries.
