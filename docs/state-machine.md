# 状态机与重连语义

- 文档版本：1.0
- 冻结日期：2026-10-23
- 状态：A03 契约文档
- 单一事实来源：所有共享状态只由 Hub 事件循环串行修改；连接与定时器只能提交命令。

## 1. 总体模型

```text
连接命令 / 定时器 / 管理命令
            |
            v
        Hub 事件循环  --->  公开快照、定向推送、观察事件
            |
            +--> 参与者 Participant
            +--> 会话 Session
            +--> 课堂 Round
            +--> 消息 Message
```

- 所有命令携带 `round_id` 与/或 `connection_epoch`；Hub 在处理入口先校验，再修改状态。
- 状态转换只产生一次副作用；重复命令必须是幂等的或返回明确错误。
- 旧轮次、旧连接代次、旧会话 ID 的迟到事件不得创建或修改当前状态。

## 2. 课堂状态

| 状态 | 含义 | 允许新加入 | 允许会话 |
| --- | --- | --- | --- |
| `RUNNING` | 当前轮次进行中 | 是 | 是 |
| `ENDED` | 本轮已结束 | 否 | 否 |

转换：

| 当前 | 事件 | 守卫 | 结果 |
| --- | --- | --- | --- |
| — | 进程启动 | — | 生成新 `round_id`，`RUNNING` |
| `RUNNING` | `round.reset(request_id, expected_round_id)` | 认证教师；`expected_round_id` 匹配 | 生成新 `round_id`，保持 `RUNNING`；清空本轮所有状态 |
| `RUNNING` | `class.end(request_id, expected_round_id)` | 同上 | 清空本轮状态，进入 `ENDED` |
| `ENDED` | `class.start(request_id, expected_round_id)` | 同上 | 生成新 `round_id`，进入 `RUNNING` |
| 任意 | `server.shutdown` | 认证教师 | 发结束通知，有限等待后退出 |

`expected_round_id` 不匹配返回 `STALE_ROUND`。相同 `request_id` 与相同载荷的重试返回进程级缓存中的原结果；相同 `request_id` 改载荷返回 `ID_CONFLICT`。管理操作结果缓存不随轮次清空。

## 3. 参与者状态

| 状态 | 含义 | 保留时限 | 可被邀请 |
| --- | --- | --- | --- |
| `ONLINE` | 有一条当前代次连接 | — | 是 |
| `OFFLINE` | 连接已关闭，保留恢复信息 | 最多 60 秒 | 否 |
| `REMOVED` | 恢复窗口结束或被清理 | — | 否 |

转换：

| 当前 | 事件 | 守卫 | 结果 |
| --- | --- | --- | --- |
| — | `join` | 容量、昵称、速率合法 | 分配新身份，`ONLINE`，返回 `welcome` |
| `OFFLINE` | `resume` | 60 秒内、token 正确、轮次匹配 | 先关闭旧连接与全部旧会话，换代次、轮换 token，`ONLINE` |
| `ONLINE` | 当前连接关闭 | epoch 匹配 | 级联关闭其邀请/会话，`OFFLINE`，启动 60 秒计时 |
| `ONLINE` | 心跳超时 | epoch 匹配 | 按连接关闭处理 |
| `OFFLINE` | 恢复窗口到期 | — | `REMOVED`，释放身份名额 |
| 任意 | `leave` | epoch 匹配 | 级联关闭后直接 `REMOVED`，不保留恢复 |
| 任意 | 轮次重置/结束 | — | 立即 `REMOVED` |

任意时刻一个身份最多只有一条受控连接。合法恢复必须先关闭旧连接与旧会话，再绑定新连接；旧连接的关闭回调因 epoch 不匹配被丢弃。

## 4. 会话状态

| 状态 | 含义 | 可发聊天 | 计入会话额度 |
| --- | --- | --- | --- |
| `INVITED` | 邀请已发出，等待回应 | 否 | 是 |
| `NEGOTIATING` | 已接受加密邀请，等待双方 ready | 否 | 是 |
| `ACTIVE` | 可通信 | 是（`ACTIVE` 且连接正常） | 是 |
| `CLOSED` | 已关闭，终态 | 否 | 否 |

