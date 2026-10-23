# WebSocket 应用协议

- 文档版本：1.0
- 协议版本：`v = 1`
- 冻结日期：2026-10-23
- 状态：A04 契约文档
- 传输：JSON WebSocket 文本消息；关闭压缩；二进制字段使用 Base64
- 上限：完整应用消息 ≤ 64 KiB；正文 ≤ 4096 UTF-8 字节；密文体 ≤ 32 KiB

## 1. 通用信封

所有消息是 UTF-8 JSON 对象，必须包含整数 `v` 和字符串 `type`。

- 客户端已加入命令还带：`request_id`、`round_id`、`connection_epoch`。
- 会话命令还带：`session_id`。
- 服务端响应若对应某个命令，带原 `request_id`；主动推送不带 `request_id`。
- 服务端按接收连接附带当前 `connection_epoch`，不能把发送者的代次广播给所有接收者。
- `sender_id` 由 A 填写；客户端外层出现 `sender_id` 一律忽略或拒绝，不能冒充。

### 1.1 标识编码

| 字段 | 编码 | 长度/格式 |
| --- | --- | --- |
| `round_id`、`participant_id`、`session_id`、`message_id`、`request_id`、`connection_epoch` | 16 个安全随机字节 → 小写十六进制 | 32 字符 `^[0-9a-f]{32}$` |
| `resume_token`、`admin_token` | 32 个安全随机字节 → 无填充 Base64url | 43 字符 `^[A-Za-z0-9_-]{43}$` |
| `seq` | 十进制字符串，对应 uint64 | 最多 20 位；除 `"0"` 外无前导零 |
| `nonce_b64`、`ciphertext_b64`、`public_key_b64` | RFC 4648 标准 Base64（带 `=` 填充） | 解码后长度见密码契约 |
| `short_id` | 大写十六进制 | 4 字符，例如 `A1B2` |
| `color` | `#RRGGBB` | 7 字符 |

`seq` 必须通过 uint64 范围检查；超出返回 `INVALID_MESSAGE`。

### 1.2 严格解析

- 只接受 JSON 对象；拒绝数组、标量、`null`、非 UTF-8 和重复键。
- 拒绝未知 `type` 与未知字段；字段超过长度立即报错。
- `v != 1` 返回 `VERSION_MISMATCH`。
- 完整消息超过 64 KiB 时读取层拒绝并关闭连接（WebSocket 1009）。
- 不允许用额外信封字段绕过正文、密文或观察预算。
- 所有 `*_ms` 必须是整数；布尔字段必须是布尔值。

### 1.3 连接与来源

| 入口 | 路径 | 鉴权 |
| --- | --- | --- |
| 学生监听器 | `/ws` | `Origin` 的 host:port 必须与请求 `Host` 一致 |
| 教师监听器 | `/ws/admin` | 先通过 Origin/Host 校验，再发送 `admin.auth` |

浏览器必须带 `Origin`。缺失、`null` 或 host 不匹配时握手返回 403。默认不启用跨域。教师监听器不接受学生加入；学生监听器不注册 `/ws/admin`。

## 2. 消息族总览

