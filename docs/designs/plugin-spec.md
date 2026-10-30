# Plugin Spec

Status: Approved. Plugin mechanisms and interfaces are pending implementation.

This specification defines how Anra registers, configures, and invokes integration plugins. For application onboarding, role handoffs, and Tape recording behaviors, see [Plugin Integration Spec](plugin-integration-spec.md). For the context-reading interface used by external execution environments, see [Anra MCP Spec](anra-mcp-spec.md).

## Responsibility Boundaries

A plugin is an application onboarding extension for Anra. It translates application interfaces into operations and observations that Anra can consume. A plugin is not a Peer; a role retains its own Peer identity. A single external application can serve both as an interactive entrypoint and as an external execution environment; each role declares its capabilities separately and enables them through explicit role bindings.

Anra core owns Roles, Peers, RoleBindings, handoff anchors, context authorization, Tape persistence, and role execution. Plugins own connections to specific applications, destination resolution, request translation, state interpretation, and observation record mapping. Plugins cannot bypass the core to append to Tape directly, nor can they unilaterally expand context authorization. Anra MCP is a core service; plugins do not own its definition or authorization policies.

## Registration and Connections

The initial version explicitly registers built-in plugins, managed by an integration package (such as `pkg/intr`) that creates and tears down connections according to configuration. Dynamic loading, out-of-process plugin protocols, or plugin marketplaces are not required. Each plugin type has a stable type ID and contract version. A single plugin type can support multiple independent `PluginConnection` instances. A connection contains a stable ID, configuration, credential references, enabled capabilities, and runtime health status. Credentials never appear in Roles, prompts, Tape records, or public plugin capability descriptors.

Connections validate configuration at startup, initialize necessary clients, and discover capabilities. When unavailable or authorization lapses, connections retain diagnostic state rather than treating unknown status as offline. During shutdown, connections stop accepting new work and release resources; whether external work continues depends on that application's contract—local connection closure must not imply cancellation of remote tasks.

Core selects destinations via connection ID and RoleBinding; plugins use pre-authorized target references without guessing roles, machines, or projects from display names. Third-party identifiers are interpreted by their respective plugins, and core persists and routes them unmodified. Shared wire transports do not imply identical execution semantics across different applications.

## Invocation Context

When core invokes a plugin, it supplies a scoped invocation context (`ctx`) carrying the active Role, authorized RoleBinding, and corresponding `PluginConnection` identity. Role identifies the persona, and RoleBinding identifies the application target and intended purpose; different bindings for the same Role are not interchangeable. Operations associated with external work also carry the handoff anchor reference. Task content, external execution references, and observations remain explicit inputs or outputs rather than generic context fields.

Core constructs `ctx` after resolving bindings and verifying connections and authorizations. Plugins resolve application targets strictly from this binding, never relying on unverified external role names or binding parameters. Inbound messages have no trusted Role or RoleBinding until source authentication and binding resolution complete: the plugin reports the raw source, core resolves the binding, and only then builds `ctx`. For asynchronous observation or recovery, core reconstructs the invocation context from persisted bindings and handoff anchors without assuming the original caller process is still running. Credentials are never placed in `ctx` or written to Tape.

This `ctx` represents a logical contract: implementations can use explicit types carrying the role and binding alongside standard runtime contexts for cancellation and timeouts, rather than untyped key-value maps.

## Capability Contract

Capabilities are verified per connection and target. Effective capabilities represent the intersection of plugin support, remote application runtime status, authorization, and local enablement. The following table describes logical operations without prescribing Go interfaces or wire serialization:

| Capability | Plugin Behavior Toward Core |
| --- | --- |
| Receive Message | Transforms authenticated application messages into inputs with source and target bindings |
| Send Reply | Dispatches results to verified origin sessions or designated targets and reports delivery status |
| Submit External Work | Accepts targets, tasks, and context references; returns acceptance status and external execution handles |
| Continue Work | Submits follow-up work to an existing external session, retaining execution references |
| Observe and Recover | Streams normalized observations, status, source identifiers, visibility scopes, and recoverable checkpoints |
| Cancel | Requests cancellation and returns immediate status; acceptance does not guarantee immediate remote stoppage |

Implementations may support only a subset of these operations. Core checks required capabilities before triggering external side effects and never automatically substitutes alternative destinations. Capability degradation, rate limits, authentication failures, undetermined outcomes, and explicit rejections must remain distinguishable so callers can decide whether to wait, reconcile, or abort. Interactive entrypoint messaging and external execution workflows exhibit different characteristics and must not be forced into a single generic execution operation.

## Observation Records and Persistence

Plugins deliver normalized observation records to core: binding, origin session and event references, record type, content, origin ordering, visibility scope, and optional revision links and checkpoints. Information omitted by external applications remains unknown; plugins must not synthesize stable IDs, synthetic full histories, or fabricated terminal states. Application-specific fields are interpreted by the plugin and retained as provenance references without requiring core to understand foreign session or operation models.

Core validates the association between bindings and handoff anchors, persisting records and deduplication metadata before advancing observation progress. During recovery, plugins reconcile state using persisted external references and checkpoints; without recoverable cursors or stable event IDs, guarantees and limitations must be stated explicitly.

Submission return and external task completion are distinct facts. Plugins must report whether work was accepted; when status cannot be confirmed, they must report an indeterminate result. Without verifiable idempotency guarantees, indeterminate submissions must not be retried automatically. Cancellation and delayed results must preserve their respective historical facts.

## Evolution

When revising contracts, establish version compatibility relationships first. Unknown mandatory capabilities or incompatible versions must be rejected explicitly; optional capabilities may be omitted. The initial version standardizes on the minimal common denominator between the two primary use cases, without promising uniform history replay, event subscriptions, idempotency, or cancellation semantics across all third-party systems.
