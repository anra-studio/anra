# Anra

> Anra do not intend to accept any PRs , it is my own agent.

Anra is another multi-agent (or agent team) implementation upon the [tape-go](https://github.com/scbizu/tape-go), which is inspired by [tape](https://tape.systems).

Agents can communicate with peers and share relevant context with permission.

- [Command line usage](docs/cli.md)
- [Agent team design](docs/agent-team.md)
- [Peer integration](docs/a2a-integration.md)
- [API and development workflow](proto/README.md)
- [BDD end-to-end scenarios and testing policy](docs/testing.md)

## Say hello between two agents

Build the CLI:

```sh
mise run build-cli
```

In one terminal, start Bob with the explicit no-key echo runner:

```sh
export ANRA_PEER_TOKEN=local-demo
./anra serve --agent bob --runner echo --listen 127.0.0.1:8080
```

In another terminal, send a greeting from Alice:

```sh
export ANRA_PEER_TOKEN=local-demo
./anra send --agent alice --peer http://127.0.0.1:8080 "hello from alice"
# bob: hello from alice
```

Both agents keep the exchange in separate tape databases under `.anra/`.
Stop Bob with Ctrl-C and restart the same command to keep his conversation.
The echo runner repeats text; use the default DeepSeek runner with
`DEEPSEEK_API_KEY` for model-generated answers.

Keep agent settings in [`anra.example.toml`](anra.example.toml) and select them
with `--config` and `--agent`:

```sh
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml serve --agent bob
# In another terminal; Alice is the configured default agent:
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml send "hello from alice"
```

Viper also accepts environment settings such as `ANRA_CONFIG`, `ANRA_AGENT`,
and `ANRA_RUNNER`. Precedence is flags, environment, agent TOML settings, shared
defaults, then built-in defaults. See [command line usage](docs/cli.md).
