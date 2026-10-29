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
provider = "openai"
model = "gpt-4o-mini"
timeout = "1m"
role = "e2e-bot"
desc = "alice is a e2e example bot"
endpoint = "127.0.0.1:0"

[agents.bob]
data-dir = ".anra"
provider = "openai"
model = "gpt-4o-mini"
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
| `[lobby].address` | Team Peer registry address; agents find peers only through the lobby | Required |
| `data-dir` | Tape database directory, relative to the config file | Required |
| `provider` | Model provider (`openai`) | Required |
| `model` | Model name; empty uses tape-go's provider default | Optional |
| `timeout` | Command timeout such as `"30s"`; must be positive | Required |
| `role` | Role part of the agent's system instruction | Optional |
| `desc` | Description part of the system instruction | Optional |
| `endpoint` | HTTP listen address of `serve`, such as `127.0.0.1:0` | Required |
| `address` | Public HTTP(S) origin, for example behind a proxy; defaults to the concrete bound address | Optional |

Each selected agent must explicitly set `data-dir`, `provider`, `timeout`, and
`endpoint` for `run`, `ask`, and `serve`. Missing, empty, or whitespace-only
values fail before storage opens or the command contacts a server.

The system instruction is `role:desc` (just the one that is set if the other is
empty). Anra adds nothing of its own: the model sees only that (plus ADK's agent metadata). Name the agent in one of them if
the model should know it.

Unknown keys (including the removed `default-agent`, `[defaults]`, `session`,
`registry`, `api-base`, and any `tools`) fail with `parse config` before
storage opens. Help does not require a readable config file. `registry` is the
one command that takes no config: it uses `--listen`, `--data-dir`, and
`--heartbeat-timeout`.

The remaining shared flags select the agent and file: `--agent` and `--config`. The model credential comes from `OPENAI_API_KEY`.
`OPENAI_BASE_URL` optionally overrides the OpenAI-compatible Chat Completions
base URL. It defaults to `https://api.openai.com/v1`; include any required path
prefix, for example `https://gateway.example.com/v1`. Anra appends
`chat/completions`, and a trailing slash is optional. Credentials are never TOML
settings. Set `model` to a model supported by your endpoint; the default is
`gpt-4o-mini`.

To migrate an existing configuration, change `provider = "deepseek"` to
`provider = "openai"`, rename `DEEPSEEK_API_KEY` to `OPENAI_API_KEY`, and rename
`DEEPSEEK_API_BASE` to `OPENAI_BASE_URL`. Keep the endpoint's model name. The old
provider and environment variable names are no longer supported.

Start the directory with `./anra registry`, set `OPENAI_API_KEY` in both
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
provider; it does not replace the agent loop. Set `OPENAI_API_KEY` in the
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
Each `serve` registers its canonical Peer with the lobby and publishes an A2A
Agent Card generated from that Peer, including its address and skills. All agents share this discovery
endpoint; each agent owns its tape and communicates directly with peers.

`serve` publishes `/.well-known/agent-card.json` and A2A at `/a2a`.
An `endpoint` of `127.0.0.1:0` chooses an available port. A wildcard bind
requires a concrete public `address` in the agent configuration. The address
must be an HTTP(S) origin with no user information, query, fragment or non-root
path; port zero and wildcard public addresses are invalid.

The registry persists metadata in `<registry-data-dir>/registry.db`; the data
directory defaults to `.anra`. It keeps each Peer after shutdown, a crash or a
registry restart. Presence is observed separately: `ONLINE` means recent contact,
`OFFLINE` means an explicit disconnect, and `UNKNOWN` means stale contact or a
registry restart. The last address and skills remain available in every state.

Heartbeats run every third of the freshness window, which defaults to `15s` and
is configurable with `registry --heartbeat-timeout` (minimum `1s`). The former
`--lease-ttl` flag has been removed. Ctrl-C or SIGTERM disconnects presence and
drains reasoning before closing the tape. A crash eventually leaves unknown
presence. Active servers retry transient failures and re-establish presence
after a registry restart. A conflicting online runtime cannot claim the same
Peer; stale-runtime mutations are rejected after replacement.

Explicitly retire an offline or unknown Peer with:

```sh
./anra registry forget alice --address http://127.0.0.1:9090
```

Retirement removes its directory entry and retains a tombstone. Reusing retired
IDs is outside the current contract. It does not delete the agent's tape.

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
a TapeView of its latest completed invocation, including while busy. The model
does not choose a snapshot. Grants pin its source, exact scope, and closing anchor.
Incoming TapeViews are exposed by `discover_peers` as ungranted hints; the lender
owns selection. Borrowing records the grant and returned provenance as a tool
result on the borrower's tape. References never confer another agent's grant.

A completed window contains its user message, assistant messages, and structured
tool calls/results. Failed or unfinished work, thought parts, state deltas, and
control anchors are excluded. The closing anchor records completion without
copying the final answer. Existing entry/page size limits still apply; there is
no separate 2 KiB publication limit. Trusted callers receive separate
requester-bound grants for these immutable windows.

The CLI uses `[lobby].address` to find the selected server; its peer tools resolve
the destination's canonical Peer and observed presence. There is no `--peer` flag or TOML `peer`
setting. Use an address reachable by other team members. Unknown/offline presence
is reported explicitly; requests are never routed automatically to a same-skill
agent. `discover_peers` explicitly lists online Peers.

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
