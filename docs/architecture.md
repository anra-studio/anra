# Application boundaries

`cmd/` contains one executable shell, `cmd/anra`. Its Cobra commands parse
arguments, select configuration, bind listeners, print results and readiness,
and handle process signals. Application code lives in `pkg/` so another host,
such as a web UI, can use it without importing Cobra or launching a subprocess.

| Package | Responsibility | Main entry points |
| --- | --- | --- |
| `config` | Strict TOML decoding, named-agent selection, resolved startup settings and validation | `Load`, `File.Agent`, `Settings.Validate` |
| `model` | Model provider construction and provider environment settings | `New` |
| `agent` | Tape-backed ADK reasoning, built-in tools, admission, recovery and publications | `New`, `Anra.Run`, `Anra.Shutdown` |
| `peer` | Compose the configured model, agent and disk storage; serve A2A/TapeView; call a running agent | `Open`, `Node.Run`, `Node.Serve`, `Node.Close`, `Ask` |
| `discovery` | Directory records, card-origin validation, HTTP client and registration lease lifecycle | `NewClient`, `Client.Lookup`, `Client.List`, `Client.MaintainRegistration` |
| `registry` | In-memory directory server, lease ownership and expiry | `NewHandler`, `Serve` |
| `a2a` | A2A and TapeView protocol adapters, clients and peer tools | `NewExecutor`, `NewPeerHandler`, `NewMessageClient`, `NewService` |
| `model/anchor`, `model/delegator`, `model/journal` | Durable execution/publication records, delegation state and grant records | Protocol and storage types |

`peer` is the composition layer. `agent` knows neither configuration files nor
process lifecycle. `discovery` knows the directory's HTTP contract but does not
host its server; `registry` implements that contract. `a2a` adapts the runtime
through its `Runtime` interface. No package under `pkg/` imports `cmd/` or Cobra.
The private `internal/httpserver` helper shares cancellation and HTTP shutdown
between peer and registry servers; it has no terminal output.

The proposed [registry design](registry.md) replaces Card-based registry records
with a canonical Peer model using `address` and `skill`, a Connect RPC contract,
and A2A translation at the protocol boundary. It describes the intended next
design; the package table above describes the current implementation.

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
come from `DEEPSEEK_API_KEY` and `DEEPSEEK_API_BASE`.

## Serving and calling running peers

Bind a listener and pass it to `node.Serve(ctx, listener, ready)`. The bound
address, including an OS-selected port, is advertised in the Agent Card.
`ready` is an optional `func(net.Addr) error` callback; a host can use it to
update its own UI. Returning an error aborts startup and cleans up registration,
HTTP and the grant journal. The library prints nothing.

Cancel the serving context and wait for `Serve` to return before calling
`node.Close`. `Serve` owns its listener and grant journal; the host owns the
node. Registration renews until cancellation, transient directory outages are
retried, and permanent registration rejection stops serving with its cause.

`registry.Serve(ctx, listener, ttl, ready)` runs the directory with the same
listener and readiness contract. `registry.NewHandler(ttl)` lets an existing
HTTP host mount the directory instead. Neither API needs model credentials.

`peer.Ask(ctx, settings, input)` discovers and calls the configured running agent
without opening its local database or constructing a model. It returns the
structured collaboration result, including BUSY and publication hints. Each
shell decides how to present those outcomes; only the CLI converts a
non-completed outcome into its terminal error text.

The [application scenarios](../tests/e2e/features/application.feature) exercise
these boundaries through public Go APIs, real HTTP, deterministic provider
responses and temporary disk storage, including interoperability with the CLI.
