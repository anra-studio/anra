Feature: Read-only MCP handoff context
  External runtimes read only the role revision and persisted window selected by
  a committed handoff. Connection credentials carry the complete authorization.

  Scenario: Official MCP client discovers and invokes both read-only tools
    Given a "normal" MCP handoff
    When an official MCP client connects with the handoff credential
    Then it discovers exactly the two read-only context tools
    And both MCP tools return the committed role and context

  Scenario: Role revisions and context survive growth and restart unchanged
    Given a "normal" MCP handoff
    When I remember both MCP tool results
    And the role changes and its tape grows
    And the MCP storage and server restart
    Then both MCP tool results are byte-for-byte unchanged

  Scenario: MCP reads do not acquire or disturb reasoning admission
    Given a "normal" MCP handoff
    When I remember both MCP tool results
    And the role starts a blocked reasoning turn
    Then both MCP tool results are byte-for-byte unchanged
    And the active reasoning turn can finish normally

  Scenario: An explicit persisted prefix of an active turn can be handed off
    Given a "normal" MCP handoff
    When the role starts a blocked reasoning turn
    And a handoff commits the persisted active-turn prefix
    Then both MCP tools return the committed role and context
    And the active reasoning turn can finish normally
    And both MCP tools return the committed role and context

  Scenario Outline: Connection identity cannot be inferred or replayed
    Given a "normal" MCP handoff
    When a caller attempts MCP access using "<attack>"
    Then MCP rejects access with "permission_denied" and no private metadata
    Examples:
      | attack                          |
      | no credential                   |
      | anchor as credential            |
      | invented credential             |
      | another role's endpoint         |
      | another binding's anchor        |
      | nonexistent anchor              |
      | spoofed identity headers        |
      | replayed MCP session identifier |

  Scenario Outline: Credentials expire and can be revoked durably
    Given a "normal" MCP handoff
    When the MCP credential is "<state>"
    And the MCP storage and server restart
    Then MCP rejects access with "permission_denied" and no private metadata
    Examples:
      | state   |
      | expired |
      | revoked |

  Scenario: Tool permissions are scoped independently
    Given a "normal" MCP handoff
    When the connection is authorized only to read the role
    Then the role tool succeeds and the window tool returns permission_denied

  Scenario Outline: The control plane rejects every mismatched grant dimension
    Given a "normal" MCP handoff
    When the controller authorizes a grant with changed "<dimension>"
    Then the mismatched MCP grant is denied
    Examples:
      | dimension    |
      | role         |
      | binding      |
      | source       |
      | snapshotHead |
      | scope        |
      | permissions  |
      | expiry       |

  Scenario Outline: Invalid requests cannot inject identity or change scope
    Given a "normal" MCP handoff
    When an authenticated caller sends "<arguments>" as MCP tool arguments
    Then MCP rejects access with "invalid_reference" and no private metadata
    Examples:
      | arguments                                              |
      | {"anchorSeq":"01"}                                     |
      | {"anchorSeq":1}                                        |
      | {}                                                     |
      | {"anchorSeq":"current","roleId":"vesta"}                |
      | {"anchorSeq":"current","sessionId":"claimed-session"}   |
      | {"anchorSeq":"current","scope":{"seqS":"1","seqE":"2"}} |

  Scenario Outline: Damaged references and snapshots fail without a live fallback
    Given a "normal" MCP handoff
    When the committed MCP data suffers "<damage>"
    Then MCP rejects access with "<code>" and no private metadata
    Examples:
      | damage                   | code                |
      | non-handoff anchor       | invalid_reference   |
      | missing anchor           | invalid_reference   |
      | missing role revision    | invalid_reference   |
      | changed snapshot content | failed_precondition |
      | missing snapshot entry   | failed_precondition |
      | corrupt snapshot entry   | failed_precondition |
      | changed handoff metadata | failed_precondition |
      | storage unavailable      | unavailable         |

  Scenario: Tool calls and results keep their structured relationships
    Given a "tools" MCP handoff
    Then the MCP window preserves tool identities and omits private event metadata

  Scenario: Native turn and integration origin boundaries are projected safely
    Given a "boundaries" MCP handoff
    Then the MCP window preserves turn and integration session boundaries

  Scenario: A legitimately empty projection is complete and distinguishable
    Given an "empty" MCP handoff
    Then the MCP window has zero entries and is complete

  Scenario: Tape sequences retain arbitrary precision over MCP
    Given a "large sequences" MCP handoff
    Then both MCP tools return the committed role and context

  Scenario Outline: Unsupported or incomplete windows fail atomically
    Given a "<kind>" MCP handoff
    When I read the MCP context window
    Then MCP rejects access with "failed_precondition" and no private metadata
    Examples:
      | kind             |
      | unknown          |
      | orphan result    |
      | unfinished call  |
      | oversized        |
      | unsupported part |
      | malformed event  |

  Scenario: Published role versions cannot be overwritten
    Given a "normal" MCP handoff
    Then reusing the role version with different contents fails

  Scenario: MCP reads leave persistent storage untouched
    Given a "normal" MCP handoff
    Then repeated MCP reads leave tape and authorization storage unchanged
