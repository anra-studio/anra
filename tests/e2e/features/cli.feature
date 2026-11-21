Feature: 从终端运行本地角色
  CLI 执行推理并跨进程持久化本地对话。

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