| 方向 | 类型 | 作用 |
| --- | --- | --- |
| 学生→A | `join` / `resume` | 创建或恢复临时身份 |
| A→学生 | `welcome` | 返回身份、恢复 token、代次、轮次和公开快照 |
| 学生→A | `invite.create` / `invite.respond` / `invite.cancel` | 邀请、回应、取消 |
| A→双方 | `session.changed` | 会话状态和固定模式 |
| 学生→A | `key.publish` | 提交本端公钥 |
| A→对端 | `key.publish` | 转交对端公钥 |
| 学生→A | `key.confirm` | 发送加密确认 |
| A→对端 | `key.confirm` | 转交加密确认 |
| 学生→A | `key.ready` | 报告本地确认结果 |
| 学生→A | `chat.send` | 发送明文或密文 |
| A→发件人 | `chat.accepted` | A 已接受 |
| A→收件人 | `chat.deliver` | 转发已验证归属的消息 |
| 收件人→A | `chat.receipt` | 成功处理或解密失败 |
| A→发件人 | `chat.status` | 确认、未确认或失败 |
| 学生→A | `session.close` / `leave` | 关闭会话或离开 |
| 双向 | `ping` / `pong` | 连通性维护 |
| A→学生 | `topology.changed` / `activity.batch` | 公开拓扑与活动摘要 |
| 教师→A | `admin.auth` | 管理认证 |
| A→教师 | `admin.welcome` | 认证结果与当前课堂快照 |
| 教师→A | `observer.subscribe` | 订阅/切换/取消观察 |
| A→教师 | `observer.snapshot` / `observer.event` | 历史与实时观察 |
| 教师→A | `round.reset` / `class.end` / `class.start` / `server.shutdown` | 课堂控制 |
| A→教师 | `round.changed` | 幂等管理操作结果 |
| A→学生 | `round.ended` | 本轮结束或重置，回到加入页 |
| A→客户端 | `error` | 结构化错误 |

## 3. 加入与恢复

### 3.1 `join`（学生→A，加入前）

```json
{
  "v": 1,
  "type": "join",
  "request_id": "00112233445566778899aabbccddeeff",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "nickname": "小明"
}
```

| 字段 | 必填 | 校验 |
| --- | --- | --- |
| `request_id` | 是 | 32 位小写十六进制 |
| `round_id` | 是 | 必须等于当前轮次，否则 `STALE_ROUND` |
| `nickname` | 是 | 去首尾空白后 1—16 个 Unicode 码点；禁止控制字符 |

昵称允许重名。A 分配唯一 `participant_id`、`short_id` 和 `color`。

### 3.2 `resume`（学生→A，恢复前）

```json
{
  "v": 1,
  "type": "resume",
  "request_id": "11112222333344445555666677778888",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "resume_token": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}
```

`resume_token` 只能是 32 字节随机数的无填充 Base64url。恢复窗口默认 60 秒；过期返回 `RESUME_EXPIRED`。恢复成功必须关闭旧连接与旧会话，轮换 token，生成新 `connection_epoch`。

### 3.3 `welcome`（A→学生）

```json
{
  "v": 1,
  "type": "welcome",
  "request_id": "00112233445566778899aabbccddeeff",
  "participant_id": "0123456789abcdef0123456789abcdef",
  "nickname": "小明",
  "short_id": "A1B2",
  "color": "#3B82F6",
  "resume_token": "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
  "connection_epoch": "22223333444455556666777788889999",
  "round": { "id": "ffeeddccbbaa99887766554433221100", "status": "RUNNING" },
  "topology_version": 7,
  "participants": [
    {
      "id": "0123456789abcdef0123456789abcdef",
      "nickname": "小明",
      "short_id": "A1B2",
      "color": "#3B82F6",
      "online": true
    }
  ],
  "sessions": []
}
```

`resume_token` 只发给本人；不得出现在拓扑、观察、公共配置或日志中。

### 3.4 `topology.changed`（A→学生/教师）

```json
{
  "v": 1,
  "type": "topology.changed",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "topology_version": 8,
  "participants": [
    {
      "id": "0123456789abcdef0123456789abcdef",
      "nickname": "小明",
      "short_id": "A1B2",
      "color": "#3B82F6",
      "online": true
    }
  ],
  "sessions": [
    {
      "session_id": "33334444555566667777888899990000",
      "a_id": "0123456789abcdef0123456789abcdef",
      "b_id": "44445555666677778888999900001111",
      "mode": "encrypted",
      "state": "ACTIVE"
    }
  ]
}
```

公开投影只含身份、短编号、颜色、在线状态和会话结构，不含正文、密文、token、公钥或密钥。`topology_version` 单调递增；旧版本事件被客户端丢弃。A 可对高频变化做有限合并，但最新快照必须完整可重建。

## 4. 邀请与会话

### 4.1 `invite.create`（学生→A）

