Feature: 通过 Anchor 恢复非阻塞节点协作
  每个角色同一时刻只接收一轮推理。角色返回 BUSY 时，调用方仍可借阅已授权的不可变视图。

  Scenario: 标准 A2A 消息通过 ProtoJSON 传输协作元数据
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then generated collaboration metadata roundtrips through A2A

  Scenario: A2A 拒绝缺失或格式无效的调用方身份且不写入 Tape
    Given a collaboration agent
    Then A2A rejects invalid caller identities before reasoning

  Scenario Outline: 无效委托元数据在推理前被拒绝且不写入 Tape
    Given a collaboration agent
    Then collaboration rejects request metadata with <problem> before reasoning

    Examples:
      | problem                      |
      | missing root                 |
      | unknown field                |

  Scenario Outline: A2A 客户端拒绝带重试提示或缺少正文的完成响应
    Given a collaboration agent
    Then the client rejects response metadata with <problem>

    Examples:
      | problem                   |
      | completed retry hint      |
      | empty completed answer    |

  Scenario: 并发请求只有一个能进入推理
    Given a collaboration agent
    When simultaneous callers admit exactly one turn

  Scenario: 没有已完成视图的忙碌角色拒绝新请求且不泄露内容
    Given a collaboration agent
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    Then the response is BUSY without another turn or model call
    And BUSY reveals no view or private control fields
    And a guessed reference cannot obtain private or control entries

  Scenario: 角色推理期间可借阅先前已提交的视图，重启后视图保持不变
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    Then the response is BUSY without another turn or model call
    When Bob borrows the BUSY view while Alice remains occupied
    Then the view grant contains exactly the completed context window
    And another trusted requester gets its own grant but cannot read or release Bob's grant
    And view requests reject a changed reference and incompatible bounds
    When Alice finishes and commits a newer context window
    And Alice restarts with the same source and the grant remains readable
    Then the view grant contains exactly the completed context window

  Scenario: 祖先回调通过 BUSY 和带 lease 来源的引用内容完成推理
    Given a collaboration agent
    And Alice has completed a shareable answer
    When cyclic peer reasoning completes using BUSY and borrowed findings

  Scenario: 独立根请求相互委托时不会阻塞
    Given a collaboration agent
    When independent roots consult one another without blocking

  Scenario: 同轮的并行委托共享根请求并保留独立路径
    Given a collaboration agent
    When Alice consults Bob and Charlie in separate turns
    Then sibling calls share their turn root and keep independent paths

  Scenario: 十跳委托保留完整祖先链
    Given a collaboration agent
    Then Alice completes a ten-hop delegation and retains its full ancestry

  Scenario: 修改提示词不能在同一轮内反复委托 BUSY 节点
    Given a collaboration agent
    When repeated BUSY delegation is suppressed only within each turn

  Scenario: 借出方选择视图，借到更新发现后才允许再次委托
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then Bob resumes reasoning only after borrowing newer lender-selected findings

  Scenario: 取消推理保留 Tape 原记录并从模型历史排除未完成内容
    Given a collaboration agent
    When Alice's cancelled turn closes and its partial history is excluded

  Scenario: 委托已过期时不调用模型或节点
    Given a collaboration agent
    Then an expired delegation deadline rejects reasoning before model or peer calls

  Scenario: 借阅视图要求借出方声明支持的协议版本
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then legacy lenders cannot silently accept view references

  Scenario: 进程被终止后恢复未完成轮次并保留原视图和授权
    Given a running team Peer directory
    And team agents "alice" and "bob" are serving
    And Alice is interrupted while reasoning after a published answer
    When I try to ask running agent "alice" to "rejected CLI callback"
    Then the CLI command fails with "busy: request was not accepted"
    When team agent "alice" crashes
    And the interrupted tape retains an unmatched tool call and state delta
    And its presence eventually becomes unknown
    And I restart team agent "alice"
    Then the recovered agent keeps its source, view, grant, and physical history
    When I stop the team agents
    Then exactly one abandoned close excludes the unfinished turn

  Scenario: 写入轮次结束记录失败后拒绝新的推理
    Given a collaboration agent
    Then an unresolved terminal append fences further reasoning

  Scenario Outline: 控制记录损坏或轮次重叠时拒绝启动
    Given a collaboration agent
    Then startup rejects a tape containing <problem>

    Examples:
      | problem                      |
      | malformed record             |
      | overlapping starts           |

  Scenario: 外部 Anchor 与原生记录共存且不影响借阅和启动恢复
    Given a collaboration agent
    And Alice's tape contains a custom anchor with schema "example.org/anchor/v1"
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    And Bob borrows the BUSY view while Alice remains occupied
    And Alice finishes and commits a newer context window
    And Alice restarts with the same source and the grant remains readable
    Then the view grant contains exactly the completed context window
    And Alice can continue reasoning after schema dispatch recovery

  Scenario: 通用 A2A 调用方收到 BUSY 文本且看不到私有提示
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then generic A2A callers receive an unaccepted response with no hint

  Scenario: 关闭 Tape 前先结束正在进行的推理
    Given a collaboration agent
    Then shutdown drains active reasoning before storage closes

  Scenario: 接收方使用自身身份借阅传入的视图引用
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then Bob borrows supplied context without reentering Alice

  Scenario Outline: 不匹配的 lease 不能替换已提交视图的来源或 Scopes
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then a client rejects a view grant with mismatched <provenance>

    Examples:
      | provenance |
      | source     |
      | range      |
