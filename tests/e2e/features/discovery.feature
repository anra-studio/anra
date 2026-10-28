Feature: Identified Peers retain metadata and communicate by name
  Alice and Bob both serve their own tape-backed runtimes. They discover one
  another through the team's directory and exchange messages directly.

  Background:
    Given a running team Peer directory

  Scenario: Both servers discover each other and exchange greetings in both directions
    Given team agents "alice" and "bob" are serving
    When I read the team directory
    Then the team directory lists "alice" and "bob"
    When running agent "alice" sends "hello from alice" to "bob"
    Then the CLI answer is "bob: hello from alice"
    When running agent "bob" sends "hello from bob" to "alice"
    Then the CLI answer is "alice: hello from bob"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                      |
      | user      | ask bob: hello from alice |
      | assistant | bob: hello from alice     |
      | user      | hello from bob            |
      | assistant | alice: hello from bob     |
    And the CLI agent "bob" has this conversation on its tape
      | role      | text                      |
      | user      | hello from alice          |
      | assistant | bob: hello from alice     |
      | user      | ask alice: hello from bob |
      | assistant | alice: hello from bob     |

  Scenario: Concurrent greetings finish with peer findings or BUSY
    Given team agents "alice" and "bob" are serving
    When both team agents send greetings simultaneously
    Then both team agents finish their reasoning without blocking

  Scenario: A restarted server registers its new address and retains its tape
    Given team agents "alice" and "bob" are serving
    When running agent "alice" sends "first hello" to "bob"
    Then the CLI answer is "bob: first hello"
    When I stop team agent "bob"
    And I restart team agent "bob"
    And running agent "alice" sends "hello after restart" to "bob"
    Then the CLI answer is "bob: hello after restart"
    When I stop the team agents
    Then the CLI agent "bob" has this conversation on its tape
      | role      | text                     |
      | user      | first hello              |
      | assistant | bob: first hello         |
      | user      | hello after restart      |
      | assistant | bob: hello after restart |

  Scenario: A stopped Peer remains known and offline
    Given team agents "alice" and "bob" are serving
    When I stop team agent "bob"
    And running agent "alice" attempts to greet "bob"
    Then the CLI answer is "Unable to consult peer: peer bob is offline"
    When I stop the team agents
    Then the CLI agent "alice" has this conversation on its tape
      | role      | text                                      |
      | user      | ask bob: hello                            |
      | assistant | Unable to consult peer: peer bob is offline |

  Scenario: Running agents register again after the directory restarts
    Given team agents "alice" and "bob" are serving
    When I restart the team directory at the same address
    Then both team agents eventually register again
    When running agent "alice" sends "hello after directory restart" to "bob"
    Then the CLI answer is "bob: hello after directory restart"

  Scenario: A CLI sender must itself be a running registered server
    When running agent "alice" attempts to greet "bob"
    Then the CLI command fails with "peer is not registered"
    And no CLI tape directory is created

  Scenario: A running agent name cannot be taken by another server
    Given team agents "alice" and "bob" are serving
    When another server attempts to register as "alice"
    Then the CLI command fails with "agent name is already registered"
    When running agent "bob" sends "still alice" to "alice"
    Then the CLI answer is "alice: still alice"

  Scenario: A crashed Peer remains known with unknown presence
    Given team agents "alice" and "bob" are serving
    When team agent "bob" crashes
    Then its presence eventually becomes unknown
    And team agent "alice" is still registered
    When running agent "alice" attempts to greet "bob"
    Then the CLI answer is "Unable to consult peer: peer bob is unknown"

  Scenario: Trusted agents register and communicate without a shared credential
    Given team agents "alice" and "bob" are serving
    When I read the team directory
    Then the team directory lists "alice" and "bob"
    And the team cards require no bearer credential
    When running agent "alice" sends "hello without a token" to "bob"
    Then the CLI answer is "bob: hello without a token"

  Scenario: A different runtime cannot heartbeat or disconnect a Peer
    Given team agents "alice" and "bob" are serving
    When another runtime attempts to heartbeat or disconnect agent "alice"
    And I read the team directory
    Then the team directory lists "alice" and "bob"

  Scenario: The operator explicitly retires a stopped Peer through the CLI
    Given team agents "alice" and "bob" are serving
    When I stop team agent "bob"
    And the operator forgets team agent "bob"
    Then team agent "bob" is forgotten
    And team agent "alice" is still registered

  Scenario: Reasoning BUSY does not interrupt heartbeat maintenance
    Given team agents "alice" and "bob" are serving
    And Alice is interrupted while reasoning after a published answer
    Then the busy agent keeps online presence across a heartbeat window
    When I try to ask running agent "alice" to "another callback"
    Then the CLI command fails with "busy: request was not accepted"
