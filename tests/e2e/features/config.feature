Feature: Manage named CLI agents in a TOML file
  Every agent setting comes from the file named by --config. --agent selects an
  [agents.<name>] table and the file's [lobby] address is the only way agents
  find each other. Settings are read at startup, so a change needs a restart.

  Scenario: Register and discover agents using the lobby and endpoint in the example config
    Given a running team Agent Card directory
    And a lobby TOML configuration based on the repository example
    And the configured CLI agent "alice" is serving
    And the configured CLI agent "bob" is serving
    When I read the team directory
    Then the team directory lists "alice" and "bob"
    When I ask configured agent "alice" for its role
    Then the CLI answer is "e2e-bot:alice is a e2e example bot"

  Scenario: A running server keeps its settings until it restarts
    Given a running team Agent Card directory
    And a lobby TOML configuration based on the repository example
    And the configured CLI agent "alice" is serving
    When I change the role of configured agent "alice" to "Answer briefly."
    And I ask configured agent "alice" for its role
    Then the CLI answer is "e2e-bot:alice is a e2e example bot"
    When I stop the team agents
    And the configured CLI agent "alice" is serving
    And I ask configured agent "alice" for its role
    Then the CLI answer is "Answer briefly.:alice is a e2e example bot"

  Scenario: Select named agents from TOML
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

  Scenario: Serve and send using configured agents
    Given a running team Agent Card directory
    And a TOML CLI configuration for the team directory
    And the configured CLI agent "alice" is serving
    And the configured CLI agent "bob" is serving
    When the configured CLI agent "alice" sends "hello from alice" to "bob"
    Then the CLI answer is "bob: hello from alice"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                      |
      | user      | ask bob: hello from alice |
      | assistant | bob: hello from alice     |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text                  |
      | user      | hello from alice      |
      | assistant | bob: hello from alice |

  Scenario Outline: Every agent command requires --config
    When I run the "<command>" command without --config
    Then the CLI command fails with "--config is required"
    And no CLI tape directory is created

    Examples:
      | command |
      | run     |
      | ask     |
      | serve   |

  Scenario Outline: Agent settings cannot be set with flags
    Given a TOML CLI configuration with Alice and Bob
    When I pass the removed setting flag "<flag>" to a configured agent
    Then the CLI command fails with "unknown flag: <flag>"

    Examples:
      | flag       |
      | --provider |
      | --model    |
      | --data-dir |
      | --session  |
      | --system   |
      | --listen   |
      | --registry |
      | --timeout  |

  Scenario Outline: Required agent settings have no fallback
    Given a TOML CLI configuration with Alice and Bob
    When I run the "<command>" command with "<key>" <problem> for agent "alice"
    Then the CLI command fails with "<key> is required"
    And no CLI tape directory is created

    Examples:
      | command | key      | problem |
      | run     | data-dir | missing |
      | run     | provider | missing |
      | run     | timeout  | missing |
      | run     | endpoint | missing |
      | ask     | data-dir | missing |
      | ask     | provider | missing |
      | ask     | timeout  | missing |
      | ask     | endpoint | missing |
      | serve   | data-dir | missing |
      | serve   | provider | missing |
      | serve   | timeout  | missing |
      | serve   | endpoint | missing |
      | run     | data-dir | empty   |
      | run     | provider | empty   |
      | run     | timeout  | empty   |
      | run     | endpoint | empty   |
      | run     | data-dir | blank   |
      | run     | provider | blank   |
      | run     | timeout  | blank   |
      | run     | endpoint | blank   |

  Scenario: Agent settings cannot be set with environment variables
    Given a TOML CLI configuration with Alice and Bob
    And the environment tries to override every agent setting
    When I run the configured CLI agent "alice" with "hello"
    Then the CLI answer is "alice: hello"
    And the CLI agent "alice" has this conversation on its tape
      | role      | text         |
      | user      | hello        |
      | assistant | alice: hello |

  Scenario Outline: Reject invalid config files
    When I run the CLI with TOML configuration problem "<problem>"
    Then the CLI command fails with "<error>"
    And no CLI tape directory is created

    Examples:
      | problem                  | error              |
      | missing file             | read config        |
      | malformed TOML           | parse config       |
      | wrong setting type       | parse config       |
      | wrong lobby address type | parse config       |
      | unknown lobby setting    | parse config       |
      | wrong role type          | parse config       |
      | wrong desc type          | parse config       |
      | wrong endpoint type      | parse config       |
      | unknown setting          | parse config       |
      | agent peer setting       | parse config       |
      | agent session setting    | parse config       |
      | agent registry setting   | parse config       |
      | defaults section         | parse config       |
      | default agent setting    | parse config       |
      | missing lobby            | must set [lobby]   |
      | invalid lobby address    | HTTP(S) base URL   |
      | no agents                | at least one agent |
      | unknown selected agent   | is not configured  |
      | invalid agent name       | invalid agent name |
      | invalid duration         | timeout            |
      | zero timeout             | must be positive   |
      | unknown provider         | unknown provider   |