模式 `mode` 在创建时固定为 `plaintext` 或 `encrypted`，生命周期内不可改变。`CLOSED` 不可复活；重新通信必须创建新 `session_id`。

### 4.1 创建

| 当前 | 事件 | 守卫 | 结果 |
| --- | --- | --- | --- |
| — | `invite.create(from, to, mode)` | 双方在线；非自己；无同对未关闭会话；双方额度未满 | 创建 `INVITED`，记录模式，向双方推送 `session.changed` |
| `INVITED` | 同一对再次 `invite.create` | — | 不创建第二条；后到者收到当前会话状态（`SESSION_EXISTS` 语义） |
| — | 双向同时邀请 | Hub 顺序处理 | 只有先到者创建；后到者绑定/获知同一会话 |

### 4.2 接受、拒绝与超时

| 当前 | 事件 | 守卫 | 结果 |
| --- | --- | --- | --- |
| `INVITED` | 被邀请人接受，`mode=plaintext` | 是预期收件人；未超时；轮次/epoch 有效 | `ACTIVE` |
| `INVITED` | 被邀请人接受，`mode=encrypted` | 同上 | `NEGOTIATING`，启动 15 秒协商计时 |
| `INVITED` | 被邀请人拒绝 | 是预期收件人 | `CLOSED`，原因 `rejected` |
| `INVITED` | 邀请方取消 | 是邀请方 | `CLOSED`，原因 `cancelled` |
| `INVITED` | 30 秒到期 | 定时器属于本会话 | `CLOSED`，原因 `invite_timeout` |
| `NEGOTIATING` | 15 秒到期 | 定时器属于本会话 | `CLOSED`，原因 `key_timeout` |
| `NEGOTIATING` | 任一方密钥失败 | 本会话 | `CLOSED`，原因 `crypto_failed` |

到期后的接受、取消或 ready 无效，不得改变已关闭会话，也不得关闭后续新会话。

### 4.3 加密协商与激活

```text
INVITED --accept(encrypted)--> NEGOTIATING
NEGOTIATING --key.publish(both)--> NEGOTIATING
NEGOTIATING --key.confirm(both verified locally)--> NEGOTIATING
NEGOTIATING --key.ready(both)--> ACTIVE
NEGOTIATING --timeout/failure/close--> CLOSED
```

- 双方各自生成临时密钥对；A 只转交公钥。
- 双方公钥在本会话固定；已固定后收到不同公钥必须关闭会话，不静默替换。
- `key.ready` 是客户端报告“本地已成功验证对端确认”。Hub 收齐两端 ready 才激活，不声称自己验证了秘密。
- 只收到一方 ready 时不能发送聊天。

### 4.4 关闭

| 当前 | 事件 | 守卫 | 结果 |
| --- | --- | --- | --- |
| `ACTIVE` | 任一方 `session.close` | 是本会话成员 | `CLOSED`，通知双方，释放额度 |
| `INVITED`/`NEGOTIATING`/`ACTIVE` | 参与者离线/离开 | epoch 匹配 | 级联 `CLOSED`，原因 `peer_offline` |
| 任意未关闭 | 轮次重置/结束 | — | 直接清理，不产生跨轮次通知 |
| `CLOSED` | 再次关闭 | — | 无副作用，返回已关闭结果 |

同一对参与者在一个轮次内同时最多一个非 `CLOSED` 会话。关闭后可以重新邀请并选择另一模式。

## 5. 连接状态

| 状态 | 含义 | 允许消息 |
| --- | --- | --- |
| `CONNECTED` | WebSocket 已建立，尚未加入/认证 | `join`、`resume`、admin `admin.auth`、`ping` |
| `STUDENT` | 学生身份已绑定当前 epoch | 学生消息族 |
| `TEACHER` | 管理认证成功 | 管理消息族 |
| `CLOSED` | 连接已关闭 | 无 |

