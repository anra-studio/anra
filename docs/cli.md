# Anra CLI

Build with `mise run build-cli`, or `go build -o anra ./cmd/anra`.
Run `./anra --help` or `./anra <command> -h` for Cobra-generated usage.
Shared flags (`--agent`, `--data-dir`, `--session`, `--config`) can precede the subcommand,
and command flags can appear before or after the message. Quote the message
to preserve its spacing. Use `--` before a message that starts with a dash.

Command definitions live in the `cmd` package: `root.go` registers the command
tree, with `run.go`, `serve.go`, and `send.go` defining each subcommand.
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

[agents.alice]
peer = "http://127.0.0.1:8080"

[agents.bob]
listen = "127.0.0.1:8080"
```

Select an entry with `--agent`; omitting it selects `default-agent`, or `anra`
if no default is configured. The selected agent must exist in the file.
Add, edit, or remove `[agents.<name>]` sections to manage the agent list.

Start Bob in one terminal:

```sh
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml serve --agent bob
```

From another terminal, send from the configured default agent, Alice:

```sh
ANRA_PEER_TOKEN=local-demo ./anra --config anra.example.toml send "hello from alice"
# bob: hello from alice
```

Each `[agents.<name>]` section and `[defaults]` can contain `data-dir`, `session`,
`runner`, `model`, `system`, `listen`, `peer`, and `timeout`. All values are
strings, with durations such as `"30s"` or `"2m"`. Only settings applicable to
the current command are applied; for example, `send` uses `peer`, while `serve`
uses `listen`. A configured `peer` satisfies `send`'s required `--peer` flag.

Explicit flags take precedence over agent settings, then `[defaults]`, then
built-in defaults. This includes explicitly empty values and values equal to
built-in defaults. Configuration values pass through Cobra's flag parsers and
the same validation as command flags. For example:

```sh
./anra --config anra.example.toml run --agent bob --session scratch "hello"
```

Relative `data-dir` values in the file resolve from the config file's directory;
an explicit `--data-dir` resolves from the current working directory. Model and
peer credentials still come from `DEEPSEEK_API_KEY` and `ANRA_PEER_TOKEN`.
Malformed TOML, unknown keys, and unknown agent selections fail before storage
opens. No config file is loaded unless `--config` is supplied, and help does
not require a readable config file.

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

## Peer messaging

Set the same `ANRA_PEER_TOKEN` in both terminals. This token authenticates the
trusted peer group. It is separate from the model API key. Start Bob:

```sh
export ANRA_PEER_TOKEN=local-demo
./anra serve --agent bob --runner echo --listen 127.0.0.1:8080
```

From another terminal:

```sh
export ANRA_PEER_TOKEN=local-demo
./anra send --agent alice --peer http://127.0.0.1:8080 "hello from alice"
# bob: hello from alice
```

`serve` publishes the standard Agent Card at `/.well-known/agent-card.json`
and authenticates core A2A messages at `/a2a`. It prints the listening URL to
stderr. Use a concrete interface address for `--listen`, such as
`127.0.0.1:8080`; this address is advertised in discovery. Port `0` selects an
available port. Ctrl-C or SIGTERM shuts down the server and closes its tape.
The CLI exposes messaging; context borrowing remains a library capability.

`send` discovers the peer and sends a text message using the official A2A SDK.
The sender runs through Anra's tape runtime with the remote peer providing its
answer, so the sender needs no model key. Both tapes retain the outgoing text
and reply. This command sends the supplied text directly; model-directed peer
tool selection is outside this initial command flow. To receive a model answer,
start Bob with `--runner deepseek` and set his `DEEPSEEK_API_KEY`.

## Storage and sessions

Every command accepts `--agent`, `--data-dir`, and `--session`. Defaults are
`anra`, `.anra`, and `default`. Agent names start with a letter or digit and
contain only letters, digits, dots, underscores, or hyphens.

Each agent uses `<data-dir>/<agent>.db`, with its identity as the local tape
owner. `--session` selects a conversation inside that database. Incoming peer
messages use the serving agent's configured session; they do not choose a local
owner or session through A2A IDs. All peers talking to that server contribute
to that conversation.

Only one process can open an agent database at a time. While Bob is serving,
use `send` to talk to him; stop the server before using `run --agent bob` on
the same database. Alice and Bob can run concurrently because their files are
separate. `run` and `send` accept `--timeout` (default `1m`).
