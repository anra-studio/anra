# Anra CLI

Build with `mise run build-cli`, or `go build -o anra ./cmd/anra`.
Run `./anra --help` or `./anra <command> -h` for Cobra-generated usage.
`--agent` and `--config` can precede the subcommand, and command flags can
appear before or after the message. Quote the message to preserve its spacing.
Use `--` before a message that starts with a dash.

The command shell lives entirely in `cmd/anra`: `root.go` registers the Cobra
command tree and `main.go` handles process signals. Commands call the reusable
`pkg/config`, `pkg/peer`, and `pkg/registry` APIs. Configuration parsing, model
construction, storage, peer serving and registration do not depend on Cobra;
see [application architecture and embedding](architecture.md).

## Agent configuration

Every agent setting comes from a TOML file, and `--config path/to/agents.toml`
is required by `run`, `ask`, and `serve`. `--agent` (default
`anra`) selects the `[agents.<name>]` table, which must exist. The repository
includes [`anra.example.toml`](../anra.example.toml):

```toml
[lobby]
address = "http://127.0.0.1:9090"

[agents.alice]
data-dir = ".anra"
provider = "deepseek"
model = "deepseek-flash"
timeout = "1m"
role = "e2e-bot"
desc = "alice is a e2e example bot"
endpoint = "127.0.0.1:0"

[agents.bob]
data-dir = ".anra"
provider = "deepseek"
model = "deepseek-flash"
timeout = "1m"
role = "e2e-bot"
desc = "bob is a e2e example bot"
endpoint = "127.0.0.1:8080"
```

The file is read once when the command starts. To change an agent's settings,
edit the file and restart its server; a running server keeps the settings it
started with. There are no flags or `ANRA_*` environment variables for these
settings.

| Key | Meaning | Requirement |
| --- | --- | --- |
| `[lobby].address` | Team Agent Card directory URL; agents find peers only through the lobby | Required |
| `data-dir` | Tape database directory, relative to the config file | Required |
| `provider` | Model provider (`deepseek`) | Required |
| `model` | Model name; empty uses tape-go's provider default | Optional |
| `timeout` | Command timeout such as `"30s"`; must be positive | Required |
| `role` | Role part of the agent's system instruction | Optional |
| `desc` | Description part of the system instruction | Optional |
| `endpoint` | HTTP listen address of `serve`, such as `127.0.0.1:0` | Required |

Each selected agent must explicitly set `data-dir`, `provider`, `timeout`, and
`endpoint` for `run`, `ask`, and `serve`. Missing, empty, or whitespace-only
values fail before storage opens or the command contacts a server.

The system instruction is `role:desc` (just the one that is set if the other is
empty). Anra adds nothing of its own: the model sees only that (plus ADK's agent metadata). Name the agent in one of them if
the model should know it.

Unknown keys (including the removed `default-agent`, `[defaults]`, `session`,
`registry`, `api-base`, and any `tools`) fail with `parse config` before
storage opens. Help does not require a readable config file. `registry` is the
one command that takes no config: it uses its own `--listen` and `--lease-ttl`
flags.

The remaining shared flags select the agent and file: `--agent` and `--config`. The model credential comes from `DEEPSEEK_API_KEY`.
`DEEPSEEK_API_BASE` optionally overrides the provider's HTTP endpoint (the E2Es
point it at a deterministic local model fixture). Credentials are never TOML
settings.

Start the directory with `./anra registry`, set `DEEPSEEK_API_KEY` in both
server environments, then start the agents in separate terminals:

```sh
./anra --config anra.example.toml serve --agent alice
./anra --config anra.example.toml serve --agent bob
```

From another terminal:

```sh
./anra --config anra.example.toml ask --agent alice "Discover Bob and ask him to say hello."
```

## Local agent

```sh
./anra --config anra.example.toml run --agent alice "hello"
```

`run` opens a tape-backed Anra agent, appends the message, runs it, prints its
answer to stdout, and closes storage. A later command with the same agent
and data directory reconstructs its conversation from disk.

The runner is tape-go's ADK runtime. The `provider` setting selects the model
provider; it does not replace the agent loop. Set `DEEPSEEK_API_KEY` in the
environment before using `run` or `serve`. Leaving `model` empty uses
tape-go's provider default. An absent key is an error. Production has no echo
provider; the repository E2Es run a deterministic provider HTTP fixture with a
fixture key, so no real model credentials are needed.

The runtime registers its built-in Bash, peer discovery, and peer messaging
tools internally. Named peer operations use the config's `[lobby].address`.
There is no tools flag, TOML `tools` key, environment tool setting, or public
option for injecting arbitrary tools.

