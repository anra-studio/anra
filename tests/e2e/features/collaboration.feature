Feature: 通过 Anchor 恢复非阻塞节点协作
  每个角色同一时刻只接收一轮推理。角色返回 BUSY 时，调用方仍可借阅已授权的不可变视图。

  Scenario: 标准 A2A 消息通过 ProtoJSON 传输协作元数据并支持标准流式回复
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then generated collaboration metadata roundtrips through A2A
    And the discovered agent streams a standard A2A answer

  Scenario: A2A 拒绝无效身份和委托元数据且不写入 Tape
    Given a collaboration agent
    Then A2A rejects invalid caller identities before reasoning
    And collaboration rejects request metadata with missing root before reasoning

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
    And generic A2A callers receive an unaccepted response with no hint
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

  Scenario: 取消推理保留 Tape 原记录并从模型历史排除未完成内容
    Given a collaboration agent
    When Alice's cancelled turn closes and its partial history is excluded

  Scenario: 进程被终止后恢复未完成轮次并保留原视图和授权
    Given a running team Peer directory
    And team agents "alice" and "bob" are serving
    And Alice is interrupted while reasoning after a published answer
    And the busy agent keeps online presence across a heartbeat window
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

  Scenario: 控制记录中的轮次重叠时拒绝启动
    Given a collaboration agent
    Then startup rejects a tape containing overlapping starts

  Scenario: 关闭 Tape 前先结束正在进行的推理
    Given a collaboration agent
    Then shutdown drains active reasoning before storage closes
