# 密码载荷与观察指标契约

- 文档版本：1.0
- 冻结日期：2026-10-23
- 状态：A05 契约文档
- 密码原语：本地打包的 TweetNaCl.js `nacl.box`（X25519 + XSalsa20-Poly1305）
- 观察口径：A 实际收到的应用消息，不是网卡抓包

## 1. 密码原语与能力边界

| 项目 | 约定 |
| --- | --- |
| 密钥交换 | `nacl.box.keyPair()` 生成临时 X25519 密钥对 |
| 加密 | `nacl.box` / `nacl.box.after`，24 字节 nonce，16 字节认证开销 |
| 公钥长度 | 32 字节 |
| 私钥长度 | 32 字节，只在浏览器内存 |
| 共享秘密 | 32 字节，由 `nacl.box.before(peerPublicKey, ownSecretKey)` 得到，只在浏览器内存 |
| 随机数 | Go `crypto/rand`；浏览器 `crypto.getRandomValues` |
| 编码 | 公钥、nonce、密文用标准 Base64；token 用无填充 Base64url |

浏览器在普通 LAN HTTP 下不是安全上下文，不能依赖 `crypto.subtle`。若 `crypto.getRandomValues` 缺失或被替换，必须明确报错并禁用加密能力；不得使用 `Math.random` 代替。

密码库只负责原语，不负责协议。nonce 构造、方向、序号、上下文绑定和生命周期由本项目实现。

## 2. 密钥生命周期

```text
邀请接受(encrypted) -> NEGOTIATING
  两端各自 keyPair() 生成临时密钥对
  学生 -> A: key.publish(own public key)
  A -> 对端: key.publish(peer public key, sender_id)
  两端计算 sharedKey = box.before(peerPublic, ownSecret)
  两端发送 key.confirm（加密确认）
  接收方认证并校验上下文 -> 固定对端公钥、方向、前缀
  两端上报 key.ready
  A 收齐两端 ready -> ACTIVE
  任一方关闭/断线/重置 -> 销毁私钥、共享秘密、nonce 状态和消息缓存
```

规则：

1. 每个加密会话使用新的临时密钥对，不复用其他会话或其他轮次的密钥。
2. A 只转交公钥、转发密文；不接收私钥或共享秘密，不提供服务端解密入口。
3. 双方公钥在本会话固定。已固定后收到不同公钥，关闭会话并以 `CRYPTO_FAILED` 通知，不静默替换。
4. `key.confirm` 成功解密并校验后，才固定对端 nonce 前缀与接收水位；此前不能发送聊天。
5. `key.ready` 只是客户端报告本地验证结果；Hub 不声称验证了秘密。
6. 关闭时对可控字节数组执行 `fill(0)` 并释放引用；JavaScript 垃圾回收时机不做保证，但不得主动保留旧密钥引用。
7. 私钥、共享秘密、确认明文不得进入可序列化公开状态、上传路径、日志或错误消息。

## 3. nonce 构造

nonce 固定 24 字节：

```text
字节 0        : 方向标记，邀请方 = 0x00，接受方 = 0x01
字节 1..15    : 15 字节安全随机前缀，由每个发送方创建会话时生成
字节 16..23   : 8 字节大端无符号计数
```

```text
nonce[0]    = direction
nonce[1:16] = senderNoncePrefix (15 random bytes)
nonce[16:24]= uint64 big-endian(counter)
```

