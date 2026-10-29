Feature: Keep identified Peers independently of runtime presence
  A durable registry preserves each agent's address and skills while observing
  its current runtime. Discovery never substitutes another agent implicitly.

  Background:
    Given a durable Peer registry with a controlled clock
    And Alice has registered her research skills

  Scenario: Missing contact changes presence without erasing identity
    Then Alice is online with her registered metadata
    When registry contact becomes stale
    Then Alice is unknown with her registered metadata
    When Alice sends a heartbeat
    Then the registry rejects the operation with "failed_precondition"
    When Alice re-establishes her presence
    Then Alice is online with her registered metadata

  Scenario: Graceful disconnect retains a Peer and closes that runtime
    When Alice disconnects twice
    Then Alice is offline with her registered metadata
    When the durable registry restarts
    Then Alice is unknown with her registered metadata
    When Alice re-establishes her presence
    Then the registry rejects the operation with "failed_precondition"
    When a new Alice runtime registers a new address
    Then Alice is online with her registered metadata

  Scenario: Registry restart invalidates observations and old conditional writes
    When the durable registry restarts
    Then Alice is unknown with her registered metadata
    And Alice has no fabricated last-contact time
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
    When Alice re-establishes her presence
    Then Alice is online with her registered metadata

  Scenario Outline: Other processes cannot mutate the current runtime
    When an unrelated runtime attempts "<operation>"
    Then the registry rejects the operation with "<code>"
    And Alice is online with her registered metadata

    Examples:
      | operation  | code              |
      | register   | already_exists    |
      | heartbeat  | permission_denied |
      | disconnect | permission_denied |
      | wrong key  | permission_denied |

  Scenario Outline: Replaced runtimes cannot return after the replacement loses contact
    When registry contact becomes stale
    And a new Alice runtime registers a new address
    And the replacement becomes "<state>"
    Then the old runtime cannot register, heartbeat or disconnect
    And Alice is <state> with her registered metadata

    Examples:
      | state   |
      | unknown |
      | offline |

  Scenario: Metadata updates are conditional and retries cannot undo newer changes
    When Alice updates her description and skills
    Then her accepted registration retry preserves its revision
    And her Agent Card represents the updated metadata
    When Alice retries her original registration
    Then the registry rejects the operation with "aborted"
    And Alice is online with her registered metadata

  Scenario: Concurrent metadata writers cannot silently overwrite each other
    When two owned updates use the same revision concurrently
    Then exactly one update commits and the other reports a conflict
    And Alice is online with her registered metadata

  Scenario: Filtering returns distinct identities and presence states
    When Bob is offline and Charlie is online with a different skill
    And registry contact becomes stale
    And Charlie re-establishes presence
    Then listing preserves every Peer and filters by state and skill
    And discovery responses contain no ownership credentials

  Scenario Outline: Invalid metadata never changes a registered Peer
    When Alice submits metadata with "<problem>"
    Then the registry rejects the operation with "invalid_argument"
    And Alice is online with her registered metadata

    Examples:
      | problem         |
      | invalid ID      |
      | relative address|
      | wildcard address|
      | zero port       |
      | credentials     |
      | address path    |
      | address query   |
      | address fragment|
      | duplicate skill |
      | empty skill ID  |
      | blank skill name|

  Scenario: Oversized registrations are rejected by the HTTP boundary
    When Alice sends metadata exceeding the registry message limit
    Then the registry rejects the operation with "resource_exhausted"
    And Alice is online with her registered metadata

  Scenario: Forget is explicit retirement and survives registry restart
    When the operator attempts to forget online Alice
    Then the registry rejects the operation with "failed_precondition"
    When Alice disconnects twice
    And the operator forgets Alice twice
    Then Alice is absent from lookup and listing
    When the durable registry restarts
    And the operator retries the same retirement
    Then Alice is absent from lookup and listing
    When Alice retries her original registration
    Then the registry rejects the operation with "failed_precondition"

  Scenario: Stale operator revisions cannot remove updated identities
    When the operator reads Alice before a metadata update
    And Alice updates her description and skills
    And Alice disconnects twice
    And the operator attempts the earlier retirement
    Then the registry rejects the operation with "aborted"
    And Alice is offline with her registered metadata

  Scenario: Failed startup cleans up even when its successful registration reply is lost
    When a startup registration acknowledgement is lost
    Then the registry rejects the operation with "unavailable"
    And the failed startup leaves Bob known and offline
    And Alice is online with her registered metadata

  Scenario: Lost update acknowledgements reconcile without replaying old metadata
    When a runtime loses a metadata-update acknowledgement
    Then the registry rejects the operation with "unavailable"
    And heartbeats reconcile the committed metadata without rolling it back
