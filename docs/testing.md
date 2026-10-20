# Testing Anra

The repository keeps BDD end-to-end scenarios as its shared acceptance contract.
Read the [agent features](../tests/e2e/features/agent.feature) and
[peer features](../tests/e2e/features/peers.feature) to review what Anra promises.
The same Given/When/Then steps execute with
[Godog](https://github.com/cucumber/godog/tree/v0.15.1), so the specifications
and checks stay together. Undefined and pending steps fail the suite.

## Running scenarios

```sh
mise run test  # Build, schema checks, generated bindings, and E2E scenarios.
mise run e2e   # Only the BDD E2E scenarios.
# Go alone, without mise:
go test -count=1 -v ./tests/e2e
# Select a scenario using its Go subtest name:
go test -count=1 -v ./tests/e2e -run 'TestFeatures/A_grant_keeps_its_original_snapshot'
```

Scenarios use real Anra agents, tape-go bbolt storage, the grant journal,
discovery, the official A2A SDK, and Connect over localhost HTTP. Each scenario
gets its own temporary files and servers, with cleanup after success or failure.
Restart steps close and reopen the database files and rebuild the server on a
new port. Pagination checks include a view larger than one full page. Expiry
uses a controlled clock rather than sleeps.

Model responses and lender permission decisions are deterministic fixtures.
The suite needs no model credentials or external service once Go dependencies
are installed. It checks Anra's behavior around model calls; it does not measure
a real model's answer quality. Anra currently exposes a library, so public Go
APIs and peer HTTP endpoints are the E2E entry points. The suite does not launch
a separate process or exercise an application UI.

## Temporary unit checks

Agents should write unit tests when useful for diagnosis or implementation.
These are local evaluation tools and must not be committed. The repository
ignores `*_test.go` outside `tests/e2e/`; ignored Go files still run when their
package is tested:

```sh
go test ./pkg/a2a -run TestLocalInvestigation
```

Remove disposable tests after use. Before proposing a change, check that its
diff contains only the intended product changes and shared E2E scenarios.
The `AGENTS.md` policy carries this workflow to future agent sessions.

## Adding behavior

Start with a concrete scenario describing a person's goal and observable
outcome. Reuse steps where their meaning is the same, and exercise the public
boundary through the actual runtime, transport, and storage. Keep setup and
implementation detail in the step definitions, not in the feature language.
Each scenario must stand alone. Include denied access and failure behavior
when they are part of the promise being changed.

This policy favors a human-readable acceptance contract. Temporary unit checks
can make debugging faster, but their assertions are not retained as a separate
regression suite. BDD scenarios should therefore preserve important observable
failure cases as the product evolves.