```json
{
  "v": 1,
  "type": "invite.create",
  "request_id": "55556666777788889999000011112222",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "target_id": "44445555666677778888999900001111",
  "mode": "encrypted"
}
```

守卫：目标在线、非自己、双方额度未满、该对参与者无未关闭会话。`mode` 只能是 `plaintext` 或 `encrypted`。成功后向双方发送 `session.changed`（`state=INVITED`）。重复或双向同时邀请不创建第二条边。

### 4.2 `invite.respond`（被邀请人→A）

```json
{
  "v": 1,
  "type": "invite.respond",
  "request_id": "66667777888899990000111122223333",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "accept": true
}
```

只有预期被邀请人能回应。明文接受 → `ACTIVE`；加密接受 → `NEGOTIATING` 并启动 15 秒计时。拒绝 → `CLOSED`，原因 `rejected`。过期返回 `INVITE_EXPIRED`。

### 4.3 `invite.cancel`（邀请方→A）

```json
{
  "v": 1,
  "type": "invite.cancel",
  "request_id": "77778888999900001111222233334444",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000"
}
```

只有邀请方在 `INVITED` 状态可取消。

### 4.4 `session.changed`（A→双方）

```json
{
  "v": 1,
  "type": "session.changed",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session": {
    "session_id": "33334444555566667777888899990000",
    "a_id": "0123456789abcdef0123456789abcdef",
    "b_id": "44445555666677778888999900001111",
    "mode": "encrypted",
    "state": "NEGOTIATING",
    "created_at_utc": "2026-10-23T02:00:00.000Z",
    "expires_at_utc": "2026-10-23T02:00:15.000Z"
  }
}
```

`a_id` 是邀请方，`b_id` 是接受方；方向固定为 `a_to_b` 或 `b_to_a`，不随当前选中者改变。`state` 为 `INVITED|NEGOTIATING|ACTIVE|CLOSED`。关闭时增加 `closed_reason`：`rejected|cancelled|invite_timeout|key_timeout|peer_offline|user|crypto_failed|slow_client|round_reset|class_ended`。

### 4.5 `session.close`（学生→A）

```json
{
  "v": 1,
  "type": "session.close",
  "request_id": "88889999000011112222333344445555",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "reason": "user"
}
```

`reason` 只允许 `user` 或 `leave`。任一方关闭后双方收到 `session.changed(CLOSED)`；重复关闭无副作用。

## 5. 加密协商

### 5.1 `key.publish`

学生→A（发布自己的公钥）：

```json
{
  "v": 1,
  "type": "key.publish",
  "request_id": "99990000111122223333444455556666",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "public_key_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
}
```

A→对端（转交，带 `sender_id`）：

```json
{
  "v": 1,
  "type": "key.publish",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "sender_id": "0123456789abcdef0123456789abcdef",
  "public_key_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
}
```

`public_key_b64` 解码后必须恰好 32 字节。只接受本会话成员在 `NEGOTIATING` 中提交；本会话公钥固定后，不同公钥返回 `CRYPTO_FAILED` 并关闭会话。

### 5.2 `key.confirm`

学生→A（加密确认）：

```json
{
  "v": 1,
  "type": "key.confirm",
  "request_id": "aaaabbbbccccddddeeeeffff00001111",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "seq": "0",
  "nonce_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "ciphertext_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
}
```

A→对端转发时结构相同，增加 `sender_id`，不改变 `seq`、`nonce_b64`、`ciphertext_b64`。加密确认不计入聊天条数、累计字节或动画。

内部确认信封（加密前，UTF-8 JSON）至少包含：

```json
{
  "v": 1,
  "kind": "key_confirm",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "session_id": "33334444555566667777888899990000",
  "sender_id": "0123456789abcdef0123456789abcdef",
  "recipient_id": "44445555666677778888999900001111",
  "seq": "0",
  "sender_public_key_b64": "<sender-32-byte-public-key>",
  "recipient_public_key_b64": "<recipient-32-byte-public-key>"
}
```

