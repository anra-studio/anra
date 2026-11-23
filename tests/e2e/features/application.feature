Feature: 通过公共 Go API 托管角色
  应用宿主和 CLI 共用 Tape 与节点发现。

  Scenario: 应用宿主和 CLI 共用持久化对话
    Given an application host with configured agents
    When the application opens an agent with an invalid identity
    Then the application rejects the identity
    And no CLI tape directory is created
    When the application runs agent "alice" with "hello from the host"
    Then the application answer is "alice: hello from the host"
    When I run the configured CLI agent "alice" with "hello from the CLI"
    Then the CLI answer is "alice: hello from the CLI"
    When the application runs agent "alice" with "hello again"
    Then the application answer is "alice: hello again"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text                       |
      | user      | hello from the host        |
      | assistant | alice: hello from the host |
      | user      | hello from the CLI         |
      | assistant | alice: hello from the CLI  |
      | user      | hello again                |
      | assistant | alice: hello again         |

  Scenario: 宿主拒绝就绪后可重新服务，并在重启后继续节点通信
    Given an application host with configured agents
    When the application rejects agent "alice" at readiness
    Then the application directory retains offline agents
    When the application serves agents "alice" and "bob"
    And the application updates Alice's public metadata
    Then Alice's published card matches the committed Peer
    When the application asks agent "alice" with "ask bob: hello from the host"
    Then the application answer is "bob: hello from the host"
    When I ask configured agent "bob" for its role
    Then the CLI answer is "bob"
    When the application stops its peers
    Then the application directory retains offline agents
    When the application serves agents "alice" and "bob"
    And the application asks agent "alice" with "ask bob: hello again"
    Then the application answer is "bob: hello again"
    When the application stops its peers
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                         |
      | user      | ask bob: hello from the host |
      | assistant | bob: hello from the host     |
      | user      | ask bob: hello again         |
      | assistant | bob: hello again             |
