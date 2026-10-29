# HTTP services

Registry and peer hosts share `internal/httpserver`: `NewHandler` mounts
Connect and Vanguard REST routes from one `connect.Server`; `Serve` owns the
listener and graceful shutdown. Vanguard v0.5.0 adds REST using the protobuf
`google.api.http` annotations. The application still uses `net/http` for the
server lifecycle. See [Vanguard's API](https://github.com/connectrpc/vanguard-go/releases/tag/v0.5.0).

The same registered implementations, validation interceptors, durable registry,
and grant journal serve both transports. Existing Connect clients and core A2A
JSON-RPC routes remain available.

## Registry routes

| HTTP request | RPC | Request fields |
| --- | --- | --- |
| `POST /v1/registry/peers:register` | Register | JSON body, including peer/runtime ownership fields |
| `POST /v1/registry/peers/{id}:heartbeat` | Heartbeat | Runtime ID and token in the JSON body |
| `POST /v1/registry/peers/{id}:disconnect` | Disconnect | Runtime ID and token in the JSON body |
| `GET /v1/registry/peers/{id}` | Lookup | Peer ID in the path |
| `GET /v1/registry/peers` | List | Optional `state` and `skill` query parameters |
| `DELETE /v1/registry/peers/{id}` | Forget | Required `expectedRevision` query parameter |

JSON bodies use `Content-Type: application/json` and protobuf JSON field names.
For example, a registration body is:

```json
{
  "peer": {
    "id": "alice",
    "address": "http://127.0.0.1:8080",
    "skill": [{"id": "chat", "name": "Chat"}]
  },
  "runtimeId": "alice-runtime",
  "token": "runtime-ownership-secret"
}
```

Path fields are supplied by the URL. Conditional metadata updates also include
`expectedRevision`. Discovery responses expose Peer, Presence, and revision;
they do not return ownership tokens. Runtime ownership, retirement, and conflict
semantics are documented in [the registry contract](registry.md).

`registry.NewHandler(store)` now returns `(http.Handler, error)` so hosts can
handle route registration failures. `registry.Serve` propagates those errors.

## TapeView routes

| HTTP request | RPC | Request fields |
| --- | --- | --- |
| `POST /v1/tape/borrows` | Borrow | JSON body containing `requestId`, `purpose`, `query`, and optional constraints |
| `GET /v1/tape/borrows/{request_id}` | Get | Borrow request ID in the path |
| `GET /v1/tape/views/{view_id}/entries` | Read | Optional `cursor` and `pageSize` query parameters |
| `POST /v1/tape/views/{view_id}:release` | Release | View ID in the path and an empty JSON body `{}` |

Every TapeView REST request requires the trusted peer identity and extension
opt-in headers, just like Connect:

```http
Anra-Agent: bob
A2A-Extensions: urn:anra:a2a:tape-view:v1
```

Successful and dispatched error responses acknowledge the negotiated extension
in `A2A-Extensions`. Authentication and extension checks execute before message
validation through a shared server interceptor. Custom authentication callbacks
still receive the original HTTP request; no body field supplies caller identity.

REST and Connect operate on the same requester-bound grants. A grant created
through either transport can be read or released through the other. Another
requester cannot use its view ID. Reading context does not invoke the lender's
model. Entry payload bytes use protobuf JSON's base64 representation.

`NewPeerHandler` mounts both TapeView route prefixes alongside the public Agent
Card and `/a2a`. Hosts using `NewViewHandler` directly must mount it at both
`ViewPath + "/"` and `ViewRESTPath` to expose both transports.

## Validation and errors

REST requests run the same Protovalidate rules as Connect requests. Unknown JSON
fields and query parameters are rejected. The existing service request-body
limits are passed to Vanguard, including its decompression limit. Oversized
requests return HTTP 429 (`RESOURCE_EXHAUSTED`); malformed or invalid messages
return HTTP 400. Unknown paths return HTTP 404 and wrong methods HTTP 405.

Vanguard error bodies use the protobuf JSON form of `google.rpc.Status`, with a
numeric code, message, and optional details. Authentication failures return HTTP
401; a missing TapeView opt-in returns HTTP 400 (`FAILED_PRECONDITION`);
unavailable or another requester's grants return HTTP 404. Connect callers
continue to receive Connect's protocol error format.

The executable acceptance contract is `tests/e2e/features/http.feature`. It
uses public application hosting APIs, real HTTP, deterministic model fixtures,
and temporary disk storage to verify REST/Connect state sharing, validation,
body limits, pagination, identity, extension negotiation, and release.
