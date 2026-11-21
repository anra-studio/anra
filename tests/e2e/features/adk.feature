Feature: ADK 使用配置的模型和内置工具
  确定性模型通过真实 HTTP 接收请求，工具调用与结果写入 Tape。

  Scenario: 请求发往配置的 OpenAI 兼容路径和模型
    Given an OpenAI-compatible endpoint with base path "/gateway/v1" and model "custom-model"
    When I run the local CLI agent "alice" with "report requested model"
    Then the CLI answer is "custom-model"

  Scenario: 模型通过工具发现并联系节点，重启后可回忆答复
    Given a running team Peer directory
    And team agents "alice" and "bob" are serving
    When I ask running agent "alice" to "discover peers and ask bob to say hello"
    Then the CLI answer is "bob: hello from alice"
    When I stop team agent "alice"
    Then agent "alice" has persisted these ADK tool calls and results
      | tool              |
      | discover_peers    |
      | send_peer_message |
    When I restart team agent "alice"
    And I ask running agent "alice" to "recall the peer answer"
    Then the CLI answer is "bob: hello from alice"
    When I stop the team agents
    Then the CLI agent "bob" has this conversation on its tape
      | role      | text                  |
      | user      | hello from alice      |
      | assistant | bob: hello from alice |

  Scenario: 同一轮推理记录 ADK 的模型调用和 Bash 工具结果
    Given the environment attempts to select an injected tool
    When I run the local CLI agent with its built-in Bash tool
    Then the CLI answer is "anra-bash-test"
    And agent "alice" has persisted these ADK tool calls and results
      | tool |
      | bash |
    And agent "alice" has one completed turn enclosing its model and tool steps

  Scenario: ADK 读取旧版模型和工具历史且保留原记录
    Given an agent with persisted legacy model and tool history
    When I ask "follow-up" and the model answers "history retained"
    Then the answer is "history retained"
    And the model retains legacy tool history without rewriting old entries
