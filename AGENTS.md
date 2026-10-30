# Domain modeling and maintainability

- Treat each feature as a change to the system's domain model. Before coding,
  understand the underlying intent and identify the relevant concepts,
  responsibilities, relationships, state transitions, and invariants.
- Let that understanding guide module boundaries and interfaces. Give each
  domain concept a clear owner, keep related behavior together, and express
  business rules explicitly instead of scattering special cases across layers.
- Fit new behavior into a coherent model and refine existing abstractions when
  the requirement exposes a missing concept. Shipping a feature includes making
  its logic understandable and maintainable; satisfying the immediate behavior
  through ad hoc branches, duplicated rules, or vaguely named helpers is not
  sufficient.
- Keep abstractions proportional to the problem. Reuse established concepts
  where they fit, and introduce new ones only when they clarify actual behavior
  or responsibilities. Avoid speculative frameworks and unrelated refactoring.

# Validation policy

- Validate non-Protobuf structures with `go-playground/validator/v10`, declaring
  field constraints in validation tags.
- Validate Protobuf messages and fields with Protovalidate, declaring constraints
  in the `.proto` schema. Reuse these rules instead of duplicating them in
  handwritten field checks.

# Repository testing policy

- The committed acceptance contract is the executable BDD E2E suite under
  `tests/e2e/`. Describe observable behavior in `features/*.feature` using
  Given/When/Then and implement steps through public APIs, real HTTP transport,
  and temporary disk storage. Keep model responses deterministic.
- Add or update an E2E scenario when changing observable behavior. Run
  `mise run test` for the complete repository checks, or `mise run e2e` while
  developing scenarios. Do not skip or weaken failing scenarios to pass checks.
- Create and run temporary unit tests whenever they help investigate, implement,
  or evaluate a change. Choose these checks autonomously when needed, without
  waiting for the user to request them. Keep them local and uncommitted; never
  include unit tests in commits or pull requests. Package-local
  `*_test.go` files are ignored; run them explicitly with `go test ./pkg/...`
  (or the specific package). Remove disposable tests when finished and never
  force-add them. Only E2E test code belongs in committed `*_test.go` files.
- Report the checks performed and their results. The BDD runner uses Go's
  testing entry point; its `_test.go` files are E2E infrastructure, not unit tests.

# Commit message policy

- Every Git commit message must use the format `{scope}: {summary of changes}`.
- The scope may be a repository directory path, such as `pkg/model`, or a
  general engineering term, such as `ci`, `mod`, or `security`.