接收者必须认证密文，并校验 `round_id`、`session_id`、双方身份、方向、前缀、序号，以及内部双方公钥与本会话固定公钥一致。成功后才固定对端 nonce 前缀与接收水位。

### 5.3 `key.ready`（学生→A）

```json
{
  "v": 1,
  "type": "key.ready",
  "request_id": "bbbbccccddddeeeeffff000011112222",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "status": "ready"
}
```

`status` 为 `ready` 或 `failed`。`failed` 时 A 以 `crypto_failed` 关闭会话。Hub 收齐双方 `ready` 且未超时才发送 `session.changed(ACTIVE)`；此前不能发送聊天。

## 6. 聊天

### 6.1 `chat.send` 明文（学生→A）

```json
{
  "v": 1,
  "type": "chat.send",
  "request_id": "ccccddddeeeeffff0000111122223333",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "message_id": "dddddddddddddddddddddddddddddddd",
  "seq": "12",
  "text": "这是明文消息"
}
```

只允许 `ACTIVE` 明文会话。A 检查正文 UTF-8 字节数 ≤ 4096，不检查密文类字段。

### 6.2 `chat.send` 加密（学生→A）

```json
{
  "v": 1,
  "type": "chat.send",
  "request_id": "ddddeeeeffff00001111222233334444",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "seq": "3",
  "nonce_b64": "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "ciphertext_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
}
```

加密消息禁止出现 `text`、`plaintext`、`private_key`、`shared_secret`、明文 hash 或任何明文备份字段。A 只校验外层路由、`seq` 与 nonce 方向、密文上限、会话状态和消息预算。

内部聊天信封（加密前，UTF-8 JSON）：

```json
{
  "v": 1,
  "kind": "chat",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "sender_id": "44445555666677778888999900001111",
  "recipient_id": "0123456789abcdef0123456789abcdef",
  "seq": "3",
  "text": "这是加密消息"
}
```

### 6.3 `chat.accepted`（A→发件人）

```json
{
  "v": 1,
  "type": "chat.accepted",
  "request_id": "ddddeeeeffff00001111222233334444",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "seq": "3",
  "accepted_at_utc": "2026-10-23T02:01:00.000Z"
}
```

表示 A 已接受，不等于收件人已处理。

### 6.4 `chat.deliver`（A→收件人）

明文：

```json
{
  "v": 1,
  "type": "chat.deliver",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "message_id": "dddddddddddddddddddddddddddddddd",
  "sender_id": "44445555666677778888999900001111",
  "seq": "12",
  "text": "这是明文消息",
  "sent_at_utc": "2026-10-23T02:00:30.000Z"
}
```

加密：把 `text` 替换为 `nonce_b64` 与 `ciphertext_b64`，其余路由字段相同。收件人必须用会话密钥解密并校验内部信封；A 不提供解密入口。

### 6.5 `chat.receipt`（收件人→A）

```json
{
  "v": 1,
  "type": "chat.receipt",
  "request_id": "eeeeffff000011112222333344445555",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "status": "processed"
}
```

`status` 为 `processed` 或 `crypto_failed`。只有预期收件人可确认；明文在解析并加入本地消息后确认，加密在认证、解密、上下文校验和加入本地消息后确认。重复确认无副作用。

### 6.6 `chat.status`（A→发件人）

```json
{
  "v": 1,
  "type": "chat.status",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "status": "confirmed",
  "at_utc": "2026-10-23T02:01:00.120Z"
}
```

`status` 为 `confirmed|unconfirmed|failed`。`confirmed` 表示收件人程序报告成功处理，不是“本人已读”。

### 6.7 去重与序号

- 同一 `session_id + sender_id + message_id` 的相同重试返回已知处理结果，不重复转发或计数。
- 相同 ID 不同载荷返回 `ID_CONFLICT`。
- 明文 `seq` 每会话每发送方单调递增，可从 0 开始，允许因本地失败出现间隔，不允许倒退或重复处理。
- 加密 `seq` 必须等于 nonce 最后 8 字节计数，规则见 `docs/crypto-and-observation.md`。
- 去重记录有界；已超出窗口的旧序号不能作为新消息重新接受。

