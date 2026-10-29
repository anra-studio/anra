# Application boundaries

`cmd/` contains one executable shell, `cmd/anra`. Its Cobra commands parse
arguments, select configuration, bind listeners, print results and readiness,
and handle process signals. Application code lives in `pkg/` so another host,
such as a web UI, can use it without importing Cobra or launching a subprocess.

| Package | Responsibility | Main entry points |
| --- | --- | --- |
| `config` | Strict TOML decoding, named-agent selection, resolved startup settings and validation | `Load`, `File.Agent`, `Settings.Validate` |
| `model` | Model provider construction and provider environment settings | `New` |
| `agent` | Tape-backed ADK reasoning, built-in tool registration, admission, recovery and committed context windows | `New`, `Anra.Run`, `Anra.Shutdown` |
| `agent/tools` | Built-in ADK tool schemas, argument handling, delegation transitions and model-facing results | `NewPeerTools`, `NewPeerBorrowTool`, `NewBorrowTool` |
| `peer` | Compose the configured model, agent and disk storage; serve A2A/TapeView; call a running agent | `Open`, `Node.Run`, `Node.Serve`, `Node.Close`, `Ask` |
| `discovery` | Typed registry client, presence checks, heartbeat maintenance and owned metadata updates | `NewClient`, `Client.Lookup`, `Client.List`, `Client.Start`, `Registration.Update` |
| `registry` | Persistent Peer directory, runtime ownership, presence and retirement | `Open`, `NewHandler`, `Serve` |
| `a2a` | A2A and TapeView protocol adapters, clients and borrowing services | `NewExecutor`, `NewPeerHandler`, `NewMessageClient`, `Client.BorrowContext`, `NewService` |
| `model/anchor`, `model/delegator`, `model/journal` | Durable execution records, delegation state and grant records | Protocol and storage types |

`peer` is the composition layer. `agent` knows neither configuration files nor
process lifecycle. `discovery` knows the directory's HTTP contract but does not
host its server; `registry` implements that contract. `a2a` adapts the runtime
through its `Runtime` interface. No package under `pkg/` imports `cmd/` or Cobra.
`agent/tools` calls discovery, Delegator, and the reusable A2A clients. `a2a`
does not depend on ADK tool definitions. `Client.BorrowContext` composes grant
selection and validated view reads; the tool layer only formats their results
as canonical ProtoJSON objects for the model.
The private `internal/httpserver` helper shares cancellation and HTTP shutdown
between peer and registry servers; it has no terminal output.

The [registry contract](registry.md) uses canonical Peer metadata with `address`
and `skill`, separate observed presence, and generated Connect RPCs. A2A cards
are produced at the adapter boundary. Known Peers survive loss of contact;
callers check presence and explicitly select a different agent if appropriate.

## Running locally from another host

Load the same configuration file used by the CLI, then own the node explicitly:

```go
file, err := config.Load("anra.example.toml")
if err != nil {
    return err
}
settings, err := file.Agent("alice")
if err != nil {
    return err
}
node, err := peer.Open(ctx, settings)
if err != nil {
    return err
}
defer func() {
    cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    runErr = errors.Join(runErr, node.Close(cleanup))
}()
answer, err := node.Run(ctx, "hello")
```

This fragment belongs in a function with a named `runErr error` return so
cleanup errors can be returned too. `Node.Close` drains reasoning before closing
its tape. If draining times out, storage stays open and the host can retry
cleanup. Do not open the same agent database in two hosts at once. After closing
it, another host or CLI command can resume the same conversation.

A host can also build a `config.Settings` value directly. `peer.Open` validates
it before opening storage, including the identity used in database filenames.
For custom runtime composition, use `agent.New` with initialized tape storage;
`model.New` is independent of the agent loop. Provider credentials continue to
come from `OPENAI_API_KEY` and `OPENAI_BASE_URL`.

## Serving and calling running peers

Bind a listener and pass it to `node.Serve(ctx, listener, ready)`. The bound
address, including an OS-selected port, is advertised in the Agent Card.
`ready` is an optional `func(net.Addr) error` callback; a host can use it to
update its own UI. Returning an error aborts startup, disconnects established
presence, and closes HTTP and the grant journal. The committed Peer remains known
and offline. The library prints nothing.

Cancel the serving context and wait for `Serve` to return before calling
`node.Close`. `Serve` owns its listener and grant journal; the host owns the
node. Heartbeats continue until cancellation, transient outages are retried,
and permanent ownership rejection stops serving with its cause.

`registry.Serve(ctx, listener, config, ready)` owns directory storage and HTTP.
Supply `registry.Config{Path: "registry.db", HeartbeatTimeout: 15*time.Second}`.
An existing host can instead `registry.Open(config)`, mount
the handler returned by `registry.NewHandler(store)` (checking its error), and
close the store after HTTP drains. Neither API
needs model credentials. A registry restart retains Peers and resets observed
presence to unknown.

`internal/httpserver` mounts Connect and Vanguard REST from the same registered
services and owns the common HTTP lifecycle. [HTTP routes](http.md) are defined
in protobuf annotations. Peer REST borrowing runs the same validation,
authentication, extension negotiation, and requester-bound grants as Connect.

`Node.UpdatePeer(ctx, metadata)` conditionally updates owned Peer metadata and
refreshes the public Agent Card. Registry lookup and list return `Entry` values
with Peer, Presence and revision; List includes all states by default. Use an
explicit `ListRequest` state/skill filter for selection.

`peer.Ask(ctx, settings, input)` discovers and calls the configured running agent
without opening its local database or constructing a model. It returns the
structured collaboration result, including BUSY and publication hints. Each
shell decides how to present those outcomes; only the CLI converts a
non-completed outcome into its terminal error text.

The [application scenarios](../tests/e2e/features/application.feature) exercise
these boundaries through public Go APIs, real HTTP, deterministic provider
responses and temporary disk storage, including interoperability with the CLI.
