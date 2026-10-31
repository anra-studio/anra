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

  Scenario: Committing a handoff writes only to Tape
    Given a "normal" MCP handoff
    When another handoff commits without changing authorization storage
    And the controller authorizes the persisted handoff
    And the MCP storage and server restart
    Then both MCP tools return the committed role and context

  Scenario: A persisted Tape handoff requires no secondary commit record
    Given a "normal" MCP handoff
    When the controller appends the handoff directly to Tape
    And the MCP storage and server restart
    Then MCP rejects access with "permission_denied" and no private metadata
    When the controller authorizes the persisted handoff
    Then both MCP tools return the committed role and context

  Scenario Outline: Handoff commit rejects an unpersisted window without appending an anchor
    Given a "normal" MCP handoff
    When the controller commits a window with "<missing>"
    Then the handoff commit fails without changing Tape or authorization storage
    Examples:
      | missing          |
      | missing boundary |
      | missing entry    |

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
      | damage                       | code                |
      | non-handoff anchor           | invalid_reference   |
      | missing anchor               | invalid_reference   |
      | missing role revision        | invalid_reference   |
      | unsupported snapshot content | failed_precondition |
      | missing snapshot entry       | failed_precondition |
      | corrupt snapshot entry       | failed_precondition |
      | changed handoff binding      | permission_denied   |
      | missing snapshot boundary    | failed_precondition |
      | storage unavailable          | unavailable         |

  Scenario: Tool calls and results keep their structured relationships
    Given a "tools" MCP handoff
    Then the MCP window preserves tool identities and omits private event metadata

  Scenario: A continuing tool response keeps its tool call open
    Given an "unfinished stream" MCP handoff
    When I read the MCP context window
    Then MCP rejects access with "failed_precondition" and no private metadata

  Scenario: A tool stream closes only after its terminal chunk
    Given a "complete stream" MCP handoff
    When I read the MCP context window
    Then the MCP window contains every ordered chunk and is complete

  Scenario: Business JSON numbers and unknown keys survive projection exactly
    Given a "precise tool JSON" MCP handoff
    When I read the MCP context window
    Then both MCP result representations preserve the raw business JSON

  Scenario Outline: A2A and MCP publish the same model projection
    Given a "<kind>" MCP handoff
    When I read the MCP context window
    Then A2A exports the same public entry content and identity
    Examples:
      | kind              |
      | normal            |
      | plain text        |
      | tools             |
      | complete stream   |
      | precise tool JSON |
      | empty tool JSON   |

  Scenario: Empty tool argument and response objects are preserved
    Given an "empty tool JSON" MCP handoff
    When I read the MCP context window
    Then both MCP result representations preserve empty business JSON objects

  Scenario Outline: Unknown nested protocol fields fail atomically
    Given a "<kind>" MCP handoff
    When I read the MCP context window
    Then MCP rejects access with "failed_precondition" and no private metadata
    Examples:
      | kind                    |
      | unknown content field   |
      | unknown call field      |
      | unknown response field  |
      | unknown response part   |
      | unknown response media  |
      | streamed call arguments |
      | call continuation       |

  Scenario Outline: Invalid Origins are rejected even with a valid credential
    Given a "normal" MCP handoff
    And the embedding runtime trusts Origin "https://trusted.example"
    When an authenticated MCP caller sends Origin "<origin>" using "<method>"
    Then MCP returns HTTP 403 without context or private metadata
    Examples:
      | origin                     | method |
      | https://evil.example        | POST   |
      | https://evil.example        | GET    |
      | https://evil.example        | DELETE |
      | null                        | POST   |
      | https://trusted.example.evil | POST |
      | https://trusted.example/    | POST   |
      | http://trusted.example      | POST   |
      | https://trusted.example:444 | POST   |

  Scenario Outline: An unconfigured Origin allowlist permits authenticated requests
    Given a "normal" MCP handoff
    And the embedding runtime leaves trusted Origins "<configuration>"
    When an authenticated MCP caller sends Origin "<origin>" using "POST"
    Then the MCP response contains the complete committed window
    Examples:
      | configuration | origin                  |
      | unset         | endpoint                |
      | unset         | https://trusted.example |
      | unset         | null                    |
      | empty         | https://evil.example    |

  Scenario: An unconfigured Origin allowlist still requires a Bearer credential
    Given a "normal" MCP handoff
    When an MCP caller sends Origin "https://trusted.example" without a credential
    Then MCP returns HTTP 401 without context or private metadata

  Scenario Outline: Configured Origin trust is never inferred from request or proxy headers
    Given a "normal" MCP handoff
    And the embedding runtime trusts Origin "https://trusted.example"
    When an authenticated MCP caller sends Origin "<origin>" using "POST"
    Then MCP returns HTTP 403 without context or private metadata
    Examples:
      | origin                  |
      | endpoint                |
      | https://evil.example    |

  Scenario Outline: Origin headers must contain exactly one nonempty serialized origin
    Given a "normal" MCP handoff
    And the embedding runtime trusts Origin "https://trusted.example"
    When an authenticated MCP caller sends "<headers>" Origin headers
    Then MCP returns HTTP 403 without context or private metadata
    Examples:
      | headers          |
      | empty            |
      | repeated trusted |
      | trusted and evil |
      | comma separated  |
      | space separated  |

  Scenario Outline: Invalid Origin configuration is rejected at startup
    Given a "normal" MCP handoff
    When the embedding runtime configures invalid trusted Origin "<origin>"
    Then the MCP Origin configuration is rejected
    Examples:
      | origin                          |
      | *                               |
      | null                            |
      | https://trusted.example/        |
      | https://trusted.example/path    |
      | https://user@trusted.example    |
      | https://*.example               |
      | https://trusted.example?query=1 |
      | ftp://trusted.example           |
      | https://:443                    |
      | https://trusted.example#part    |
      | https://trusted.example?        |
      | https://trusted.example#        |

  Scenario Outline: Explicitly trusted browser Origins can read the committed window
    Given a "normal" MCP handoff
    And the embedding runtime trusts Origin "<origin>"
    When an authenticated MCP caller sends Origin "<origin>" using "POST"
    Then the MCP response contains the complete committed window
    Examples:
      | origin                          |
      | https://trusted.example         |
      | https://trusted.example:8443    |
      | http://localhost:8080           |
      | http://[::1]:8080               |

  Scenario: Native MCP clients work without Origin after trust is configured
    Given a "normal" MCP handoff
    And the embedding runtime trusts Origin "https://trusted.example"
    When an official MCP client connects with the handoff credential
    Then both MCP tools return the committed role and context

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