- 邀请方方向为 0，接受方方向为 1；每条加密消息的方向字节必须是发送方角色对应的值。
- 每个发送方拥有自己的 15 字节前缀；一方不能使用对端前缀。
- 计数从 0 开始。`key.confirm` 与聊天共用本方向计数；`key.confirm` 占用 0，首条聊天使用 1。
- 每次加密先分配当前计数、再递增；即使构造或发送失败也不回退。
- 计数达到 `2^64-1` 并已使用后关闭会话；不得回绕。
- 浏览器使用 `BigInt` 维护计数；协议外层 `seq` 与内部信封 `seq` 都是计数的规范十进制字符串：除 `"0"` 外无前导零，范围 0—18446744073709551615。
- 外层 `seq` 必须等于 nonce 最后 8 字节计数的十进制表示；不相等即拒绝。
- 接收方在 `key.confirm` 成功后固定对端方向与前缀，并将“下一条聊天计数”水位设为 1。
- 后续消息必须：方向一致、前缀一致、外层 `seq` 与 nonce 计数一致、计数严格大于上次成功接收的计数。重复或倒退一律拒绝。
- 只有认证、解密、上下文和正文长度检查全部通过后，才推进接收水位。

方向由固定双方定义：`a_to_b` 的发送方是邀请方（方向字节 0），`b_to_a` 的发送方是接受方（方向字节 1）。它不随教师或学生当前选中者改变。

## 4. 加密确认 `key.confirm`

加密前内部信封（UTF-8 JSON，严格字段）：

```json
{
  "v": 1,
  "kind": "key_confirm",
  "round_id": "<current-round>",
  "session_id": "<current-session>",
  "sender_id": "<sender>",
  "recipient_id": "<recipient>",
  "seq": "0",
  "sender_public_key_b64": "<sender-32-byte-public-key>",
  "recipient_public_key_b64": "<recipient-32-byte-public-key>"
}
```

- 使用 `nacl.box.after(internalEnvelopeBytes, nonce, sharedKey)` 加密。
- 外层 `key.confirm` 只带 `session_id`、`seq`、`nonce_b64`、`ciphertext_b64` 和常规信封字段；A 转发时增加 `sender_id`。
- 接收方认证并解密后，必须逐项校验：
  1. 内部 `v`、`kind`、`round_id`、`session_id` 与外层及当前状态一致。
  2. `sender_id` 是对端，`recipient_id` 是自己。
  3. `seq` 为 `"0"`，且等于 nonce 计数 0。
  4. 方向字节与对端角色一致，前缀与对端声明一致。
  5. `sender_public_key_b64` 等于对端已发布公钥；`recipient_public_key_b64` 等于自己的公钥。
  6. 认证标签有效。
- 任一失败：不固定任何状态，发送 `key.ready(failed)` 或关闭会话，原因 `crypto_failed`。
- 确认消息不计入聊天条数、累计字节、消息动画或活动摘要。

## 5. 加密聊天信封

加密前内部信封（UTF-8 JSON，严格字段）：

```json
{
  "v": 1,
  "kind": "chat",
  "round_id": "<current-round>",
  "session_id": "<current-session>",
  "message_id": "<random-32-hex>",
  "sender_id": "<sender>",
  "recipient_id": "<recipient>",
  "seq": "<nonce-counter-decimal>",
  "text": "<student-input>"
}
```

- 外层 `chat.send` 只包含路由与版本字段、`message_id`、`seq`、`nonce_b64`、`ciphertext_b64`。
- 外层禁止出现 `text`、`plaintext`、`private_key`、`shared_secret`、明文 hash 或明文备份。出现即 `INVALID_MESSAGE`，不转发、不降级。
- A 不能检查加密正文长度，只检查密文、外层字段和完整应用消息预算。
- 接收方解密后强制检查 `text` 的 UTF-8 字节数 ≤ 4096；超限按失败处理，不显示、不确认。
- 密文长度等于“整个内部信封的 UTF-8 字节数 + 16 字节认证开销”，不是用户文字字节数 + 16。

## 6. 接收端解密校验顺序

对每条加密业务消息，接收方严格按以下顺序处理：

