Feature: 按名称发现并联系节点
  目录按节点身份保留元数据和在线状态，角色之间直接通信。

  Background:
    Given a running team Peer directory

  Scenario: 两个节点无需共享凭证即可相互发现和通信
    Given team agents "alice" and "bob" are serving
    When I read the team directory
    Then the team directory lists "alice" and "bob"
    And the team cards require no bearer credential
    When running agent "alice" sends "hello from alice" to "bob"
    Then the CLI answer is "bob: hello from alice"
    When running agent "bob" sends "hello from bob" to "alice"
    Then the CLI answer is "alice: hello from bob"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                      |
      | user      | ask bob: hello from alice |
      | assistant | bob: hello from alice     |
      | user      | hello from bob            |
      | assistant | alice: hello from bob     |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text                      |
      | user      | hello from alice          |
      | assistant | bob: hello from alice     |
      | user      | ask alice: hello from bob |
      | assistant | alice: hello from bob     |

  Scenario: 并发消息返回节点答复或 BUSY
    Given team agents "alice" and "bob" are serving
    When both team agents send greetings simultaneously
    Then both team agents finish their reasoning without blocking

  Scenario: 节点重启后重新注册并继续此前对话
    Given team agents "alice" and "bob" are serving
    When running agent "alice" sends "first hello" to "bob"
    Then the CLI answer is "bob: first hello"
    When I stop team agent "bob"
    And I restart team agent "bob"
    And running agent "alice" sends "hello after restart" to "bob"
    Then the CLI answer is "bob: hello after restart"
    When I stop the team agents
    Then the CLI agent "bob" has this conversation on its tape
      | role      | text                     |
      | user      | first hello              |
      | assistant | bob: first hello         |
      | user      | hello after restart      |
      | assistant | bob: hello after restart |

  Scenario: 正常停止的节点保留身份并标为离线
    Given team agents "alice" and "bob" are serving
    When I stop team agent "bob"
    And running agent "alice" attempts to greet "bob"
    Then the CLI answer is "Unable to consult peer: peer bob is offline"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                                      |
      | user      | ask bob: hello                            |
      | assistant | Unable to consult peer: peer bob is offline |

  Scenario: 目录重启后运行中的节点自动重新注册
    Given team agents "alice" and "bob" are serving
    When I restart the team directory at the same address
    Then both team agents eventually register again
    When running agent "alice" sends "hello after directory restart" to "bob"
    Then the CLI answer is "bob: hello after directory restart"

  Scenario: 发信角色必须是已注册且正在运行的节点
    When running agent "alice" attempts to greet "bob"
    Then the CLI command fails with "peer is not registered"
    And no CLI tape directory is created

  Scenario: 新进程无法抢占运行中节点的名称
    Given team agents "alice" and "bob" are serving
    When another server attempts to register as "alice"
    Then the CLI command fails with "agent name is already registered"
    When running agent "bob" sends "still alice" to "alice"
    Then the CLI answer is "alice: still alice"

  Scenario: 崩溃节点保留身份且在线状态变为未知
    Given team agents "alice" and "bob" are serving
    When team agent "bob" crashes
    Then its presence eventually becomes unknown
    And team agent "alice" is still registered
    When running agent "alice" attempts to greet "bob"
    Then the CLI answer is "Unable to consult peer: peer bob is unknown"

  Scenario: 管理员可通过 CLI 移除已停止的节点
    Given team agents "alice" and "bob" are serving
    When I stop team agent "bob"
    And the operator forgets team agent "bob"
    Then team agent "bob" is forgotten
    And team agent "alice" is still registered

  Scenario: 推理处于 BUSY 时仍按时续报心跳
    Given team agents "alice" and "bob" are serving
    And Alice is interrupted while reasoning after a published answer
    Then the busy agent keeps online presence across a heartbeat window
    When I try to ask running agent "alice" to "another callback"
    Then the CLI command fails with "busy: request was not accepted"