## 7. 公开活动摘要

`activity.batch`（A→学生）只含必要摘要，不含正文、密文或长度统计：

```json
{
  "v": 1,
  "type": "activity.batch",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999",
  "events": [
    {
      "session_id": "33334444555566667777888899990000",
      "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
      "sender_id": "44445555666677778888999900001111",
      "direction": "b_to_a",
      "at_utc": "2026-10-23T02:01:00.000Z"
    }
  ]
}
```

每条首次接受的业务消息只产生一次摘要。控制消息、心跳、回执、确认、转发不产生摘要。批量上限由实现设定且必须有界（≤ 32 条/批）；高频时合并显示但不改变原始统计。

## 8. 教师管理

### 8.1 `admin.auth`（教师→A）

```json
{
  "v": 1,
  "type": "admin.auth",
  "request_id": "ffff0000111122223333444455556666",
  "token": "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
}
```

必须是通过本机 fragment 传入的 32 字节 token。失败返回 `UNAUTHORIZED` 并关闭管理连接。管理身份只由教师监听器和鉴权决定，不接受任何 `role=admin` 字段。

### 8.2 `admin.welcome`（A→教师）

```json
{
  "v": 1,
  "type": "admin.welcome",
  "request_id": "ffff0000111122223333444455556666",
  "connection_epoch": "99990000111122223333444455556666",
  "server_version": "0.1.0",
  "protocol_version": 1,
  "round": { "id": "ffeeddccbbaa99887766554433221100", "status": "RUNNING" },
  "topology_version": 9,
  "participants": [],
  "sessions": []
}
```

教师不被计入学生人数，也不获得学生恢复 token。

### 8.3 `observer.subscribe`（教师→A）

订阅或切换：

```json
{
  "v": 1,
  "type": "observer.subscribe",
  "request_id": "00001111222233334444555566667777",
  "session_id": "33334444555566667777888899990000"
}
```

取消订阅：`"session_id": null`。切换订阅必须先清空旧游标，不能串记录。

### 8.4 `observer.snapshot`（A→教师）

```json
{
  "v": 1,
  "type": "observer.snapshot",
  "request_id": "00001111222233334444555566667777",
  "session_id": "33334444555566667777888899990000",
  "mode": "encrypted",
  "stats": {
    "message_count": 42,
    "payload_bytes_total": 8192,
    "ws_message_bytes_total": 13000,
    "last_direction": "b_to_a",
    "direction_switches": 7,
    "confirmed_count": 40,
    "unconfirmed_count": 1,
    "failed_count": 1,
    "first_at_utc": "2026-10-23T02:00:00.000Z",
    "last_at_utc": "2026-10-23T02:05:00.000Z"
  },
  "truncated": false,
  "messages": [
    {
      "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
      "sender_id": "44445555666677778888999900001111",
      "direction": "b_to_a",
      "mode": "encrypted",
      "payload_bytes": 128,
      "ws_message_bytes": 320,
      "at_utc": "2026-10-23T02:01:00.000Z",
      "mono_ms": 61000,
      "interval_ms": 2500,
      "receipt_status": "confirmed",
      "text": null,
      "nonce_b64": "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
      "ciphertext_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
    }
  ]
}
```

明文记录的 `text` 是 A 实际收到的正文，`ciphertext_b64` 为 `null`；加密记录的 `text` 为 `null`，密文与 nonce 是真实载荷。`receipt_status` 为 `pending|confirmed|unconfirmed|crypto_failed`。`at_utc` 是显示用 UTC；`mono_ms` 是本进程单调毫秒；`interval_ms` 按同一会话 Hub 接受顺序的相邻业务事件计算，首条为 `null`。`payload_bytes` 与 `ws_message_bytes` 口径见 A05。

### 8.5 `observer.event`（A→教师）