1. 外层信封：`v`、`type`、字段集合、`round_id`、`connection_epoch`、`session_id`、`message_id`、`seq`。
2. 会话：成员、`ACTIVE`、模式为 `encrypted`。
3. Base64：`nonce_b64` 解码恰好 24 字节；`ciphertext_b64` 解码 16—32768 字节。
4. nonce：方向字节符合对端角色；前缀等于已固定对端前缀；计数与 `seq` 一致；计数严格大于接收水位。
5. 认证解密：使用本会话共享秘密和 nonce 解密；失败不泄漏内部内容。
6. 内部信封：严格 JSON；`v`、`kind`、`round_id`、`session_id`、`message_id`、`sender_id`、`recipient_id`、`seq` 与外层及当前会话一致。
7. 正文：`text` 是字符串，UTF-8 字节数 ≤ 4096。
8. 去重：同一 `message_id` 同载荷返回已知结果；不同载荷 `ID_CONFLICT`。
9. 全部通过后：加入本地消息、推进接收水位、生成 `chat.receipt(processed)`。
10. 任一失败：不显示、不推进水位、以 `crypto_failed` 关闭会话；不回退到明文，不静默替换密钥。

`key.confirm` 的校验使用相同原则，但只接受计数 0，并在成功后把下一聊天计数设为 1。

## 7. 观察事件生成

观察事件在 A **接受**一条业务消息的位置生成，且每条首次接受的业务消息只生成一次。

不生成观察事件的内容：

- 心跳 `ping`/`pong`
- 邀请、接受、拒绝、取消、关闭
- 公钥发布、加密确认、`key.ready`
- `chat.accepted`、`chat.deliver`、`chat.receipt`、`chat.status`
- 拓扑、活动摘要、管理消息
- 重复消息重试（返回原结果）
- 被拒绝的消息

观察事件字段：

| 字段 | 含义 |
| --- | --- |
| `message_id` | 业务消息 ID |
| `sender_id` | A 依据真实连接确定的发送方 |
| `direction` | `a_to_b` 或 `b_to_a`，固定双方定义 |
| `mode` | `plaintext` 或 `encrypted` |
| `payload_bytes` | 明文为正文 UTF-8 字节数；加密为 Base64 解码后的密文本体字节数 |
| `ws_message_bytes` | A 实际收到的完整 WebSocket 应用消息 UTF-8 字节数 |
| `at_utc` | 显示用 UTC 时间，RFC 3339 毫秒 |
| `mono_ms` | 本进程单调时钟毫秒，进程启动为 0 |
| `interval_ms` | 同会话相邻业务事件的间隔；首条为 `null` |
| `receipt_status` | `pending`、`confirmed`、`unconfirmed`、`crypto_failed` |
| `text` | 明文模式为真实正文；加密模式为 `null` |
| `nonce_b64` | 加密模式为真实 nonce；明文为 `null` |
| `ciphertext_b64` | 加密模式为真实密文；明文为 `null` |

### 7.1 两种长度口径

`payload_bytes`：

- 明文：`TextEncoder().encode(text).length`
- 加密：`ciphertext_b64` 解码后的字节数
- 不是用户字符数，也不是 IP/TCP 包长

`ws_message_bytes`：

- A 收到的完整 WebSocket 文本应用消息的 UTF-8 字节数
- 包含 JSON 字段名、ID、Base64、转义和信封开销
- 不包含 WebSocket 帧头、TCP/IP 头或链路层开销

因此：

```text
加密消息 ws_message_bytes ≫ Base64(ciphertext) ≫ payload_bytes
明文消息 ws_message_bytes ≥ payload_bytes（JSON 转义时更大）
```

示例（仅说明关系，具体值取决于实际 JSON）：

- 明文正文 `"你好"`：`payload_bytes = 6`；`ws_message_bytes` 包含完整 JSON，通常明显大于 6。
- 加密正文 `"你好"`：`payload_bytes` 是整个内部信封加密后的字节数，通常大于 6；`ws_message_bytes` 还包含 Base64 膨胀与字段名。

界面必须分别显示两种长度，不能只显示一个“消息大小”。

### 7.2 时间与间隔

