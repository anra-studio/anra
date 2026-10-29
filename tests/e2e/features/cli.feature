Feature: Run tape-backed agents from the terminal
  People can use Anra locally and send messages between independently stored
  agents. Agent settings come from the --config file; flags only select an
  agent and a destination.

  Scenario Outline: Discover command usage without configuring an agent
    When I request CLI help for "<command>"
    Then the CLI help lists "<item>"
    And no CLI tape directory is created

    Examples:
      | command | item     |
      | root    | serve    |
      | run     | --config |
      | serve   | --agent  |
      | ask     | --agent  |

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
    Given a running team Peer directory
    And team agents "alice" and "bob" are serving
    When running agent "alice" sends "hello from alice" to "bob"
    Then the CLI answer is "bob: hello from alice"
    When I stop team agent "bob"
    And I restart team agent "bob"
    And running agent "alice" sends "hello again" to "bob"
    Then the CLI answer is "bob: hello again"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                      |
      | user      | ask bob: hello from alice |
      | assistant | bob: hello from alice     |
      | user      | ask bob: hello again      |
      | assistant | bob: hello again          |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text                  |
      | user      | hello from alice      |
      | assistant | bob: hello from alice |
      | user      | hello again           |
      | assistant | bob: hello again      |

  Scenario Outline: Peer messages require a valid caller identity
    Given a running team Peer directory
    And the CLI agent "bob" is serving with a deterministic model
    When a peer sends a greeting with identity "<identity>"
    Then the CLI command fails
    When I stop the serving CLI agent
    Then the CLI agent "bob" has an empty tape

    Examples:
      | identity |
      | missing  |
      | bad/name |

  Scenario Outline: Invalid CLI configuration fails before opening storage
    When I run the CLI with invalid configuration "<problem>"
    Then the CLI command fails with "<error>"
    And no CLI tape directory is created

    Examples:
      | problem                  | error                |
      | missing message          | non-empty message    |
      | invalid agent            | --agent must start   |
      | missing key              | OPENAI_API_KEY       |
      | removed send             | unknown command      |
      | peer flag                | unknown flag: --peer |
      | removed destination flag | unknown flag: --to   |
      | unknown command          | unknown command      |