```json
{
  "v": 1,
  "type": "observer.event",
  "session_id": "33334444555566667777888899990000",
  "message": {
    "message_id": "ffffffffffffffffffffffffffffffff",
    "sender_id": "0123456789abcdef0123456789abcdef",
    "direction": "a_to_b",
    "mode": "encrypted",
    "payload_bytes": 96,
    "ws_message_bytes": 256,
    "at_utc": "2026-10-23T02:02:00.000Z",
    "mono_ms": 121000,
    "interval_ms": 3000,
    "receipt_status": "pending",
    "text": null,
    "nonce_b64": "AgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
    "ciphertext_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
  }
}
```

确认状态变化时，A 再发送同一 `message_id` 的 `observer.event`，带 `"update": true` 和新的 `receipt_status`。`update=true` 只更新现有记录，不新增条数、不改变原始时间、长度或累计统计。

### 8.6 课堂控制

`round.reset`：

```json
{
  "v": 1,
  "type": "round.reset",
  "request_id": "11110000111100001111000011110000",
  "expected_round_id": "ffeeddccbbaa99887766554433221100"
}
```

`class.end` 与 `class.start` 字段相同，分别结束和开始课堂。`server.shutdown` 只带 `request_id`。

`round.changed`（A→教师）：

```json
{
  "v": 1,
  "type": "round.changed",
  "request_id": "11110000111100001111000011110000",
  "round": { "id": "00000000000000000000000000000001", "status": "RUNNING" },
  "action": "reset"
}
```

`action` 为 `reset|end|start|shutdown`。同 `request_id` 同载荷重试返回原结果；改载荷返回 `ID_CONFLICT`；`expected_round_id` 不匹配返回 `STALE_ROUND`。

`round.ended`（A→学生）：

```json
{
  "v": 1,
  "type": "round.ended",
  "reason": "reset",
  "new_round_id": "00000000000000000000000000000001"
}
```

`reason` 为 `reset|class_end|shutdown`。客户端收到后清空会话密钥、聊天缓存和身份状态，回到加入页；`class_end` 时加入页显示“课堂已结束”。

## 9. 心跳与离开

`ping`（客户端→A）：`{ "v":1, "type":"ping" }`

`pong`（A→客户端）：`{ "v":1, "type":"pong", "server_time_utc":"2026-10-23T02:00:00.000Z" }`

心跳不计聊天统计。默认 15 秒一次，45 秒未收到判定失联。

`leave`（学生→A）：

```json
{
  "v": 1,
  "type": "leave",
  "request_id": "22220000222200002222000022220000",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "22223333444455556666777788889999"
}
```

离开会关闭该身份全部邀请与会话、移除身份并关闭连接，不保留恢复 token。

## 10. 错误

```json
{
  "v": 1,
  "type": "error",
  "request_id": "ccccddddeeeeffff0000111122223333",
  "session_id": "33334444555566667777888899990000",
  "code": "MESSAGE_TOO_LARGE",
  "message": "消息超过允许长度"
}
```

