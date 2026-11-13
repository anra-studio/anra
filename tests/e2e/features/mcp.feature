# language: zh-CN
功能: 通过只读 MCP 获取交接上下文
  外部运行时凭连接凭证读取已提交交接绑定的角色版本和Tape 视图。
  后续推理、角色更新和服务重启不能扩大授权范围，失败响应不得泄露私有信息。

  场景: 官方客户端发现并读取两个只读工具且不改变存储
    假如 已提交一份普通 MCP 交接并签发凭证
    而且 控制器使用默认 validator 验证角色定义和授权
    当 官方 MCP 客户端使用交接凭证连接
    那么 客户端仅发现两个只读上下文工具
    而且 两个工具返回交接绑定的角色版本和 Tape 视图
    而且 MCP 服务重建和重复读取均不新增文件或改变 Tape

  场景: 原始文本条目仅通过结构化内容导出
    假如 已提交只含原始文本条目的 MCP 交接并签发凭证
    当 读取 MCP Tape 视图
    那么 原始文本仅作为结构化内容返回且没有摘要字段
    而且 A2A 与 MCP 返回相同的公开条目及存储标识

  场景: 空 Parts 的已提交条目在 MCP 与 peer 中一致保留
    假如 已提交包含空 Parts 条目的 MCP 交接并签发凭证
    当 读取 MCP Tape 视图
    那么 Quote 保留该空 Parts 条目的角色和序号
    而且 A2A 与 MCP 返回相同的公开条目及存储标识

  场景: Anra 条目内容和扩展在分配元数据和 JSONL 存储重建后保持完整
    假如 已提交一份普通 MCP 交接并签发凭证
    当 运行时将交接和视图存入 JSONL Tape
    而且 控制器通过 Anra 条目追加带扩展的新视图
    而且 读取 MCP Tape 视图
    那么 Anra 条目内容和扩展在存储后保持完整
    当 JSONL 存储和 MCP 服务重建
    而且 读取 MCP Tape 视图
    那么 Anra 条目内容和扩展在存储后保持完整

  场景: 借阅视图在导出和重启后保留多模态内容
    假如 已提交包含图片文件和代码结果的 MCP 交接并签发凭证
    当 读取 MCP Tape 视图
    那么 借阅内容保留图片文件和代码结果的原始顺序且不泄露执行元数据
    而且 A2A 与 MCP 返回相同的公开条目及存储标识
    当 Tape 和 MCP 服务重启
    而且 读取 MCP Tape 视图
    那么 借阅内容保留图片文件和代码结果的原始顺序且不泄露执行元数据

  场景: 大型 MCP 请求和音频响应完整传输且仍受宿主授权约束
    假如 已提交包含大型音频的 MCP 交接并签发凭证
    当 使用超过 SDK 默认大小的 MCP 请求读取 Tape 视图
    那么 MCP 完整返回已授权的大型音频及来源
    当 使用不匹配凭证的锚点读取 MCP Tape 视图
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    当 官方 MCP 客户端使用交接凭证连接
    那么 官方 MCP 客户端能完整读取大型音频

  场景大纲: 同一多段 lease 在 MCP 与 peer 共享身份隔离和终止状态
    假如 已提交两段非相邻内容并签发共享 lease 凭证
    那么 原生多段 View 只持久化一条完整授权记录
    当 读取 MCP Tape 视图
    那么 Quote 完整保留两段内容且不包含间隙
    而且 A2A 与 MCP 返回相同的公开条目及存储标识
    而且 其他调用方均不能通过 MCP 或 peer 读取该 lease
    而且 peer 拒绝改写完整 Scopes 的借阅请求
    当 通过 "<方式>" 终止共享 lease
    而且 读取 MCP Tape 视图
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    而且 peer 也拒绝读取该共享 lease
    而且 角色读取仍遵守独立授权
    当 Tape 和 MCP 服务重启
    而且 读取 MCP Tape 视图
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    而且 peer 也拒绝读取该共享 lease

    例子:
      | 方式     |
      | peer释放 |
      | 本地撤销 |
      | lease到期 |

  场景: 控制器提交的交接必须单独授权后才能读取
    假如 已提交一份普通 MCP 交接并签发凭证
    而且 运行时将交接和视图存入 JSONL Tape
    当 控制器向 Tape 提交另一份交接
    而且 使用原凭证读取新交接
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    当 控制器为新交接签发凭证
    而且 官方 MCP 客户端使用交接凭证连接
    那么 两个工具返回交接绑定的角色版本和 Tape 视图

  场景: 借阅内容不能通过伪造通道或来源声明提升权限
    假如 已提交包含伪造通道和来源声明的 MCP 交接并签发凭证
    当 读取 MCP Tape 视图
    那么 借阅 Quote 仅携带 lease 绑定的通道与来源
    而且 借阅内容保留思考标记和文本且不泄露执行元数据
    而且 A2A 与 MCP 返回相同的公开条目及存储标识
    当 控制器为同一视图签发另一调用方的凭证
    而且 读取 MCP Tape 视图
    那么 借阅 Quote 仅携带 lease 绑定的通道与来源
    当 Tape 和 MCP 服务重启
    而且 读取 MCP Tape 视图
    那么 借阅 Quote 仅携带 lease 绑定的通道与来源
    而且 借阅内容保留思考标记和文本且不泄露执行元数据

  场景: 凭证隔离角色并在服务重启后保持撤销
    假如 已提交一份普通 MCP 交接并签发凭证
    当 未携带凭证读取上下文
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 使用当前凭证访问另一角色的服务
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    当 连接仅获准读取角色
    那么 角色工具成功且视图工具返回 permission_denied
    当 控制器撤销当前凭证
    而且 读取 MCP 角色
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    当 Tape 和 MCP 服务重启
    而且 读取 MCP 角色
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息

  场景: MCP 上下文使用统一借阅审批和租期
    假如 已提交一份普通 MCP 交接并签发凭证
    而且 宿主拒绝后续上下文借阅
    当 控制器通过 peer API 借阅交接视图
    那么 本次借阅被拒绝且未签发 lease
    当 宿主批准后续上下文借阅且租期为十五分钟
    而且 控制器通过 peer API 借阅交接视图
    那么 新 lease 使用审批指定的十五分钟租期
    当 读取 MCP Tape 视图
    那么 MCP 响应包含交接绑定的 Tape 视图
    当 共享时钟前进十六分钟
    而且 读取 MCP Tape 视图
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    而且 角色读取仍遵守独立授权

  场景: HTTP 认证成功不能绕过工具调用时的凭证撤销
    假如 已提交一份普通 MCP 交接并签发凭证
    而且 宿主在下一次 HTTP 认证通过后撤销凭证
    当 读取 MCP Tape 视图
    那么 MCP 返回 "permission_denied" 且不泄露上下文或私有信息
    当 读取 MCP 角色
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息

  场景: 单个 Bearer 凭证兼容大小写和多个空格且重复认证头一律拒绝
    假如 已提交一份普通 MCP 交接并签发凭证
    当 携带以下 Authorization 请求头读取上下文
      | bEaReR   {token} |
    那么 MCP 响应包含交接绑定的 Tape 视图
    当 携带以下 Authorization 请求头读取上下文
      | Bearer {token} |
      | Bearer invalid |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 携带以下 Authorization 请求头读取上下文
      | Bearer invalid |
      | Bearer {token} |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 携带以下 Authorization 请求头读取上下文
      | Bearer {token} |
      | Bearer {token} |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 携带以下 Authorization 请求头读取上下文
      | Bearer |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 携带以下 Authorization 请求头读取上下文
      | Bearer {token} extra |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 读取 MCP Tape 视图
    那么 MCP 响应包含交接绑定的 Tape 视图

  场景: 凭证验证不可用返回 503 且重复认证头仍返回 401
    假如 已提交一份普通 MCP 交接并签发凭证
    当 凭证验证服务暂时不可用
    而且 读取 MCP Tape 视图
    那么 MCP 返回 HTTP 503 且不泄露上下文或私有信息
    当 携带以下 Authorization 请求头读取上下文
      | Bearer {token} |
      | Bearer {token} |
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息

  场景: 并发推理和重启不会改变已交接快照
    假如 已提交一份普通 MCP 交接并签发凭证
    当 记录两个工具的原始结果
    而且 角色开始一轮等待模型返回的推理
    那么 两个工具的结果逐字节保持不变
    当 当前推理正常结束
    而且 角色版本更新并继续写入 Tape
    而且 Tape 和 MCP 服务重启
    那么 两个工具的结果逐字节保持不变

  场景: 已结束的工具流保留分块顺序和跨协议投影一致性
    假如 已提交包含完整工具响应流的 MCP 交接并签发凭证
    当 读取 MCP Tape 视图
    那么 视图保留工具标识和所有有序响应分块
    而且 A2A 与 MCP 返回相同的公开条目及存储标识

  场景: Origin 配置限制浏览器访问且始终要求凭证
    假如 已提交一份普通 MCP 交接并签发凭证
    当 携带凭证从 Origin "https://trusted.example" 读取上下文
    那么 MCP 响应包含交接绑定的 Tape 视图
    当 从 Origin "https://trusted.example" 不带凭证读取上下文
    那么 MCP 返回 HTTP 401 且不泄露上下文或私有信息
    当 运行时仅信任 Origin "https://trusted.example"
    而且 从 Origin "https://evil.example" 不带凭证读取上下文
    那么 MCP 返回 HTTP 403 且不泄露上下文或私有信息
    当 携带凭证从 Origin "https://evil.example" 读取上下文
    那么 MCP 返回 HTTP 403 且不泄露上下文或私有信息
    当 携带凭证从 Origin "https://trusted.example" 读取上下文
    那么 MCP 响应包含交接绑定的 Tape 视图
    当 官方 MCP 客户端使用交接凭证连接
    那么 两个工具返回交接绑定的角色版本和 Tape 视图