## Running servers and discovery

Start one team directory with `./anra registry --listen 127.0.0.1:9090`.
The directory and peer endpoints must be accessible only to trusted team members.
There is no global peer token or credential exchange. Clients automatically send
`Anra-Agent: <caller-agent-name>`; receivers require a valid identity and trust
members not to impersonate one another. The header is not a credential, and a
receiver does not verify membership against the registry on every request.
The local CLI acts on behalf of the agent selected with `--agent`.
TapeView grants remain bound to the supplied requester identity.
Each `serve` registers with the lobby and publishes its Agent Card, including
its actual bound address and capabilities. All agents share this discovery
endpoint; each agent owns its tape and communicates directly with peers.

`serve` publishes `/.well-known/agent-card.json` and A2A at `/a2a`.
An `endpoint` of `127.0.0.1:0` chooses an available port. Agent names are
unique while their leases are active; attempting to start another server with the same name fails.

Registrations renew every third of the lease lifetime. The directory defaults
to a `15s` lifetime, configurable with `registry --lease-ttl`. Ctrl-C or SIGTERM
shuts down HTTP, closes the tape, and unregisters the server. A crashed process
loses its registration after the lease expires. The directory is in memory;
active servers retry transient directory failures and register again after it
restarts. An invalid or conflicting registration stops the affected
agent server. An agent restart registers the same name at its new port and
resumes its saved conversation.

`ask --agent alice "Discover Bob and ask him to say hello."` submits a question
to Alice's model over A2A. Her ADK loop can answer locally, discover peers, ask a
specific question with `send_peer_message`, and combine the result into her
answer. Both servers need model credentials; the `ask` process does not.
The forwarding-only `send` command, `--to`, and `/anra/send` route are removed.

A tape admits one reasoning invocation at a time. `ask` reports `busy: request
was not accepted` if the selected server is occupied. A peer's messaging tool
receives structured `completed` or `busy` results, so its
model can continue. Repeated reasoning against unchanged BUSY context returns
`no_new_context` locally. Delegation propagates a deadline and records its path.
Expired deadlines and cancellation remain ordinary errors. There is no hop
counter, queue, or automatic retry.

The built-in `borrow_peer_context` tool discovers a named peer's TapeView
endpoint and sends `peer`, `requestId`, `purpose`, and `query`. The lender selects
its latest eligible publication locally, including while busy. The model does
not choose an anchor. Grants contain exactly that immutable anchor,
with its original source and coverage. Borrowing works while the owner is busy.
Incoming publication references are exposed by `discover_peers` as hints; the
lender owns the borrow selection. Borrowing records the grant and returned provenance as a tool result on the borrower's
tape. References never confer another agent's grant.

The agent lifecycle publishes eligible completed final answers up to 2 KiB by
default. Trusted callers borrow those projections through TapeView, which issues
separate requester-bound grants. Unfinished events and raw control records remain
private. This first implementation publishes final answers; it does not expose an
intermediate `publish_context` tool.

The CLI uses `[lobby].address` to find the selected server; its peer tools resolve
the destination's current Agent Card. There is no `--peer` flag or TOML `peer`
setting. Use a listen address reachable by other team members.

## Storage and sessions

`--agent` defaults to `anra`. Agent names start with a letter or digit and
contain only letters, digits, dots, underscores, or hyphens.

Each agent uses `<data-dir>/<agent>.db`, with its identity as the local tape
owner. Each agent has a single conversation (session `default`) inside that database; the
session is not configurable. Incoming peer messages do not choose a local
owner or session through A2A IDs. All peers talking to that server contribute
to that conversation.

Only one process can open an agent database at a time. `ask` uses the
running server, so it works while its database is open. Stop a server
before using `run` with that agent's database. Alice and
Bob run concurrently because their files are separate. The `timeout` setting
bounds `run`, `ask`, and server reasoning requests, with an additional two-minute runtime ceiling. Shutdown cancels active reasoning
and waits for terminal cleanup before closing the tape.

The tape stores a stable source identity and invocation start/close anchors using
`anra.dev/anchor/v1`. A successful answer is acknowledged only after its close
commits. On restart, an open invocation receives one abandoned close; its
unfinished ADK events and state deltas stay on disk but are excluded from the
next model context. An uncertain storage append fences new reasoning.

`serve` also opens `<data-dir>/<agent>.grants.db` and exposes the TapeView Connect
service advertised in its card. Grants retain their original source, exact range,
head, requester, expiry, and release/revocation state across restart.
