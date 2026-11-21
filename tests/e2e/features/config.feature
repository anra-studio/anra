Feature: 从 TOML 选择和加载角色配置
  CLI 从 --config 指定的文件读取设置，通过 --agent 选择角色。
  运行中的服务在重启后才采用更新的配置。

  Scenario: 示例配置指定注册位置，角色设置在重启后生效
    Given a running team Peer directory
    And a lobby TOML configuration based on the repository example
    And the configured CLI agent "alice" is serving
    And the configured CLI agent "bob" is serving
    When I read the team directory
    Then the team directory lists "alice" and "bob"
    When I change the role of configured agent "alice" to "Answer briefly."
    And I ask configured agent "alice" for its role
    Then the CLI answer is "e2e-bot:alice is a e2e example bot"
    When I stop the team agents
    And the configured CLI agent "alice" is serving
    And I ask configured agent "alice" for its role
    Then the CLI answer is "Answer briefly.:alice is a e2e example bot"

  Scenario: 同一份配置可选择不同角色并分别保存对话
    Given a TOML CLI configuration with Alice and Bob
    When I run the configured CLI agent "alice" with "hello alice"
    Then the CLI answer is "alice: hello alice"
    When I run the configured CLI agent "bob" with "hello bob"
    Then the CLI answer is "bob: hello bob"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text               |
      | user      | hello alice        |
      | assistant | alice: hello alice |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text           |
      | user      | hello bob      |
      | assistant | bob: hello bob |

  Scenario: 缺少配置文件参数时不打开 Tape
    When I run a local agent without --config
    Then the CLI command fails with "--config is required"
    And no CLI tape directory is created

  Scenario: 缺少必填存储路径时不打开 Tape
    Given a TOML CLI configuration with Alice and Bob
    When I serve an agent whose data-dir is missing
    Then the CLI command fails with "data-dir is required"
    And no CLI tape directory is created

  Scenario: 环境变量不能覆盖 TOML 中的角色设置
    Given a TOML CLI configuration with Alice and Bob
    And the environment tries to override every agent setting
    When I run the configured CLI agent "alice" with "hello"
    Then the CLI answer is "alice: hello"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text         |
      | user      | hello        |
      | assistant | alice: hello |

  Scenario Outline: 缺失或包含未知字段的 TOML 配置在写入前失败
    When I run the CLI with TOML configuration problem "<problem>"
    Then the CLI command fails with "<error>"
    And no CLI tape directory is created

    Examples:
      | problem       | error        |
      | missing file  | read config  |
      | unknown field | parse config |
