Feature: 从终端运行本地角色
  CLI 可以显示命令用法、执行推理并持久化本地对话。

  Scenario: 未配置角色时也能查看命令用法
    When I request CLI help
    Then the CLI help lists "serve"
    And no CLI tape directory is created

  Scenario: 本地命令跨进程继续同一段对话
    When I run the CLI agent "alice" with "hello" and flags before the command
    Then the CLI answer is "alice: hello"
    When I run the CLI agent "alice" with "hello again" and flags after the message
    Then the CLI answer is "alice: hello again"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text               |
      | user      | hello              |
      | assistant | alice: hello       |
      | user      | hello again        |
      | assistant | alice: hello again |

  Scenario: 缺少消息时不打开 Tape
    When I run a local agent without a message
    Then the CLI command fails with "non-empty message"
    And no CLI tape directory is created
