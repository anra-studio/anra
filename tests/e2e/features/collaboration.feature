Feature: Anchor-backed nonblocking peer collaboration
  Reasoning owns one tape turn at a time. BUSY is an unaccepted request,
  while immutable permitted views remain independently borrowable.

  Scenario: Collaboration metadata uses generated ProtoJSON over standard A2A
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then generated collaboration metadata roundtrips through A2A

  Scenario Outline: Invalid delegation metadata cannot enter reasoning
    Given a collaboration agent
    Then collaboration rejects request metadata with <problem> before reasoning

    Examples:
      | problem                      |
      | missing root                 |
      | oversized root               |
      | missing deadline             |
      | invalid deadline             |
      | empty ancestor               |
      | oversized ancestor           |
      | excessive context            |
      | malformed context            |
      | missing context scope        |
      | reversed context scope       |
      | uncommitted context boundary |
      | unknown field                |

  Scenario Outline: Invalid response metadata is rejected through the A2A client
    Given a collaboration agent
    Then the client rejects response metadata with <problem>

    Examples:
      | problem                   |
      | unspecified status        |
      | unknown status            |
      | unknown retry             |
      | completed retry hint      |
      | completed view            |
      | malformed busy view       |
      | missing busy scope        |
      | uncommitted busy boundary |
      | answer in metadata        |
      | empty completed answer    |
      | local suppression status  |
      | legacy lowercase status   |

  Scenario: Only one simultaneous request enters reasoning
    Given a collaboration agent
    When simultaneous callers admit exactly one turn

  Scenario: A busy agent without completed findings does not accept another turn
    Given a collaboration agent
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    Then the response is BUSY without another turn or model call
    And BUSY reveals no view or private control fields
    And a guessed reference cannot obtain private or control entries

  Scenario: Completed context windows can be borrowed while the owner thinks
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    Then the response is BUSY without another turn or model call
    When Bob borrows the BUSY view while Alice remains occupied
    Then the view grant contains exactly the completed context window
    And another trusted requester gets its own grant but cannot read or release Bob's grant
    And view requests reject a changed reference and incompatible bounds
    When Alice finishes and commits a newer context window
    And Alice restarts with the same source and the grant remains readable
    Then the view grant contains exactly the completed context window

  Scenario Outline: View grants obey their terminal permission rules
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    And Bob borrows the BUSY view while Alice remains occupied
    Then the view grant is ended by <termination>

    Examples:
      | termination |
      | release     |
      | revocation  |
      | expiry      |

  Scenario: A TapeView retains the completed tool conversation and long final answer
    Given a collaboration agent
    Then Bob borrows Alice's completed tool conversation as a TapeView while she thinks

  Scenario: An ancestor callback uses BUSY and borrowed findings to finish
    Given a collaboration agent
    And Alice has completed a shareable answer
    When cyclic peer reasoning completes using BUSY and borrowed findings

  Scenario: Independent roots cannot deadlock one another
    Given a collaboration agent
    When independent roots consult one another without blocking

  Scenario: Sibling delegations preserve their root and independent paths
    Given a collaboration agent
    When Alice consults Bob and Charlie in separate turns
    Then sibling calls share their turn root and keep independent paths

  Scenario: Delegation can follow a path longer than eight hops
    Given a collaboration agent
    Then Alice completes a ten-hop delegation and retains its full ancestry

  Scenario: A changed prompt cannot turn BUSY into polling
    Given a collaboration agent
    When repeated BUSY delegation is suppressed only within each turn

  Scenario: The lender selects context and only newer borrowed findings permit another reasoning request
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then Bob resumes reasoning only after borrowing newer lender-selected findings

  Scenario: Cancellation preserves the physical tape and rebuilds valid model history
    Given a collaboration agent
    When Alice's cancelled turn closes and its partial history is excluded

  Scenario: An expired deadline stops reasoning before model or peer calls
    Given a collaboration agent
    Then an expired delegation deadline rejects reasoning before model or peer calls

  Scenario: View borrowing requires the advertised version
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then legacy lenders cannot silently accept view references

  Scenario: Killing a process preserves views and recovers its open turn
    Given a running team Peer directory
    And team agents "alice" and "bob" are serving
    And Alice is interrupted while reasoning after a published answer
    When I try to ask running agent "alice" to "rejected CLI callback"
    Then the CLI command fails with "busy: request was not accepted"
    When team agent "alice" crashes
    And the interrupted tape retains an unmatched tool call and state delta
    And its presence eventually becomes unknown
    And I restart team agent "alice"
    Then the recovered agent keeps its source, view, grant, and physical history
    When I stop the team agents
    Then exactly one abandoned close excludes the unfinished turn

  Scenario: Terminal storage failure cannot reopen reasoning
    Given a collaboration agent
    Then an unresolved terminal append fences further reasoning

  Scenario Outline: Corrupt control history cannot be interpreted as idle
    Given a collaboration agent
    Then startup rejects a tape containing <problem>

    Examples:
      | problem                      |
      | unknown v1 type              |
      | invalid v1 start             |
      | invalid v1 source            |
      | invalid v1 outcome           |
      | oversized identity           |
      | malformed integration record |
      | malformed record             |
      | overlapping starts           |
      | unmatched close              |
      | conflicting source           |

  Scenario Outline: Other anchor schemas coexist with native execution history
    Given a collaboration agent
    And Alice's tape contains a custom anchor with schema "<schema>"
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    When Bob asks the occupied Alice for reasoning
    And Bob borrows the BUSY view while Alice remains occupied
    And Alice finishes and commits a newer context window
    And Alice restarts with the same source and the grant remains readable
    Then the view grant contains exactly the completed context window
    And Alice can continue reasoning after schema dispatch recovery

    Examples:
      | schema                      |
      | anra.dev/anchor/intr/v1      |
      | anra.dev/anchor/handoff/v1   |
      | anra.dev/anchor/v2           |
      | example.org/anchor/v1        |

  Scenario: Generic A2A callers get clear BUSY text without private hints
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then generic A2A callers receive an unaccepted response with no hint

  Scenario: Shutdown drains the runtime before closing its tape
    Given a collaboration agent
    Then shutdown drains active reasoning before storage closes

  Scenario: Supplied view references are borrowed under the recipient's identity
    Given a collaboration agent
    And Alice has completed a shareable answer
    And Alice is held inside its model call
    Then Bob borrows supplied context without reentering Alice

  Scenario Outline: Mismatched grants cannot expand a view reference
    Given a collaboration agent
    And Alice has completed a shareable answer
    Then a client rejects a view grant with mismatched <provenance>

    Examples:
      | provenance |
      | source     |
      | range      |
      | head       |
