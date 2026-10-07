# A2A client ablation

Historical study of the removed handwritten tape-view JSON-RPC client. The
current client uses generated protobuf/Connect bindings; the page-provenance
and bounded-reconstruction experiments below remain covered by Connect tests.
See [current API contract](../../proto/README.md).

The client duplicated service-side request validation and checked returned entries
both in Read and BorrowTool. This study removes those layers in stages and tests
the remaining borrowing invariants individually.

## Experiments

| Change | Evidence | Outcome |
| --- | --- | --- |
| Baseline | Existing A2A suite passes | Reference |
| Remove per-call client/URL checks, Borrow/Read parameter preflight, and peer-ranking URL checks | Existing A2A suite passes | Keep removal; NewClient handles construction, HTTP handles request errors, Service validates RPC parameters |
| Remove Read's page/entry revalidation, expiry-presence check, per-entry re-encoding, total counter, and cursor cache | Existing A2A suite passes | Keep removal; BorrowTool owns grant binding and ordered range reconstruction |
| Encode RPC request once; accept additional response fields | Final suite and additional-response-fields test | Keep simplification |
| Temporarily remove page source/view/snapshot binding | Wrong source, view, and snapshot tests accept mismatched data | Restore check |
| Temporarily remove entry sequence/range check | Out-of-range test accepts an unauthorized sequence | Restore check |
| Temporarily remove final completeness check | Missing-entries test accepts a truncated result | Restore check |
| Temporarily remove nonempty continuation check | No-progress test makes further requests until the fixture terminates with HTTP 500 | Restore check |

The temporary variants were restored after each experiment. These are local
behavioral experiments, not performance measurements or live-peer trials.
Passing the existing suite alone does not establish equivalent behavior for
all malformed responses.

## Resulting boundaries

- NewClient validates the advertised extension endpoint and required configuration.
  Callers construct a usable client once; nil/zero clients are not supported.
- Service validates request parameters and enforces sharing, expiry, and export limits.
- Client handles HTTP/RPC errors, response IDs, extension acknowledgement, and
  the response-body size limit. JSON decoding accepts additional fields.
- Borrow/Get check decision correlation and the usable grant shape. Read returns
  the decoded page; direct Read callers now own their page-content validation.
- BorrowTool checks page provenance, snapshot identity, ordered sequences inside
  the grant, and complete reconstruction. Nonempty continuation pages advance
  through the finite selected grant range, so a cursor cache and total counter
  do not add a termination guarantee.
- Entry kind, timestamps, payload metadata, per-entry size, page-size limits,
  and expiry metadata are no longer revalidated by the client. The service still
  applies its export and request limits. No authorization checks were removed
  from the service.

client.go shrank from 326 to 290 lines.
