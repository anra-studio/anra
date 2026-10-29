# Anra

> Anra do not intend to accept any PRs , it is my own agent.

Anra is another multi-agent (or agent team) implementation upon the [tape-go](https://github.com/scbizu/tape-go), which is inspired by [tape](https://tape.systems).

Agents can communicate with peers and share relevant context with permission.

- [Application architecture and embedding](docs/architecture.md)
- [Command line usage](docs/cli.md)
- [Agent team design](docs/agent-team.md)
- [A2A adapter](docs/a2a-adapter.md)
- [HTTP and REST routes](docs/http.md)
- [API and development workflow](proto/README.md)
- [BDD end-to-end scenarios and testing policy](docs/testing.md)

## Say hello between two running agents

Build the CLI with `mise run build-cli`. Set `OPENAI_API_KEY` for both agent
servers. For a custom OpenAI-compatible service, set `OPENAI_BASE_URL` to its
base URL (including `/v1` if required) and configure its model name. Then start
the shared directory:

```sh
./anra registry --listen 127.0.0.1:9090
```

Start Alice and Bob in separate terminals using [anra.example.toml](anra.example.toml):

```sh
./anra --config anra.example.toml serve --agent alice
# Another terminal:
./anra --config anra.example.toml serve --agent bob
```

Each server registers its Peer metadata with the lobby and publishes an A2A card. Alice's configured `endpoint`
chooses an available port; Bob's is set to `127.0.0.1:8080`. With both
running, ask either agent to consult the other from another terminal:

```sh
./anra --config anra.example.toml ask --agent alice "Discover Bob and ask him to say hello."
./anra --config anra.example.toml ask --agent bob "Ask Alice how her work is going."
```

The CLI contacts the selected agent's running model. That agent decides whether
to consult a peer through its built-in tools and combines the findings into an
answer. Each agent persists its own conversation and tools in `.anra/`. No peer
addresses are needed in the config.
The registry preserves Peer identities, addresses and skills in `.anra/registry.db`.
Ctrl-C marks presence offline; lost contact marks it unknown. Explicit retirement
uses `anra registry forget <id>`. See the [registry contract](docs/registry.md).

The registry and agent endpoints run inside a trusted network. No shared peer
token is required. Requests carry the caller's agent identity; team members are
trusted not to impersonate one another. TapeView borrowing still enforces grants,
selected ranges, expiry, and release for each caller.

Each tape admits one reasoning invocation at a time. A second request receives
BUSY immediately. A peer can borrow a lender-selected TapeView of a completed
invocation while its owner keeps thinking, then continue its own model loop.
Execution anchors use `anra.dev/anchor/v1`; interrupted work is closed as
abandoned at restart. See the [collaboration specification](docs/designs/anchor-tape-view-collaboration-spec.md).

Agents run tape-go's ADK runtime with the OpenAI-compatible provider. Tools are built in;
the CLI and config do not accept injected tools. Agent settings come only from
the required `--config` file and are read at startup; restart a server to apply
a change. `[lobby].address` and each agent's `data-dir`, `provider`, `timeout`,
and `endpoint` are required. The E2Es use a deterministic local model HTTP fixture
and need no real LLM key. See [command line usage](docs/cli.md).
