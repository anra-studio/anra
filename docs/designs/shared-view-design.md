# 共享 View 与借阅服务

状态：approved。按本文决议实现共享 View、Lease 和 Quote；实现验收结果另行记录。

MCP 和 peer 读取同一类已提交上下文，统一使用 `pkg/flow` 的借阅流程与 `pkg/model/view` 的内容表示和来源信封。peer 也只允许借阅已提交的 View，去掉空范围授权和自由选段能力。

本次允许直接调整类型、协议和存储格式。不设计历史数据迁移、兼容解码、旧字段、过渡别名或双入口回退。

## 重构前的问题

- `pkg/mcp/view.go` 自行读取 Tape、生成 `TapeViewResult` / `ViewEntry`，从 MCP Grant 组装 Channel 和 Provenance。
- `pkg/a2a/service.go` 同时承担协议服务、范围选择、lease 签发、读取、分页和撤销。现有 `pb.View` 是授权，`pb.TapeView` 是没有授权的快照引用，两者名称没有表达差异。
- `pkg/agent/tools/borrow.go` 再从 peer decision 和读取结果 组装另一份 `borrowed_context`。两条路径已经共用 Entry 内容，却没有共用借阅模型。
- `entry.Channel` 和 `entry.Provenance` 描述消费权限，却放在持久化条目包内。

只移动响应结构体不能解决这些问题；共享的业务规则和 lease 状态必须有同一个所有者。

## 领域概念

| 概念 | 内容 | 所有者与约束 |
| --- | --- | --- |
| Ref | Source、Scopes | 定位一个真实、已提交、不可变的多段 View，本身不授予读取权限 |
| Lease | ID、Caller、Ref、ExpiresAt | 借阅服务签发；精确绑定一个 Ref 和一个调用方 |
| Quote | Channel、Provenance、公开条目 | 表达合作交互中的引用内容与来源；借阅服务依据当前有效 lease 构造 borrowed 通道的 Quote |
| Entry | Seq、Kind、Timestamp、公开内容 | `entry.Entry` 提供多模态内容，anchor 提供公开 Body；读取结果不暴露原始存储条目 |

### 已提交的 View

Ref 表示一个已提交快照中的完整选定范围，使用 `Scopes` 表达一段或多段内容：

- Source 标识 agent 及其 Tape incarnation。
- `Ref.Scopes` 是非空的 `EntryRange` 列表，JSON / ProtoJSON 字段名为 `scopes`；每段都是非空半开区间 `[SeqS, SeqE)`。
- 各段属于同一个 Source，按 SeqS 递增且互不重叠，可以相邻，不限制为单段。
- 需要整个 View 的末端序号时，从最后一段 Scope 的 SeqE 取得，不单独存储一个末端字段，也不从最后一个返回条目推导。Scopes 间的空隙不属于授权内容；读取不能取最小起点和最大终点来补齐空隙。
- 所有序号均为无前导零的非零 ASCII 十进制字符串，保持任意精度。
- 原生单个完整 Turn 生成一段 `[start + 1, close)`，排除起止控制锚点。多段 View 由已提交视图的所有者确定并固定全部 Scopes；单 Turn 的一段表示不限制 Ref 的通用段数。失败、取消和未结束的 Turn 不成为可借阅内容。

已提交性由视图的生成和提交规则保证，不能由某个末端序号证明。原生 Agent 的已提交视图索引负责解析 Ref，确认 Source 和完整 Scopes 对应同一个真实的已提交 View。借阅服务信任该解析结果，不再次对 Provider 输出进行格式扫描，也不通过当前 Tape 末尾推导新范围。消费方不自动排序、合并或裁剪 Scopes 来替换原 Ref。

peer 的全部借阅路径都经过这个解析入口。按 purpose/query 选择时只能选择一个已提交 View；指定 Ref 时必须匹配 Source 和完整有序 Scopes。请求中的 bounds 若保留，只能作为资格约束：不能完整容纳该 View 的全部段就拒绝，不能据此截取子段或减少段数。

