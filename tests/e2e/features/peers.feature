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
    当 调用方读取完整授权视图
    而且 研究节点在授权后提交新的发现
    而且 研究节点重启存储和服务
    那么 重试同一请求仍返回原 lease
    而且 调用方再次读取相同的原有授权视图

  场景: 多段已提交视图一次完整读取
    假如 研究节点已提交 103 条发现
    当 调用方请求借阅已提交视图
    那么 调用方完整读出全部 103 条发现

  场景: 缺失授权条目时整次读取失败
    当 调用方请求借阅已提交视图
    而且 授权视图后一段的正文丢失并重启存储
    那么 读取缺失的授权视图失败且不返回部分内容

  场景: 重用请求 ID 返回原有借阅决定
    当 调用方请求借阅已提交视图
    而且 调用方保持请求 ID 但修改用途、查询和范围
    那么 返回原有借阅决定且 lease 仍可读取

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
    而且 审批租期无效时原待审批决策不变
    而且 另一借阅服务读取当前待审批决策
    当 研究节点将请求处理为 "<outcome>"
    而且 另一服务的审批和重复借阅均不能改写结果
    而且 研究节点重启存储和服务
    那么 恢复后的决策仍为 "<outcome>"
    而且 读取结果符合 "<outcome>" 决策
    而且 已完成的请求不能再次审批且原决策不变

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
      | 空 lease ID        | invalid_argument    |

  场景大纲: 原生 OverView 的候选授权通过独立 Journal 保存和释放
    假如 授权 sidecar 使用 "<backend>" 存储
    而且 保存源 Tape 的正文和序号
    那么 未绑定 grant handler 的 Tape 不能创建借阅服务
    而且 原生 OverView 拒绝无效授权命令且 sidecar 不变
    而且 未提交的候选 lease 不能读取或释放且 sidecar 不变
    当 重复注册 grant handler 被拒绝且原绑定仍可使用
    而且 授权终止方式为 released
    那么 sidecar 按顺序保留同一视图的授权和释放
    而且 源 Tape 的正文和序号保持不变
    当 研究节点重启存储和服务
    那么 sidecar 按顺序保留同一视图的授权和释放
    而且 源 Tape 的正文和序号保持不变
    而且 调用方不能继续读取该 lease
    当 其他 Source 不能重新绑定该授权 sidecar
    那么 sidecar 按顺序保留同一视图的授权和释放
    而且 调用方不能继续读取该 lease

    例子:
      | backend |
      | bbolt   |
      | jsonl   |

  场景大纲: 多个服务共享 Journal 时审批只能提交一次
    假如 授权 sidecar 使用 "<backend>" 存储
    而且 保存源 Tape 的正文和序号
    而且 研究节点要求本地批准借阅
    当 调用方请求借阅已提交视图
    那么 借阅决策为 "pending"
    而且 取消的授权请求不追加 sidecar 记录
    当 两个借阅服务并发批准同一请求且只有一个结果提交
    而且 研究节点重启存储和服务
    那么 重启后仅恢复已提交的审批结果且 lease 可读取
    而且 源 Tape 的正文和序号保持不变

    例子:
      | backend |
      | bbolt   |
      | jsonl   |

  场景大纲: Approve 和 Reject 分别表达批准与拒绝
    假如 授权 sidecar 使用 "<backend>" 存储
    而且 研究节点要求本地批准借阅
    那么 无可用视图时批准保留 pending 且只能显式拒绝

    例子:
      | backend |
      | bbolt   |
      | jsonl   |
