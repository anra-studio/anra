# Anra MCP Spec

Status: Approved. MCP service, authorization, and window interfaces are pending implementation.

Anra MCP is a core context service that allows authorized external execution environments to read role definitions and fixed Tape context windows for a specific handoff. It is decoupled from specific integration plugins: plugins initiate external execution, but do not proxy or replace execution endpoints calling this service directly.

Related specifications: [Plugin Spec](plugin-spec.md) defines onboarding and extension mechanisms; [Plugin Integration Spec](plugin-integration-spec.md) defines handoff anchor creation and authorization timing.

## Context Model

A role's Tape stores native Anra runtime execution records and authorized external observations. A Context Window is an immutable model input selected and projected from the Tape for a specific task; it is not synonymous with the entire tape history. A handoff custom anchor commits role revision metadata and window references before external work is submitted (see [Plugin Integration Spec](plugin-integration-spec.md#handoff-custom-anchor)).

Subsequent messages, concurrent tasks, and background observation entries never expand a committed window. The service reconstructs or serves the window strictly according to the handoff anchor; when reconstruction fails, it returns an explicit error rather than silently reading the live tape head. The exported window preserves origin and session boundaries, as well as necessary relationships between requests, tool calls, and results. Anchor IDs are references, not credentials.

Handoff anchors use the dedicated namespace `anra.dev/anchor/intr/v1` to represent handoff snapshots and authorization references, isolated from native execution records. Tape `Source` identifies the underlying storage instance and carries no application-level provenance semantics; external context windows are projected dynamically from committed anchors and projection policies. Storage representations and projection codecs will freeze following evaluation of tape-go capabilities.

## Read Capabilities

The initial release requires the following logical MCP tools. Exact tool identifiers, JSON schemas, and transport bindings will freeze during implementation design:

| Capability | Request | Successful Result |
| --- | --- | --- |
| Read Role | Authorized handoff anchor reference | Immutable Role identity and definition revision bound to this handoff |
| Read Window | Authorized handoff anchor reference | Committed TapeView descriptor, projected window entries, and completeness indicator |

Reads must be strictly bound to the original window and authorized scope; repeated reads must yield identical contents regardless of Tape growth. Response payloads must enforce size limits, and empty windows must remain distinguishable from nonexistent ones. Exceeding limits must fail explicitly rather than truncating silently. Unknown or unsupported entry types must be expressed clearly without dropping dependencies or claiming false completeness.

## Authorization and Connections

Callers must establish verified identity through controlled MCP connection authentication. Authorization binds:
- **Scope**: Role identity, handoff anchor reference, and RoleBinding identifier;
- **Access Control**: Fixed Tape snapshot/range, read-only operation permissions, and time-to-live (TTL).

The service validates authorization against the authenticated connection, strictly forbidding inference of identity from request parameters like role names or session IDs. Cross-binding, expired, or revoked access must be rejected. Connection credentials are delivered through secure runtime configurations, never leaked into prompts, Tape entries, or artifacts.

### External Invocation Example

Consider an external coding agent (such as a Lody session) reading committed context via MCP:

1. **Connection Setup**: The external runtime connects to Anra MCP using pre-distributed credentials/configuration.
2. **Read Role Identity**:
   ```json
   // Tool invocation: read_role
   {
     "anchorSeq": "21"
   }
   ```
   **Response**: Returns the bound Role identity and definition version (e.g., `vesta@role-revision-42`).

3. **Read Committed Context Window**:
   ```json
   // Tool invocation: read_context_window
   {
     "anchorSeq": "21"
   }
   ```
   **Response**:
   ```json
   {
     "view": {
       "source": { "agentId": "vesta", "tapeId": "tape-uuid" },
       "snapshotHead": "20",
       "scope": { "seqS": "1", "seqE": "20" }
     },
     "entries": [
       {
         "seq": "19",
         "role": "user",
         "content": "Implement the requested feature..."
       }
     ],
     "isComplete": true
   }
   ```
   *Note: `anchorSeq` functions solely as a resource locator; the service first verifies that the authenticated connection holds valid grants for that anchor before projecting entries.*

4. **Error Handling Examples**:
   - **Authentication Failure / Permission Denied** (unauthorized or cross-tenant access):
     ```json
     {
       "error": {
         "code": "permission_denied",
         "message": "unauthorized connection or missing grant for target handoff anchor"
       }
     }
     ```
   - **Invalid Reference / Missing Metadata** (target anchor is not a valid handoff record, or role definition cannot be found):
     ```json
     {
       "error": {
         "code": "invalid_reference",
         "message": "anchor 21 does not identify a valid integration handoff record or role definition is missing"
       }
     }
     ```
   - **Window Reconstruction Failure / Corrupted Snapshot**:
     ```json
     {
       "error": {
         "code": "failed_precondition",
         "message": "committed context window [1, 20) cannot be reconstructed from tape"
       }
     }
     ```

Before external session IDs exist, authorization may bind initially to the target integration and handoff anchor, registering verified session associations once available. Caller-reported session IDs do not prove identity. Systems cannot assume external applications support session-level credential injection; concrete credentials and revocation models require verification during plugin integration.

If callers cannot establish authorized connections meeting these constraints, handoffs must not pretend context is accessible. Missing identities, insufficient permissions, expiration, revocation, invalid references, non-reconstructible windows, and transient errors must be distinguishable, while avoiding metadata leakage across roles or tenants.

## Concurrency and Lifecycle

Reads access only persisted, authorized snapshots without acquiring role reasoning admission. The role may continue processing other messages concurrently; reads must not expose uncommitted records or alter window boundaries. The service does not manage external session launch, progress observation, cancellation, or result commits.

When invocations occur while Anra's current turn is active, the system must deterministically delineate which records are durably committed and eligible for inclusion in the fixed window. Existing TapeViews from completed turns do not automatically prove in-flight context is safe to export. The core must guarantee deterministic snapshot boundaries resilient against concurrent native writes, background observation appends, and crash recovery.