没有可用窗口返回 denied，需要本地决策时可以返回 pending。只有 granted 决策携带 lease；不再签发空范围 lease，也不允许策略返回任意 Tape 范围。锚点导出的验收数据同样必须先形成真实的已提交窗口。

### 借阅 lease

Lease 精确绑定 `Caller + Ref`，包含公开 ID 和有效期。ID 用于引用授权，不是 Bearer secret。其 Ref 在签发后不变，后续推理、追加数据或新窗口都不改变已有 lease。

请求决策由 `borrow.Journal` 原子持久化，lease 的签发与有效期由 `grant.Service` 管理，状态存入独立 sidecar，详见 [View Handler 模型](tape-view-handlers-design.md)：

```mermaid
stateDiagram-v2
    [*] --> pending: 等待本地决策
    [*] --> denied: 不允许借阅
    [*] --> active: 已提交 View 获准借阅
    pending --> denied: 本地拒绝
    pending --> active: 本地批准已提交 View
    active --> released: 调用方释放或本地撤销
    active --> expired: 到达有效期
    expired --> released: 显式释放
    released --> released: 幂等释放
```

expired 由有效期判定，不要求额外写一份过期状态。借阅请求以调用方和 request ID 实现幂等；重用 request ID 返回原决定，不因输入变化重新审批或延长有效期。释放保持幂等，终止后的 lease 不重新激活。

请求转换规则集中声明在 `pkg/flow/borrow/state.go`，使用 looplab/fsm。新请求可进入 pending、granted 或 denied，pending 只能完成为 granted 或 denied。grant 根据已提交 lease 的释放标记和时钟判断有效期；释放后的 lease 不重新激活。状态机回调不执行 I/O，只有 sidecar 追加成功后结果才对调用方可见。

`Borrow` 负责新请求的 Review 策略与首次记录；`Approve` 批准已有 pending 请求，`Reject` 拒绝已有 pending 请求，均不重新执行 Review。已完成请求再次审批返回冲突；Approve 无合格 View 时返回不可用并保留 pending。Approve 在提交锁内重新比较此前读取的 pending 快照，竞争审批不能覆盖已提交结果。

`tape-go v0.2.0-anra.5` 原生提供 Tape、OverView 与 handler Registry，Anra 已删除本地 `pkg/tape` 包装。`grant.Service` 从完整原生视图签发候选 lease，`borrow.Journal` 将其与请求决策一次追加为 `anra:lease-decision/v1` 记录，恢复请求及 lease 索引。直接调用 handler 得到的候选 lease 不能读取或释放。共享 Journal 的锁覆盖预期状态比较、lease ID 唯一性与提交。sidecar 借用 tape-go storage 实现，不构造第二个 Tape，也不提供上下文 View。详见 [flow 边界](flow-boundaries-design.md)。

每次读取按 Caller 和 Lease ID 解析当前授权，检查释放、撤销和到期状态，然后读取固定 Ref。读取不调用 Borrow、不续租、不重新选择 View，也不进入推理 Admission。

共享服务为 Read 的授权判断和 Release / Revoke 的提交定义一致的先后顺序。终止状态先提交的后续读取必须拒绝；已经获得读取许可的在途请求可以完成，撤销不能收回已发送的内容。

### Quote：引用内容与来源

所有借阅读取统一产生 `view.Quote`：`channel: borrowed`、一份 Provenance，以及公开条目。Provenance 包含 Caller、Lease ID、Source 和完整 Scopes。删除独立的 MCP Grant ID 来源链；Bearer 凭证只是引用这个 lease。

Channel 和 Provenance 从 `entry` 移到 `view`。它们由已验证的借阅关系产生，不能从条目内容、客户端参数、历史 user/model role 或自述身份中推导。Primary / Observation 仍由原生消费方赋予；两个借阅入口只能输出 Borrowed。

Quote 表达合作交互中的引用内容及来源，包含 Channel、Provenance 和 Entries。借阅服务根据当前有效 lease 将读取结果标为 borrowed；Quote 本身不授予权限。公开 Entry 不重复复制 Channel、Provenance 或整份 Ref。协议 ReadRequest 仅携带 lease_id，ReadResponse 返回单个完整 Quote，没有分页参数或续读令牌。

