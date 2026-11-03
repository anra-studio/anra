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

# Go code review conventions

Distilled from Dave Cheney's [The Zen of Go][zen], Rob Pike's
[Go Proverbs][proverbs], and the official [Effective Go][eg]. Check a source's
age before arguing from it: Effective Go was written for the 2009 release and
predates generics, modules, `any`, `errors.Is`/`errors.As`, and the Go 1.22 loop
variable change.

[zen]: https://dave.cheney.net/2020/02/23/the-zen-of-go
[proverbs]: https://go-proverbs.github.io/
[eg]: https://go.dev/doc/effective_go

## Classify every finding before reporting it

- Language convention: naming, formatting, comment shape. Let `gofmt`,
  `go vet`, and `buf lint` own it, leave the rest as a nit, and never block on
  it.
- Design tradeoff: two options with real costs that depend on this repository's
  constraints. State both costs and who pays them; the author may reject it.
- Real defect: demonstrable through a failing assertion, race report, panic,
  wrong error, or timeout. Block on it and attach the demonstration.
- A proverb or idiom can only support a tradeoff comment. Reaching "defect"
  requires an observable failure. "This is not idiomatic" is not a finding
  because it names no consequence.

## Hold the code to these rules

- Name a package for the single kind of thing it provides, using one noun. Do
  not repeat the package name in its exported identifiers. When the name no
  longer matches the requirement, replace the package rather than widen it.
- Prefer replacing a component over making it flexible in advance. Add
  interfaces, hooks, or type parameters when a second implementation or caller
  already exists, not because one might appear.
- Keep the zero value usable. Require a constructor only when initialization
  carries an invariant.
- Avoid package-level mutable state. Test the design with three questions: can
  the code be tested in isolation, tested in parallel, and instantiated more
  than once? Package variables that fail those questions are coupling, not
  convenience.
- Solve the failure path before the happy path. Return early on failed
  preconditions, keep the success path and its return at the lowest indentation,
  and omit the `else` after a branch that returns.
- Never discard an error. Handle it, wrap it with `%w` and context about the
  operation and subject, or state in a comment why it is safe to ignore. Make
  failures distinguishable with `errors.Is`/`errors.As` instead of string
  matching, so errors stay values with structure.
- Reserve `panic` for state that cannot be repaired, and only during
  initialization of a command. Library and package code returns errors and
  converts internal panics at its exported boundary.
- Before starting a goroutine, know under what condition it stops, what makes
  that condition arise, and what signal confirms it has stopped. Tie the
  resources it owns to its lifetime and bound how many run concurrently.
- Let the caller decide to be asynchronous. Exported functions do not spawn
  untracked goroutines; they return something the caller can cancel and wait on.
- Prove performance claims with `go test -bench` and a profile. Reject dogma
  about `defer`, cgo, atomics, or allocation without measurement.
- Treat slices, maps, and channel values as shared references. Return new slices
  from functions that append, copy when transferring ownership, and initialize a
  map before writing. Prefer composite literals with field names so added fields
  cannot silently shift positional values.
- Keep receivers consistent per type: if one method needs a pointer receiver,
  use pointer receivers throughout.
- Keep interfaces small and define them where they are consumed. Do not export a
  type that exists only to implement an interface; return the interface from its
  constructor. Use embedded structs to reuse behavior, not to model
  inheritance, and forward explicitly when the embedded receiver is wrong.
- Document the contract, not the code: preconditions, failure semantics,
  concurrency safety, and zero-value behavior. Keep a comment that records why a
  tradeoff was chosen, since that is what stops it from being reverted.

## Do not apply proverbs mechanically

- A mutex guarding a counter is correct. "Share memory by communicating" and
  "channels orchestrate; mutexes serialize" are a default, not a prohibition.
- Constant tables and `init` defaults are not the package state the guidance
  targets. Test such state with the three questions above.
- Do not require a channel, an interface, or a generic type where a concrete
  value is clearer. Clarity outranks demonstrating a language feature.
- Do not report superseded advice: use `any` rather than `interface{}`, current
  error wrapping rather than the type-assertion pattern in Effective Go, and do
  not raise pre-Go-1.22 loop variable capture against this module.
- An observable behavior change without a matching `tests/e2e/features` scenario
  is a defect against this repository's contract, however clean the code is.

# Validation policy

- Validate non-Protobuf structures with `go-playground/validator/v10`, declaring
  field constraints in validation tags.
- Validate Protobuf messages and fields with Protovalidate, declaring constraints
  in the `.proto` schema. Reuse these rules instead of duplicating them in
  handwritten field checks.

# Repository testing policy

## 核心分工与反刷分纪律
- 严禁为了“测试覆盖率指标”或“用例数量”刷题堆砌代码。工程质量的标准是架构精炼、职责单一、无过度设计，而不是用例堆得多。
- 测试分为两层，职责严格隔离：
  1. **单元测试（`pkg/**/*_test.go`）**：
     - **仅限覆盖纯逻辑与算法边界**：如状态机跃迁、协议白名单递归过滤、数据编解码与大数值精度保真等无外部依赖的叶子函数。
     - **严禁为了写单测引入 Mock 框架或无意义的接口抽象**；严禁测试纯胶水代码、配置透传或结构体字段赋值。
  2. **E2E 验收测试（`tests/e2e/`）**：
     - 聚焦**真实公共 API、真实网络传输与持久化存储的端到端集成旅程**。
     - 覆盖核心成功链路、安全隔离边界、并发确定性与原子失败。**严禁在 E2E 中构造上百个穷举参数的伪集成用例**。
     - **后续新增与修改的 E2E feature 场景描述，统一使用中文撰写**，表达精准直接，消除形式主义。

## 运行与验收
- 运行 `mise run test` 执行全量仓库检查，运行 `mise run e2e` 进行端到端开发验证。
- 严禁跳过、弱化失败断言以迎合测试通过。不要在 PR 中提交一次性的临时脚手架测试。
- E2E 验收套件使用 Go 的测试入口（`tests/e2e/*_test.go`），属于 BDD 验收基础设施。

# Commit message policy

- Every Git commit message must use the format `{scope}: {summary of changes}`.
- The scope may be a repository directory path, such as `pkg/model`, or a
  general engineering term, such as `ci`, `mod`, or `security`.
