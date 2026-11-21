Feature: 按身份持久化节点元数据
  目录保留节点地址和能力，独立跟踪运行实例的在线状态。

  Background:
    Given a durable Peer registry with a controlled clock
    And Alice has registered her research skills

  Scenario: 联络超时和目录重启使旧在线状态及条件写入失效
    Then Alice is online with her registered metadata
    When registry contact becomes stale
    Then Alice is unknown with her registered metadata
    When Alice sends a heartbeat
    Then the registry rejects the operation with "failed_precondition"
    When Alice re-establishes her presence
    Then Alice is online with her registered metadata
    When the durable registry restarts
    Then Alice is unknown with her registered metadata
    And Alice has no fabricated last-contact time
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
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
    When registry contact becomes stale
    Then the old runtime cannot register, heartbeat or disconnect
    And Alice is unknown with her registered metadata

  Scenario: 无关进程和错误凭证不能变更当前实例
    When an unrelated runtime attempts "heartbeat"
    Then the registry rejects the operation with "permission_denied"
    When an unrelated runtime attempts "wrong key"
    Then the registry rejects the operation with "permission_denied"
    And Alice is online with her registered metadata

  Scenario: 元数据更新检查修订号，旧请求重试无法覆盖新版本
    When Alice updates her description and skills
    Then her accepted registration retry preserves its revision
    And her Agent Card represents the updated metadata
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
    And Alice is online with her registered metadata
    When Alice submits metadata with "duplicate skill"
    Then the registry rejects the operation with "invalid_argument"
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

  Scenario: HTTP 拒绝超出大小限制的注册请求
    When Alice sends metadata exceeding the registry message limit
    Then the registry rejects the operation with "resource_exhausted"
    And Alice is online with her registered metadata

  Scenario: 显式移除已停止节点后，目录重启也不会恢复该节点
    When the operator attempts to forget online Alice
    Then the registry rejects the operation with "failed_precondition"
    When the operator reads Alice before a metadata update
    And Alice updates her description and skills
    And Alice disconnects twice
    And the operator attempts the earlier retirement
    Then the registry rejects the operation with "aborted"
    And Alice is offline with her registered metadata
    When the operator forgets Alice twice
    Then Alice is absent from lookup and listing
    When the durable registry restarts
    And the operator retries the same retirement
    Then Alice is absent from lookup and listing
    When Alice retries her original registration
    Then the registry rejects the operation with "failed_precondition"