借阅 API 为 `Read(ctx, caller, leaseID) (io.Reader, error)`，授权后惰性输出公开 Quote JSON。打开流不访问正文，调用方用自己的字节缓冲消费。原生 Tape Reader 与 io.MultiReader 顺序遍历全部授权 Scopes，允许段间间隙，例如 `[9,14)` 与 `[31,42)` 之间的内容不会读取。流按条目导出公开内容，缺失条目、解码失败或取消会终止读取，错误后不会补上 Quote 的结束标记。流不拥有底层 storage，也不启动 goroutine。宿主关闭存储前，调用方需完成读取或取消上下文。

A2A 和 MCP 在响应前通过 `view.Decode` 消费整个流，检查完整 JSON 与尾部错误；失败不返回部分 Quote。网络 Read 保持 unary RPC，Go 调用方可以增量消费 Reader。客户端校验完整来源、范围及逐条序号，返回单个 Quote；借阅成功后的委托进度更新也以完整校验通过为前提。

内容继续复用现有领域对象：

- 模型内容使用 `Entry.Content()` 的 SDK-backed 多模态结构，保留 Part 顺序、思考文本及 thought 标记、图片、音频、文件和工具内容。
- 原生锚点调用 `Record.Body()`，排除 OwnerEpoch；未知锚点 schema 的公开内容为 null，保留条目序号。
- 不导出执行状态、任意扩展或 provider thought signatures；读取公开表示不修改 Tape 中的原始事件。

公开 Entry 存在模型内容、原生锚点和第三方文本三种实际内容形态，不再重新手写 GenAI Part 模型或引入 Projection。异构内容的编码集中在共享出口完成；协议适配不再自行判断条目种类或删改 Parts。

保留不支持内容和编码失败的显式错误，不静默删除条目。统一取消现有 MCP 对空 Parts 的独立过滤，两条协议对同一 View 返回相同条目序列。业务 JSON 中的数字词法必须保持精度，禁止经 `map[string]any` 解码后重新编码。模型工具因 ADK 转换需要而使用 JSON 文本字符串的处理留在工具适配边界。

Channel 是消费权限约定，不能保证任意模型免疫 Prompt 注入。原生消费者将 Quote 放入只读 `borrowed_context` 工具结果，历史内容不展开为当前 Session 的主指令；实际工具执行权限仍由运行时控制。

## 模块职责与依赖

`pkg/model/view` 只拥有公共内容模型、导出和编解码；`pkg/flow/borrow` 拥有借阅审批、请求状态机、Journal、授权读取流，`pkg/flow/grant` 拥有 lease 签发与有效期。根包 `pkg/model` 继续负责 LLM provider 初始化。flow 直接复用原生 View、grant handler 和 sidecar，不调用本进程的 HTTP 接口，也不增加第二套持久化状态。

| 模块 | 目标职责 |
| --- | --- |
| `agent` | 提交和恢复 Turn，维护已提交窗口索引，解析或选择准确的 Ref |
| `model/view` | Quote、Entry、Provenance、Channel、公开内容导出及编解码 |
| `flow` | 通过 Set 组合具体流程 |
| `flow/borrow` | 借阅策略、请求决策、共享 Journal、授权读取流 |
| `flow/grant` | 原生 view handler、候选 lease 签发、有效期与释放 |
| `flow/clbr` | 委托、BUSY 抑制、完整借阅后的进度更新 |
| `model/entry` | 存储条目、扩展访问、SDK-backed 模型内容 |
| `model/anchor` | 原生执行记录及公开 Body |
| tape-go `tape` / `view` | 原生 Tape、OverView 和 handler 注册与分发 |
| `mcp` | Bearer 认证、请求上下文、交接与角色权限、MCP 编解码和公开错误映射 |
| `a2a` | peer 身份、extension 协商、Connect/REST 与 A2A 编解码、领域服务调用和错误映射 |
| `agent/tools` | 发现 peer、发起借阅、完整读取、把共享 Quote 转为 ADK 工具结果 |
| host / `peer` | 创建和注入同一个借阅服务，拥有依赖初始化、关闭及凭证发行 |