| 代码 | 含义 | 连接处理 |
| --- | --- | --- |
| `INVALID_MESSAGE` | JSON、字段、类型、Base64 或范围非法 | 保留；重复时关闭 |
| `VERSION_MISMATCH` | `v` 不支持 | 关闭 |
| `STALE_ROUND` | 轮次已过期 | 拒绝该命令 |
| `STALE_CONNECTION` | 连接代次不匹配 | 拒绝；不修改状态 |
| `NOT_MEMBER` | 不是会话成员 | 拒绝 |
| `NOT_ACTIVE` | 会话未激活 | 拒绝 |
| `MODE_MISMATCH` | 消息字段与会话模式不符 | 拒绝 |
| `CAPACITY_REACHED` | 人数、连接或额度已满 | 拒绝 |
| `INVITE_EXPIRED` | 邀请已超时 | 拒绝 |
| `KEY_TIMEOUT` | 加密协商超时 | 会话关闭 |
| `MESSAGE_TOO_LARGE` | 正文/密文/消息超限 | 拒绝；读取层超限则关闭 |
| `RATE_LIMITED` | 超过聊天或控制速率 | 拒绝该条 |
| `ID_CONFLICT` | 相同 ID 不同载荷 | 拒绝 |
| `CRYPTO_FAILED` | 公钥变化、认证失败或上下文不符 | 会话关闭 |
| `RESUME_EXPIRED` | 恢复窗口过期或 token 无效 | 拒绝恢复 |
| `CLASS_ENDED` | 课堂已结束 | 拒绝新加入 |
| `SLOW_CLIENT` | 写队列溢出或写超时 | 关闭该连接 |
| `UNAUTHORIZED` | 管理 token 或来源无效 | 关闭管理连接 |
| `FORBIDDEN` | 越权访问管理接口或他人会话 | 拒绝 |
| `SESSION_EXISTS` | 该对参与者已有未关闭会话 | 返回现有会话状态 |
| `INTERNAL` | 未预期内部错误 | 记录代码，不泄露敏感字段 |

错误消息不得包含正文、密文、token、私钥或共享秘密。

## 11. 可见性矩阵

| 数据 | 学生本人 | 其他学生 | 教师 | A 的业务输入 |
| --- | --- | --- | --- | --- |
| 公开昵称、短编号、颜色、在线状态 | 是 | 是 | 是 | 是 |
| 会话结构（双方、模式、状态） | 是 | 是 | 是 | 是 |
| 明文会话正文 | 参与者 | 否 | 是（观察） | 明文模式是 |
| 加密会话密文/nonce | 参与者 | 否 | 是（观察） | 加密模式是 |
| 加密会话明文/私钥 | 仅本端 | 否 | 否 | 否 |
| 恢复 token | 本人 | 否 | 否 | 是（用于恢复） |
| 管理 token | 否 | 否 | 教师页内存 | 是（用于鉴权） |
| 观察统计与历史 | 否 | 否 | 是 | 是 |

公共广播不得“先广播全部明文再由前端隐藏”。教师观察正文只经 `127.0.0.1` 管理连接传输。

## 12. 异常样例

### 12.1 未知版本

```json
{ "v": 2, "type": "join", "request_id": "00112233445566778899aabbccddeeff", "round_id": "ffeeddccbbaa99887766554433221100", "nickname": "小明" }
```

响应：`error(code="VERSION_MISMATCH")`，随后关闭连接。

### 12.2 加密消息夹带明文

```json
{
  "v": 1,
  "type": "chat.send",
  "request_id": "ddddeeeeffff00001111222233334444",
  "round_id": "ffeeddccbbaa99887766554433221100",
  "connection_epoch": "44445555666677778888999900001111",
  "session_id": "33334444555566667777888899990000",
  "message_id": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "seq": "3",
  "nonce_b64": "AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "ciphertext_b64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
  "text": "不应出现的明文"
}
```

响应：`error(code="INVALID_MESSAGE")`；不转发、不生成观察事件。不得降级为明文。

### 12.3 非成员发送

对不属于自己的 `session_id` 发送 `chat.send` 返回 `NOT_MEMBER`，不泄露会话是否存在。

### 12.4 相同消息 ID 改载荷

相同 `session_id + sender_id + message_id` 但 `text`/`ciphertext_b64` 不同，返回 `ID_CONFLICT`，不重复转发或计数。

### 12.5 超限正文

正文 UTF-8 字节数 4097（4096 合法）返回 `MESSAGE_TOO_LARGE`；不转发、不计数。多字节中文和 emoji 必须按字节而非字符计数。

### 12.6 失效恢复

超过 60 秒使用旧 `resume_token` 返回 `RESUME_EXPIRED`，客户端必须回到昵称加入页。

## 13. 协议修改规则

协议修改必须同时更新：Go 类型、TypeScript 类型、本文档、`docs/crypto-and-observation.md`（如涉及）和受影响的验收样例。禁止只改一端。未冻结字段不得在正式界面冒充实测数据。
