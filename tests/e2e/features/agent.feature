Feature: 共享数据库中的角色隔离
  重开 Tape 后，角色只用自己的历史继续对话。

  Scenario: 共享数据库的角色在重启后仍只能看到自己的对话
    Given an agent with its own tape
    When I ask "my question" and the model answers "my answer"
    And another member uses the same database for a private conversation
    And I close and reopen the agent's storage
    And I ask "my follow-up" and the model answers "my next answer"
    Then the model sees the conversation in order
      | role      | text         |
      | user      | my question  |
      | assistant | my answer    |
      | user      | my follow-up |
    And the answer is "my next answer"