共享服务只定义已经有调用方的能力：Borrow、Get、Read、Release 和本地 Approve / Reject / Revoke。控制器和 peer 均通过 Borrow 申请 lease，控制器在 BorrowRequest.View 中指定准确的已提交 Ref。新请求统一执行 borrow.Config.Review，没有额外传入预批准 Selection 或绕过审批的签发入口；未配置 Review 时沿用一小时的默认授权策略。已记录请求直接重放原决策，不重复审批或续租。Approve / Reject 只完成已有 pending 决策，不成为新的远程管理接口。

现有 Exporter 有文本和结构化内容两种实际实现，具体导出器保留在 model/view，消费接口定义在 flow/borrow。每个部署为两个协议注入同一个内容策略；MCP 不再有独立的 `exportEntry`。不为测试新增 Provider 框架、通用 middleware 框架或一组仅转发字段的接口。

### 类型与 Protobuf

将 Source、EntryRange、Ref 和 Lease 等公共数据声明从 A2A 命名空间抽到中立的 `proto/anra/model/view/v1/view.proto`，生成到 `gen/anra/model/view/v1`。原 `TapeView` 命名为 Ref，只包含 Source 和 `repeated EntryRange scopes`，不保留单段 scope 字段、独立末端字段或 `max_items = 1` 限制。原授权 `View` 命名为 Lease，Lease 直接引用单个完整 Ref，不重复声明其中的来源和范围；一个 Ref 内可以有多段。

借阅业务请求和 Decision 同样由公共 schema 声明，供共享服务及 grant sidecar 编解码使用，避免通过 A2A 请求类型反向耦合协议。RPC 专有的响应封装及 HTTP annotations 继续位于各协议 schema 中。

Source、EntryRange、Ref、Lease 和决策直接复用公共生成消息，不另写同字段 Go 副本或旧名 alias。Quote 的 JSON 表示使用 RawMessage 内容，协议转换负责将其映射为 protobuf 的精确保真 Payload。`pkg/model/view` 拥有内容模型和导出行为，`pkg/flow` 拥有借阅生命周期。JSON / ProtoJSON 编码属于边界，统一使用明确的字段名。

Protobuf 的字段约束使用 Protovalidate，非 Protobuf 的构建约束使用 validator 标签。在外部输入或对象构建边界完成格式验证；共享服务执行真实的提交解析、授权和状态转换规则，消费方不递归重复验证受信任结果。

这样 anchor 依赖公共生成值而不依赖 `pkg/flow`，grant handler 同样直接使用公共生成消息；共享服务可以使用 anchor 公开内容，不形成 `view -> anchor -> view` 的包循环。A2A 的客户端、生成消息和错误类型不会反向进入共享业务服务。

修改 `.proto` 后重新生成，禁止直接编辑 `tape.pb.go`。直接删除旧的重复声明，不保留兼容字段或历史存储读取路径。

## 两个入口的调用链

```mermaid
flowchart TD
    Controller[控制器 / host] -->|Borrow 指定的已提交 Ref| Service[borrow.Service]
    Peer[peer 身份 + Borrow / Read] --> A2A[A2A 适配器]
    A2A --> Service
    Token[Bearer 凭证] --> MCP[MCP middleware + 宿主授权]
    MCP -->|Caller + Lease ID| Service
    Service -->|选择和解析 Ref| Agent[Agent 已提交窗口索引]
    Service --> Binding[tape-go 原生 OverView]
    Binding --> Grant[grant handler]
    Service --> Journal[borrow.Journal]
    Grant -->|授权检查与释放| Journal
    Journal --> Sidecar[授权 sidecar 与状态投影]
    Service --> Tape[固定 Ref 的 Tape 内容]
    Service --> Quote[统一的 borrowed Quote]
    Quote --> MCPResponse[MCP 结果编码]
    Quote --> PeerResponse[peer 响应编码]
    PeerResponse --> Tool[ADK borrowed_context]
```