- `at_utc` 只用于显示；不同手机时钟不参与计算。
- `interval_ms` 使用服务端同一进程的单调时钟，在 Hub 接受顺序上计算。
- 首条业务事件 `interval_ms = null`，不显示为 0。
- 历史淘汰不影响后续间隔的计算基准：保留“最近一次接受事件”的单调时间，即使该事件记录已被淘汰。
- 渲染节流（≤ 100 ms 合并）只影响绘制，不改变原始事件时间或统计。

### 7.3 累计统计

每个会话独立保存，且不随历史淘汰回退：

- `message_count`：首次接受的业务消息数
- `payload_bytes_total`
- `ws_message_bytes_total`
- `last_direction`
- `direction_switches`：相邻业务事件方向变化的次数；首条不计
- `confirmed_count`、`unconfirmed_count`、`failed_count`
- `first_at_utc`、`last_at_utc`

统计按 Hub 接受顺序更新。控制消息、去重重试和被拒绝消息不增加任何计数。

## 8. 有界历史

| 范围 | 上限 | 淘汰规则 |
| --- | --- | --- |
| 单会话观察历史 | 最近 200 条，或正文/密文累计 1 MiB | 任一达到即淘汰最旧记录 |
| 全局观察历史 | 原始正文与密文累计 32 MiB | 淘汰最旧记录；保留累计统计并标记截断 |
| 已关闭会话元数据 | 200 个或 10 分钟 | 先满足者淘汰；活跃会话另受额度限制 |

- 单会话字节预算按保留记录的原始内容计：明文 `len(text)`，加密 `len(ciphertext)`；不把 WS 信封和 Base64 重复计入。
- 全局预算同样按原始正文/密文内容计。
- 淘汰后统计保持不变；界面显示 `truncated=true`，并说明当前只展示保留窗口。
- 时间轴只显示当前窗口；不得把已淘汰区间画成零值或无事件。
- 轮次重置清空全部观察历史与统计；管理幂等缓存除外。

## 9. 确认与教师显示

- `chat.receipt(processed)` 表示接收端程序已成功处理。
- 明文：解析并加入本地消息后确认。
- 加密：认证、解密、上下文校验、正文长度检查和加入本地消息后确认。
- A 向发件人发送 `chat.status(confirmed)`；5 秒无确认则 `unconfirmed`，迟到确认可更新。
- 教师观察中的 `receipt_status` 随确认事件更新，但不增加消息条数或字节统计。
- 界面写“接收端已确认”，不能写“本人已读”；A 不声称独立验证了解密。

## 10. 隐私与日志

- A 的业务输入只保存观察所需的真实载荷：明文正文或加密密文/nonce。
- 加密会话的明文、私钥、共享秘密绝不进入 A 的业务输入、教师接口、公共广播或默认日志。
- 日志只保留代码、计数、版本等必要信息；不记录正文、密文、token、密钥。
- 观察历史只在内存中，不写数据库、文件、localStorage、sessionStorage 或 IndexedDB。
- 教师观察接口只对 loopback 管理连接开放；学生无法订阅。
- 页面中的昵称和正文按纯文本渲染。

## 11. 负例要求

以下情况必须明确失败，不崩溃、不降级：

- nonce 长度不是 24 字节
- 方向字节错误，或使用对端前缀
- 外层 `seq` 与 nonce 计数不一致
- `seq` 非规范十进制、带前导零或超出 uint64
- 计数重复或倒退
- 密文被篡改、认证失败
- 内部信封上下文不匹配
- 明文夹带 `text`/`private_key` 等禁止字段
- 密文超过 32 KiB 或完整消息超过 64 KiB
- 解密正文超过 4096 UTF-8 字节
- 已固定公钥被替换
- `key.confirm` 在非 `NEGOTIATING` 状态到达
- 会话关闭后仍收到旧 `session_id` 消息

加密失败必须关闭相关会话并给出原因，不能继续使用旧密钥，也不能改发明文。