- 每个连接只有一个写出口和独立写循环；Hub 不等待网络写。
- 单连接待写队列超过 64 条或 256 KiB，或写超时，关闭该连接并产生 `SLOW_CLIENT`；只影响该连接。
- 连接代次 `connection_epoch` 由服务端生成，学生每条已加入命令必须携带；不匹配返回 `STALE_CONNECTION`。

## 6. 消息显示状态（客户端）

| 状态 | 进入条件 | 后续 |
| --- | --- | --- |
| `SENDING` | 用户提交且本地通过长度检查 | 收到 `chat.accepted` → `ACCEPTED`；发送失败 → `FAILED` |
| `ACCEPTED` | A 已接受该消息 | 收到 `chat.status(confirmed)` → `CONFIRMED`；5 秒无确认 → `UNCONFIRMED` |
| `CONFIRMED` | 预期收件人报告成功处理 | 终态；迟到确认不重复 |
| `UNCONFIRMED` | `ACCEPTED` 后 5 秒无确认 | 迟到确认可更新为 `CONFIRMED` |
| `FAILED` | A 拒绝、连接断开或本地构造失败 | 终态；首版不自动重发 |

`CONFIRMED` 的含义是“接收端程序报告已成功处理”，不是“本人已读”，也不表示服务端独立验证了解密。

## 7. 重连与恢复时序

```text
页面检测 WebSocket 关闭
  -> 禁发；清除本页会话密钥与旧聊天状态
  -> 60 秒内在同一页面用内存 resume_token 发送 resume
Hub:
  -> 校验 token、轮次、恢复窗口
  -> 关闭旧连接与旧邀请/会话（epoch 失效）
  -> 分配新 connection_epoch，轮换 resume_token
  -> 返回身份、昵称、短编号、颜色和新快照
  -> 旧会话、旧密文、未确认消息不恢复、不自动重发
```

- 页面刷新、恢复窗口超时或轮次改变：创建新身份，重新输入昵称。
- 合法恢复即使先于旧连接心跳超时到达，也按“先关闭旧连接与会话，再绑定新连接”处理。
- 旧连接的任何回调因 epoch 不匹配被丢弃，不能影响新连接。

## 8. 重置与清理顺序

`round.reset` / `class.end` / `class.start` 的原子清理顺序：

1. 校验管理认证、`request_id`、`expected_round_id`；命中原幂等结果则直接返回。
2. 生成新 `round_id`（`class.end` 不生成可用轮次，进入 `ENDED`）。
3. 停止并失效所有旧计时器与在途命令。
4. 关闭所有学生连接；清空发送队列与待写项。
5. 清空参与者、邀请、会话、公钥、密钥确认与消息去重状态。
6. 清空观察历史、累计统计、订阅与快照游标。
7. 失效全部恢复 token、连接代次与旧轮次消息。
8. 向客户端推送 `round.ended`；学生回到加入页；教师界面更新。
9. 保存管理操作幂等结果（进程级、跨轮次保留）。

已清空轮次的迟到消息、旧队列项和迟到回执一律丢弃，不得写入新轮次。

## 9. 不变量

1. 同一身份任意时刻最多一条受控连接。
2. 同一对参与者任意时刻最多一个非 `CLOSED` 会话。
3. `CLOSED` 是终态，不可复活；新会话必须使用新 `session_id`。
4. 会话模式固定，不能中途从明文切到加密或反向。
5. 加密会话的公钥固定；变化即关闭，不静默替换。
6. 只有 `ACTIVE` 且成员、轮次、代次、模式全部匹配的业务消息可被接受。
7. 每条首次接受的业务消息只生成一次观察事件；控制消息、转发、回执、心跳不计入聊天统计。
8. 观察累计统计不因历史淘汰回退。
9. 管理 token、恢复 token、私钥、共享秘密绝不进入公共广播、观察记录或默认日志。
10. 轮次重置后，旧轮次任何事件不能创建新状态。
