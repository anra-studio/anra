# Anra CLI

Build with `mise run build-cli`, or `go build -o anra ./cmd/anra`.
Run `./anra --help` or `./anra <command> -h` for Cobra-generated usage.
Shared flags (`--agent`, `--data-dir`, `--session`, `--config`, `--registry`) can precede the subcommand,
and command flags can appear before or after the message. Quote the message
to preserve its spacing. Use `--` before a message that starts with a dash.

Command definitions live in the `cmd` package: `root.go` registers the command
tree, with one file per subcommand, including `run.go`, `serve.go`, `send.go`,
`registry.go`, and `peers.go`.
`cmd/anra/main.go` is the executable entry point.

## Agent configuration

Use `--config path/to/agents.toml` to keep named agents in a TOML file.
The repository includes [`anra.example.toml`](../anra.example.toml):

```toml
default-agent = "alice"

[defaults]
data-dir = ".anra"
session = "default"
runner = "echo"
timeout = "1m"
registry = "http://127.0.0.1:9090"
listen = "127.0.0.1:0"

[agents.alice]

[agents.bob]
```

Select an entry with `--agent`; omitting it selects `default-agent`, or `anra`
if no default is configured. The selected agent must exist in the file.
Add, edit, or remove `[agents.<name>]` sections to manage the agent list.

Start the directory with `ANRA_PEER_TOKEN=local-demo ./anra registry`.
Then start both agents in separate terminals:

```sh
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml serve --agent alice
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml serve --agent bob
```

From another terminal:

```sh
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml send --agent alice --to bob "hello from alice"
# bob: hello from alice
```

Each `[agents.<name>]` section and `[defaults]` can contain `data-dir`, `session`,
`runner`, `model`, `system`, `listen`, `registry`, `peer`, and `timeout`. All values are
strings, with durations such as `"30s"` or `"2m"`. Only settings applicable to
the current command are applied; for example, `send` uses `peer`, while `serve`
uses `listen`. `registry` is the shared discovery endpoint. Named sends use
`--to`; a configured `peer` selects the optional direct-address mode.

Explicit flags take precedence over environment variables, agent settings,
`[defaults]`, and built-in defaults, in that order. This includes explicitly
empty values and values equal to built-in defaults. Viper resolves these sources;
configuration and environment values pass through Cobra's flag parsers and
the same validation as command flags. For example:

```sh
./anra --config anra.example.toml run --agent bob --session scratch "hello"
```

Relative `data-dir` values in the file resolve from the config file's directory;
an explicit `--data-dir` or `ANRA_DATA_DIR` resolves from the current working
directory. Model and peer credentials come from `DEEPSEEK_API_KEY` and
`ANRA_PEER_TOKEN`, bound through Viper.
Malformed TOML, unknown keys, and unknown agent selections fail before storage
opens. No config file is loaded unless `--config` or `ANRA_CONFIG` is supplied,
and help does not require a readable config file.

## Environment variables

Viper binds command options to `ANRA_` environment variables, with hyphens
replaced by underscores. Variables apply to the commands that support the
corresponding flags.

| Variable | Setting |
| --- | --- |
| `ANRA_CONFIG` | TOML configuration file |
| `ANRA_AGENT` | Agent name |
| `ANRA_DATA_DIR` | Tape database directory |
| `ANRA_SESSION` | Conversation session |
| `ANRA_RUNNER` | `deepseek` or `echo` |
| `ANRA_MODEL` | Model name |
| `ANRA_SYSTEM` | System instruction |
| `ANRA_LISTEN` | Server or registry listen address |
| `ANRA_REGISTRY` | Team Agent Card directory URL |
| `ANRA_TO` | Named send destination |
| `ANRA_LEASE_TTL` | Registry lease lifetime (default `15s`, minimum `1s`) |
| `ANRA_PEER` | Optional direct-address send destination |
| `ANRA_TIMEOUT` | Command timeout, such as `30s` |
| `ANRA_PEER_TOKEN` | Peer bearer token |
| `DEEPSEEK_API_KEY` | DeepSeek API key |

For example, run an agent without repeating command flags:

```sh
ANRA_AGENT=alice ANRA_RUNNER=echo ./anra run "hello"
ANRA_CONFIG=anra.example.toml ANRA_AGENT=bob ./anra run "hello"
```

Flags override environment variables even when the flag equals its built-in
default. Empty environment values are also honored: an empty required setting
fails validation instead of falling back to TOML. Credentials remain
environment-only and are not accepted as flags or TOML settings.

## Local agent

```sh
./anra run --agent alice --runner echo "hello"
# alice: hello
```

`run` opens a tape-backed Anra agent, appends the message, runs it, prints its
answer to stdout, and closes storage. A later command with the same agent,
data directory, and session reconstructs its conversation from disk.

`--runner echo` is an explicit deterministic demonstration. The default runner
is `deepseek`, using tape-go's DeepSeek provider and ADK runner adapter. Set
`DEEPSEEK_API_KEY` in the environment before using it:

```sh
./anra run --agent alice --model deepseek-chat --system "Answer briefly." "hello"
```

Leaving `--model` empty uses tape-go's provider default. An absent key is an
error; the command never silently switches to echo. Live provider behavior
requires your credentials; the repository E2Es use echo.

## Running servers and discovery

Start one team directory with `./anra registry --listen 127.0.0.1:9090`.
Use the same `ANRA_PEER_TOKEN` for the directory, agent servers, and CLI clients.
Each `serve --registry http://127.0.0.1:9090` registers its Agent Card, including
its actual bound address and capabilities. All agents share this discovery
endpoint; each agent owns its tape and communicates directly with peers.

`serve` publishes `/.well-known/agent-card.json` and authenticated A2A at `/a2a`.
`--listen 127.0.0.1:0` chooses an available port. `peers --registry <url>` lists
registered names and card URLs. Agent names are unique while their leases are
active; attempting to start another server with the same name fails.

Registrations renew every third of the lease lifetime. The directory defaults
to a `15s` lifetime, configurable with `registry --lease-ttl`. Ctrl-C or SIGTERM
shuts down HTTP, closes the tape, and unregisters the server. A crashed process
loses its registration after the lease expires. The directory is in memory;
active servers retry transient directory failures and register again after it
restarts. A rejected credential or conflicting registration stops the affected
agent server. An agent restart registers the same name at its new port and
resumes its saved conversation.

`send --agent alice --to bob` requires both named agents to be serving. The CLI
resolves Alice, invokes her authenticated outbound endpoint, and Alice discovers
Bob's current card before sending over the official A2A SDK. Alice records the
outgoing message and Bob's reply; Bob records the incoming message and his
answer. Both source and destination tapes stay owned by their server processes.
Bob can use the same path to send to Alice. Concurrent opposite-direction sends
are supported; concurrent exchanges can interleave in each agent's session.

This explicit send flow uses the target's configured runner to answer. The CLI
needs no model key; the destination server needs one when using DeepSeek. The
echo runner exercises registration, discovery, transport, and storage without
model credentials. The CLI exposes messaging; context borrowing remains a
library capability.

For direct-address use without discovery, `send --peer http://127.0.0.1:8080`
remains available. This mode opens the sender's local tape and requires that
sender's server to be stopped. `--peer` and `--to` are mutually exclusive.
A server started without `--registry` publishes its card but does not register.
Use a concrete listen address reachable by other team members; that address
is advertised in its card.

## Storage and sessions

Every command accepts `--agent`, `--data-dir`, and `--session`. Defaults are
`anra`, `.anra`, and `default`. Agent names start with a letter or digit and
contain only letters, digits, dots, underscores, or hyphens.

Each agent uses `<data-dir>/<agent>.db`, with its identity as the local tape
owner. `--session` selects a conversation inside that database. Incoming peer
messages use the serving agent's configured session; they do not choose a local
owner or session through A2A IDs. All peers talking to that server contribute
to that conversation.

Only one process can open an agent database at a time. Named sends use the
running source server, so they work while its database is open. Stop a server
before using `run` or direct `send --peer` with that agent's database. Alice and
Bob run concurrently because their files are separate. `run` and `send` accept
`--timeout` (default `1m`).
