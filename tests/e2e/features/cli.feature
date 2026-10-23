Feature: Run tape-backed agents from the terminal
  People can use Anra locally and send messages between independently stored
  agents without requiring model credentials for a hello demonstration.

  Scenario Outline: Discover command usage without configuring an agent
    When I request CLI help for "<command>"
    Then the CLI help lists "<item>"
    And no CLI tape directory is created

    Examples:
      | command | item      |
      | root    | serve     |
      | run     | --runner  |
      | serve   | --listen  |
      | send    | --peer    |

  Scenario Outline: Place shared flags before the command or after the message
    When I run the CLI agent "alice" with "hello" and flags <placement>
    Then the CLI answer is "alice: hello"

    Examples:
      | placement          |
      | before the command |
      | after the message  |

  Scenario: Run a local agent and retain its conversation across commands
    When I run the local CLI agent "alice" with "hello"
    Then the CLI answer is "alice: hello"
    When I run the local CLI agent "alice" with "hello again"
    Then the CLI answer is "alice: hello again"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text               |
      | user      | hello              |
      | assistant | alice: hello       |
      | user      | hello again        |
      | assistant | alice: hello again |

  Scenario: Say hello from one agent to another and restart the receiving agent
    Given the CLI agent "bob" is serving with the echo runner
    When the CLI agent "alice" sends "hello from alice" to the serving agent
    Then the CLI answer is "bob: hello from alice"
    When I restart the serving CLI agent
    And the CLI agent "alice" sends "hello again" to the serving agent
    Then the CLI answer is "bob: hello again"
    When I stop the serving CLI agent
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                  |
      | user      | hello from alice      |
      | assistant | bob: hello from alice |
      | user      | hello again           |
      | assistant | bob: hello again      |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text                  |
      | user      | hello from alice      |
      | assistant | bob: hello from alice |
      | user      | hello again           |
      | assistant | bob: hello again      |

  Scenario: An incorrect peer token cannot send a message
    Given the CLI agent "bob" is serving with the echo runner
    When the CLI agent sends a greeting with the wrong peer token
    Then the CLI command fails
    When I stop the serving CLI agent
    Then the CLI agent "bob" has an empty tape

  Scenario Outline: Invalid CLI configuration fails before opening storage
    When I run the CLI with invalid configuration "<problem>"
    Then the CLI command fails with "<error>"
    And no CLI tape directory is created

    Examples:
      | problem         | error                  |
      | missing message | non-empty message      |
      | invalid agent   | --agent must start     |
      | missing key     | DEEPSEEK_API_KEY        |
      | missing token   | ANRA_PEER_TOKEN         |
      | missing peer    | required flag(s)        |
      | unknown runner  | unknown runner         |
      | unknown command | unknown command        |
