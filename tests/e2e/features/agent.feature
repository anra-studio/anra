Feature: An agent keeps its conversation on its own tape
  People can continue a conversation after restarting Anra without losing
  earlier answers or importing another member's private history.

  Scenario: Continue a conversation after closing and reopening storage
    Given an agent with its own tape
    When I ask "first question" and the model answers "first answer"
    And I close and reopen the agent's storage
    And I ask "second question" and the model answers "second answer"
    Then the model sees the conversation in order
      | role      | text            |
      | user      | first question  |
      | assistant | first answer    |
      | user      | second question |
    And the answer is "second answer"

  Scenario: Members sharing a database keep separate conversations
    Given an agent with its own tape
    When I ask "my question" and the model answers "my answer"
    And another member uses the same database for a private conversation
    And I ask "my follow-up" and the model answers "my next answer"
    Then the model sees the conversation in order
      | role      | text         |
      | user      | my question  |
      | assistant | my answer    |
      | user      | my follow-up |