宿主通过 AccessProvider 验证凭证并解析目标：每次 HTTP 请求执行 Authenticate，每次工具调用执行 Authorize，检查当前凭证、精确的 AnchorSeq 和工具权限，返回仅包含 Caller、Lease ID 和 RoleRef 的 ReadTarget。MCP 不再定义 Grant、凭证有效期或权限列表。凭证策略与交接绑定由宿主负责；lease 的有效期、撤销和来源由共享服务负责。不再携带一份可绕过借阅流程直接读取的独立 TapeView 授权。

`read_tape_view` 先执行现有交接和工具权限检查，再通过共享服务读取绑定 lease。`read_role` 仍读取绑定的角色 revision；Tape lease 不提供角色权限。若交接承诺了 Ref，控制器签发时必须确保它与绑定 lease 完全相同，不能在读取时重借一个新 View 来代替。

认证失败继续映射为 401 / Bearer challenge，认证后端不可用为 503；工具阶段的失效或不属于当前调用方的 lease 映射为公开 permission_denied，不能暴露其他调用方的授权是否存在。两种协议映射同一组领域错误，不把底层存储诊断返回给客户端。

保留 Origin 先于认证、重复 Authorization 拒绝、请求级身份隔离和所有 MCP 响应 no-store。Bearer 不增加字符集限制，也不进入日志、Tape 或 Provenance。请求和内容响应不增加固定 body 上限；读取流保留完整多模态条目。

## 实现与验收

1. 建立公共 Ref / Lease schema、`pkg/model/view` 和 `pkg/flow`，明确上面的约束及字段名，重新生成协议绑定。
2. 将 A2A 借阅业务和 grant sidecar 接入共享服务；所有借阅改为解析已提交 View，删除自由范围 Policy 和空 lease 分支。原生 anchor、委托提示和 grant sidecar 使用公共 Ref / Lease。
3. 将公开条目出口、Channel 和 Provenance 收到共享服务，删除 MCP 与 peer 各自的来源组装及内容分支。
4. MCP 凭证改为绑定同一服务的 lease，删除直接 TapeReader 旁路；host 注入同一个实例。同步更新协议与运行文档。
5. 通过真实 HTTP、Tape 和 grant sidecar 完成验收，再提交实现。

实现位置：公共 schema 位于 `proto/anra/model/view/v1/view.proto`；共享借阅服务和请求状态机位于 `pkg/flow/borrow`，Quote 和内容导出位于 `pkg/model/view`；原授权专用存储已替换为 tape-go 原生 OverView 与 flow 内的 grant handler / sidecar。MCP 和 A2A 注入同一服务实例，MCP 已移除直接 TapeReader 路径。

复用并调整现有中文 E2E，重点覆盖：

- 同一调用方和 lease 经 MCP、peer 得到相同 Ref、Provenance、条目顺序及完整内容；复用真实提交路径构造至少两段不相邻内容，验证读取跨段跳过间隙且不重复、不丢失授权条目。
- 没有成功提交窗口时拒绝借阅；未结束或失败 Turn、空范围及自由区间不能生成 lease。锚点与大型媒体用例通过真实提交窗口构造。
- Bearer 或 peer 调用方不能读取另一调用方的 lease，参数不能改写 Source 或 Scopes、插入间隙或增减段；角色工具权限独立执行。
- 追加新 Turn 和服务重建不扩大已有 Ref；释放、撤销及到期对两个入口同时生效。Read 不签发、不续租、不进入推理。
- 多模态、思考内容、原生和第三方锚点、JSON 数字精度及私有元数据边界在两个入口保持一致。

纯算法与编解码边界才增加单测，不为字段搬运添加 mock 或测试接口。实现验收运行 `GOFLAGS=-race mise run test`，结果随实现提交记录。

## 拆分 flow 前的验收记录

View Handler 与授权 sidecar 接入后，`mise run test` 全量通过：252 个场景、1218 个步骤，包括公共协议生成一致性、构建、纯逻辑测试和启用 race 检测的真实网络/持久化 E2E。新增场景覆盖 bbolt 与 JSONL sidecar、注册与绑定约束、跨 Source 拒绝、取消无追加，以及共享 handler 的竞争审批。Go 修改已执行 gofumpt。
