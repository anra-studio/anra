Feature: Reuse Anra outside the command line
  An application host can load configuration, run local agents, and serve peers
  through public Go packages. It shares the CLI's storage and discovery contract.

  Scenario: A local application and the CLI share the same durable conversation
    Given an application host with configured agents
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

  Scenario: An application serves discoverable peers and restarts them
    Given an application host with configured agents
    And the application serves agents "alice" and "bob"
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

  Scenario: An application can reject readiness without leaving online presence or open storage
    Given an application host with configured agents
    When the application rejects agent "alice" at readiness
    Then the application directory retains offline agents
    When the application serves agents "alice" and "bob"
    And the application asks agent "alice" with "hello"
    Then the application answer is "alice: hello"

  Scenario: Direct application settings validate identity before opening storage
    Given an application host with configured agents
    When the application opens an agent with an invalid identity
    Then the application rejects the identity
    And no CLI tape directory is created

  Scenario: Owned metadata updates refresh the published A2A representation
    Given an application host with configured agents
    And the application serves agents "alice" and "bob"
    When the application updates Alice's public metadata
    Then Alice's published card matches the committed Peer
    When the application asks agent "alice" with "hello"
    Then the application answer is "alice: hello"
