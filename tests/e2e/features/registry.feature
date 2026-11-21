Feature: 按身份持久化节点元数据
  目录保留节点地址和能力，独立跟踪运行实例的在线状态。

  Background:
    Given a durable Peer registry with a controlled clock
    And Alice has registered her research skills

  Scenario: 联络超时改变在线状态并保留节点身份
    Then Alice is online with her registered metadata
    When registry contact becomes stale
    Then Alice is unknown with her registered metadata
    When Alice sends a heartbeat
    Then the registry rejects the operation with "failed_precondition"
    When Alice re-establishes her presence
    Then Alice is online with her registered metadata

  Scenario: 正常断开关闭原实例并保留节点身份
    When Alice disconnects twice
    Then Alice is offline with her registered metadata
    When the durable registry restarts
    Then Alice is unknown with her registered metadata
    When Alice re-establishes her presence
    Then the registry rejects the operation with "failed_precondition"
    When a new Alice runtime registers a new address
    Then Alice is online with her registered metadata

  Scenario: 目录重启使旧在线状态和条件写入失效
    When the durable registry restarts
    Then Alice is unknown with her registered metadata
    And Alice has no fabricated last-contact time
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
    When Alice re-establishes her presence
    Then Alice is online with her registered metadata

  Scenario Outline: 其他进程无法修改当前实例的状态
    When an unrelated runtime attempts "<operation>"
    Then the registry rejects the operation with "<code>"
    And Alice is online with her registered metadata

    Examples:
      | operation  | code              |
      | register   | already_exists    |
      | heartbeat  | permission_denied |
      | disconnect | permission_denied |
      | wrong key  | permission_denied |

  Scenario Outline: 接替实例失联或下线后旧实例仍无法恢复注册
    When registry contact becomes stale
    And a new Alice runtime registers a new address
    And the replacement becomes "<state>"
    Then the old runtime cannot register, heartbeat or disconnect
    And Alice is <state> with her registered metadata

    Examples:
      | state   |
      | unknown |
      | offline |

  Scenario: 元数据更新检查修订号，旧请求重试无法覆盖新版本
    When Alice updates her description and skills
    Then her accepted registration retry preserves its revision
    And her Agent Card represents the updated metadata
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
    And Alice is online with her registered metadata

  Scenario: 同一修订号的两次并发更新只能提交一次
    When two owned updates use the same revision concurrently
    Then exactly one update commits and the other reports a conflict
    And Alice is online with her registered metadata

  Scenario: 按在线状态和能力筛选节点且不泄露所有权凭证
    When Bob is offline and Charlie is online with a different skill
    And registry contact becomes stale
    And Charlie re-establishes presence
    Then listing preserves every Peer and filters by state and skill
    And discovery responses contain no ownership credentials

  Scenario Outline: 无效地址或重复能力不会改变已注册的节点
    When Alice submits metadata with "<problem>"
    Then the registry rejects the operation with "invalid_argument"
    And Alice is online with her registered metadata

    Examples:
      | problem          |
      | wildcard address |
      | duplicate skill  |

  Scenario: HTTP 拒绝超出大小限制的注册请求
    When Alice sends metadata exceeding the registry message limit
    Then the registry rejects the operation with "resource_exhausted"
    And Alice is online with her registered metadata

  Scenario: 显式移除已停止节点后，目录重启也不会恢复该节点
    When the operator attempts to forget online Alice
    Then the registry rejects the operation with "failed_precondition"
    When Alice disconnects twice
    And the operator forgets Alice twice
    Then Alice is absent from lookup and listing
    When the durable registry restarts
    And the operator retries the same retirement
    Then Alice is absent from lookup and listing
    When Alice retries her original registration
    Then the registry rejects the operation with "failed_precondition"

  Scenario: 旧修订号无法移除已更新的节点
    When the operator reads Alice before a metadata update
    And Alice updates her description and skills
    And Alice disconnects twice
    And the operator attempts the earlier retirement
    Then the registry rejects the operation with "aborted"
    And Alice is offline with her registered metadata

  Scenario: 注册成功回执丢失时，启动失败的节点退回离线
    When a startup registration acknowledgement is lost
    Then the registry rejects the operation with "unavailable"
    And the failed startup leaves Bob known and offline
    And Alice is online with her registered metadata

  Scenario: 心跳在更新回执丢失后确认新元数据，不重放旧值
    When a runtime loses a metadata-update acknowledgement
    Then the registry rejects the operation with "unavailable"
    And heartbeats reconcile the committed metadata without rolling it back
