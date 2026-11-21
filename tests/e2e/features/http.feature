Feature: 通过 REST 和 Connect 访问目录与视图
  两种传输共享应用的目录服务与借阅授权记录。

  Scenario: REST 拒绝无效及超限注册并与 Connect 共享持久化目录
    Given an application host with configured agents
    Then the registry rejects invalid REST input "missing runtime"
    And the registry rejects invalid REST input "oversized body"
    And REST and Connect share the registry lifecycle

  Scenario: REST 可借阅已提交视图，Connect 可查询并读取同一授权
    Given an application host with configured agents
    And the application serves agents "alice" and "bob"
    When the application asks agent "alice" with "hello"
    Then the application answer is "alice: hello"
    When Bob borrows Alice's committed TapeView through REST
    Then TapeView REST enforces identity, extension negotiation, and requester-bound grants
    When Bob releases his REST grant and both transports deny further reads
    And the application stops its peers
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text         |
      | user      | hello        |
      | assistant | alice: hello |
