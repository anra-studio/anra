# Testing Anra

The repository keeps BDD end-to-end scenarios as its shared acceptance contract.
Read the [agent features](../tests/e2e/features/agent.feature) and
[peer features](../tests/e2e/features/peers.feature), plus the
[CLI features](../tests/e2e/features/cli.feature) and
[TOML configuration features](../tests/e2e/features/config.feature) and
[application embedding features](../tests/e2e/features/application.feature), to review what Anra
promises.
The [running-agent discovery features](../tests/e2e/features/discovery.feature)
cover two registered servers, bidirectional and concurrent greetings, restart,
conflicting runtimes, ownership rejection, token-free peer communication,
graceful disconnect and retained identities after crashes. The
[registry scenarios](../tests/e2e/features/registry.feature) exercise generated
Connect calls, persistent metadata, controlled-clock presence, conditional updates,
filtered discovery, tombstones, stale runtimes and lost acknowledgement recovery.
The [ADK features](../tests/e2e/features/adk.feature) cover built-in tool execution,
model-driven peer discovery and messaging, persisted tool history, and rejection
of tool configuration. The same Given/When/Then steps execute with
[Godog](https://github.com/cucumber/godog/tree/v0.15.1), so the specifications
and checks stay together. Undefined and pending steps fail the suite.

## Running scenarios

```sh
mise run test  # Build, schema checks, generated bindings, unit tests, and E2E scenarios.
mise run unit  # Pure domain logic and codecs under pkg/.
mise run e2e   # Only the BDD E2E scenarios.
GOFLAGS=-race mise run test  # Include unit tests and builds in race detection.
# Go alone, without mise:
go test -count=1 -v ./tests/e2e
# Select a scenario using its Go subtest name:
go test -count=1 -v ./tests/e2e -run 'TestFeatures/A_grant_keeps_its_original_snapshot'
```

## Investigating in the browser

Install the optional reporter with Node.js 22 or newer and npm:

```sh
npm ci
mise run e2e-report
# Equivalent command, with optional Go test arguments:
npm run e2e:report
npm run e2e:report -- -run 'TestFeatures/A_grant_keeps_its_original_snapshot'
# Open the latest report on macOS:
open reports/e2e/latest.html
```

The [Cucumber HTML Reporter](https://github.com/gkushang/cucumber-html-reporter)
shows features, scenarios, Given/When/Then results, durations, and failure
details. Reports are generated after failed test runs too, and the command
still exits with the test failure. A Go build failure cannot produce scenario
results; check the console output in that case.

Each run writes `reports/e2e/<timestamp>/index.html` and its `cucumber.json`.
`reports/e2e/latest.html` opens the newest report; older reports remain available.
On scenario failure, its closed database files and a `failure.json` snapshot
are saved under that run's `artifacts/scenario-*/` directory. The snapshot
includes the scenario name, error, model request, borrow request, decision, and
first page when available. These are separate files to inspect alongside the
HTML report; HTTP traffic is not captured automatically.

Dependencies and generated reports are ignored by Git. Delete `reports/e2e/`
when you no longer need its run history. Routine `mise run test` and `mise run e2e`
continue to work with Go alone; Node is used only for HTML reporting.

Scenarios use real Anra agents, tape-go bbolt storage, the grant journal,
discovery, the official A2A SDK, and Connect over localhost HTTP. Each scenario
gets its own temporary files and servers, with cleanup after success or failure.
Reporting runs preserve a copy of failed scenarios' files before cleanup.
Restart steps close and reopen the database files and rebuild the server on a
new port. Pagination checks include a view larger than one full page. Expiry
uses a controlled clock rather than sleeps.

Model responses and lender permission decisions are deterministic fixtures.
The suite needs no model credentials or external service once Go dependencies
are installed. It checks Anra's behavior around model calls; it does not measure
a real model's answer quality. Library scenarios use public Go APIs and peer
HTTP endpoints. CLI scenarios build `cmd/anra` once per suite and launch real
processes using tape-go's ADK runtime and real provider HTTP requests to a
deterministic local model fixture, temporary disk storage, and localhost HTTP. They check local persistence, two-agent messaging, receiver restart,
missing or malformed caller identities, required `--config` files, TOML agent selection, rejected
flag and environment overrides, running-agent discovery, and invalid configuration.
All CLI sends use registered agent names. Direct peer flags and TOML settings
are rejected, as are agent-setting flags and `ANRA_*` variables.
Process presence transitions use bounded polling of the public registry API;
the crashing process is killed while the remaining agent keeps heartbeating.
Registry contract scenarios use a controlled clock, real Connect HTTP calls and
temporary directory storage. Registry restart resets presence and retains metadata.
Each subprocess has
inherited `ANRA_` settings and model credentials removed from its environment;
scenario fixtures supply only deterministic settings and model fixture keys.
TapeView scenarios also use trusted caller identities without shared tokens,
including denial when another identified peer tries to use a borrower's grant.
The application embedding scenarios call `config`, `peer`, `discovery`, and
`registry` directly, checking storage interoperability with CLI processes,
peer communication, restart, invalid identities and readiness failure cleanup.
The suite does not exercise an application UI.

## Pure logic unit tests

Keep focused `pkg/**/*_test.go` tests for pure logic and algorithm boundaries:
state transitions, recursive protocol filtering, codecs, and number precision.
These tests are committed and run by `mise run test`. They need no external
dependencies, mock frameworks, or interfaces introduced only for testing.
Do not unit-test configuration forwarding or struct field assignments.

```sh
mise run unit
go test -race ./pkg/model/entry ./pkg/mcp
```

Keep E2E scenarios for core journeys through public APIs, real network
transport, and persistent storage, including isolation, concurrency, and atomic
failure. Do not enumerate large parameter matrices in Scenario Outlines or
inflate fixtures to increase test counts or coverage. Architecture, clear
responsibilities, and meaningful assertions determine test quality.

The [MCP feature](../tests/e2e/features/mcp.feature) contains such journeys;
content codecs, privacy, business JSON precision, and pure state transitions
live in package unit tests. Remove disposable investigative
scaffolding before committing. [AGENTS.md](../AGENTS.md) defines this policy.

## Adding behavior

Write new and modified feature scenarios in Chinese. Start with a concrete
scenario describing a person's goal and observable outcome. Reuse steps where
their meaning is the same, and exercise the public
boundary through the actual runtime, transport, and storage. Keep setup and
implementation detail in the step definitions, not in the feature language.
Each scenario must stand alone. Include denied access and failure behavior
when they are part of the promise being changed.

Use unit tests for pure logic boundaries and BDD scenarios for observable
integration behavior; avoid repeating the same parameter matrix across layers.
