Feature: Expose RPC services through schema-defined HTTP routes
  Vanguard serves REST and Connect from the same validated service implementations,
  using the application's HTTP lifecycle, temporary storage, and grant journal.

  Scenario: REST and Connect observe the same durable registry
    Given an application host with configured agents
    Then REST and Connect share the registry lifecycle

  Scenario Outline: REST preserves validation and request size limits
    Given an application host with configured agents
    Then the registry rejects invalid REST input "<problem>"

    Examples:
      | problem            |
      | missing runtime    |
      | wrong JSON type    |
      | unknown JSON field |
      | unknown query      |
      | invalid enum       |
      | oversized body     |
      | wrong method       |

  Scenario: REST borrows committed context with the same permissions and lifecycle
    Given an application host with configured agents
    And the application serves agents "alice" and "bob"
    When the application asks agent "alice" with "hello"
    Then the application answer is "alice: hello"
    When Bob borrows Alice's committed TapeView through REST
    Then TapeView REST enforces identity, extension negotiation, and requester-bound grants
    When Bob releases his REST grant and both transports deny further reads
    And the application stops its peers
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text         |
      | user      | hello        |
      | assistant | alice: hello |
