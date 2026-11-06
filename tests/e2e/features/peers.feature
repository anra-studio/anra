# language: zh-CN
功能: 节点通过 A2A 通信并借阅已提交视图
  调用方只读取完整获准的已提交 Scopes，授权决策和终止状态在重启后保持。

  背景:
    假如 研究节点已提交两份发现并持久化授权记录

  场景: 通过标准 A2A 向发现的节点发送消息
    当 调用方发现研究节点并发送 "hello"
    那么 节点回答 "research response"
    而且 节点也能流式回答 "hello again"

  场景: 授权范围在借出节点重启后保持不变
    当 调用方请求借阅已提交视图
    那么 借阅决策为 "granted"
    当 调用方读取第一条发现
    而且 研究节点在授权后提交新的发现
    而且 研究节点重启存储和服务
    那么 重试同一请求仍返回原 lease
    而且 调用方分页读完原有授权视图

  场景: 多段已提交视图经过分页仍完整读取
    假如 研究节点已提交 103 条发现
    当 调用方请求借阅已提交视图
    那么 调用方跨页读完全部 103 条发现

  场景: 同一请求 ID 不能改写借阅输入
    当 调用方请求借阅已提交视图
    而且 调用方保持请求 ID 但修改查询
    那么 借阅请求返回冲突

  场景: 其他调用方不能使用已有借阅授权
    当 调用方请求借阅已提交视图
    那么 其他调用方不能查询读取或释放该 lease
    而且 其他调用方自己的请求被拒绝且不返回内容

  场景大纲: 终止授权后重启仍拒绝读取
    当 调用方请求借阅已提交视图
    而且 授权终止方式为 <closure>
    而且 研究节点重启存储和服务
    那么 调用方不能继续读取该 lease
    而且 记录的决策仍标识原 lease

    例子:
      | closure  |
      | released |
      | revoked  |
      | expired  |

  场景大纲: 待批准的借阅决策可以完成并恢复
    假如 研究节点要求本地批准借阅
    当 调用方请求借阅已提交视图
    那么 借阅决策为 "pending"
    当 研究节点重启存储和服务
    那么 恢复后的决策仍为 "pending"
    当 研究节点将请求处理为 "<outcome>"
    而且 研究节点重启存储和服务
    那么 恢复后的决策仍为 "<outcome>"
    而且 读取结果符合 "<outcome>" 决策

    例子:
      | outcome |
      | granted |
      | denied  |

  场景大纲: 无效或未授权的协议请求不能借阅上下文
    当 节点提交包含 "<problem>" 的协议请求
    那么 返回协议错误 "<code>"

    例子:
      | problem           | code                |
      | no identity       | unauthenticated     |
      | no extension      | failed_precondition |
      | missing purpose   | invalid_argument    |
      | reversed bounds   | invalid_argument    |
      | excessive page    | invalid_argument    |
