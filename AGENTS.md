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
