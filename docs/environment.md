# 开发、构建与运行环境

- 文档版本：1.0
- 冻结日期：2026-10-23
- 状态：A02 契约文档
- 原则：只把真实检查过的环境写成“已知”；未检查项一律标“待确认”。

## 1. 开发机已知环境

以下为本仓库当前开发机的实测结果（2026-10-23）：

| 项目 | 实测值 | 说明 |
| --- | --- | --- |
| 操作系统 | Windows 10 Pro 10.0.19045 x64 | 中文区域设置 `zh-CN` |
| Shell | PowerShell 7.6.8 | 构建脚本以 `pwsh` 为目标 |
| Go | go1.22.6 windows/amd64 | 用于编译单文件 EXE |
| Node.js | v24.14.1 | 仅构建前端，不随发布包分发 |
| npm | 11.11.0 | 使用 `package-lock.json` 锁定依赖 |
| Git | 2.44.0.windows.1 | 版本管理 |
| 开发机 LAN 地址 | `192.168.5.5/24`（WLAN） | 仅代表当前网络，不是课堂地址 |

开发机同时存在多个 `169.254.*` 链路本地地址和虚拟网卡。程序不能把这类地址当作默认课堂入口；地址选择必须排除 loopback 并允许教师显式更换。

### 1.1 已锁定依赖版本

| 依赖 | 锁定版本 | 锁定文件 |
| --- | --- | --- |
| Go | 1.22.6（`GOTOOLCHAIN=local`） | `go.mod` |
| coder/websocket | v1.8.13 | `go.mod`、`go.sum` |
| Vite | 7.3.7 | `web/package-lock.json` |
| TypeScript | 5.9.3 | `web/package-lock.json` |
| Vitest | 5.0.5 | `web/package-lock.json` |
| TweetNaCl.js | 1.0.3 | `web/package-lock.json` |
| qrcode | 1.5.4 / @types/qrcode 1.5.6 | `web/package-lock.json` |

不使用 `latest`。构建脚本先执行 `npm ci`，再执行 Go 构建；`GOTOOLCHAIN=local` 防止构建时隐式下载新工具链。

## 2. 尚未验证的环境（保持待确认）

| 项目 | 状态 | 验证方式 |
| --- | --- | --- |
| Windows 11 x64 | 待确认 | 在目标版本双击启动并完成手机访问 |
| 教室电脑 CPU/内存/权限 | 待确认 | 现场记录；不允许依赖管理员安装 |
| 实际教室 Wi-Fi 与电脑接入方式 | 待确认 | 两部手机访问学生入口 |
| 校园网客户端隔离 / AP 隔离 | 待确认 | 手机能否访问电脑 LAN IPv4 |
| Windows 防火墙入站策略 | 待确认 | 只为明确端口放行；不关闭整个防火墙 |
| Android 机型与浏览器版本 | 待确认 | 真机兼容矩阵 |
| iPhone 机型与浏览器版本 | 待确认 | 真机兼容矩阵 |
| 实际 U 盘盘符、中文或空格路径 | 待确认 | 从该路径启动 EXE |

上述任一未验证项都不能在发布说明中写成“支持”。发布版本只声明实际验证过的系统与浏览器版本。

## 3. 运行期依赖边界

- 目标电脑只需：Windows x64、已有浏览器、可访问的 LAN。
- 不需要：Go、Node.js、Python、数据库、抓包驱动、二维码在线 API、外网。
- 前端所有必需资源（脚本、样式、字体、二维码库、密码库）随 EXE 内嵌提供。
- 运行期不下载远程资源，不使用 Service Worker 缓存课堂状态。
- 聊天、身份凭据和密钥不写入磁盘；程序只读自身嵌入资源。

## 4. 监听器与入口

| 入口 | 默认监听 | 作用 | 安全约束 |
| --- | --- | --- | --- |
| 学生入口 | `0.0.0.0:8080` | 学生页面、`/api/config`、`/healthz`、`/ws` | 不注册管理接口；不返回私密字段 |
| 教师入口 | `127.0.0.1:8081` | 教师页面、`/ws/admin` | 固定 loopback；违反时启动失败 |

端口冲突时显示原因和显式改端口方式，不静默切换端口。学生二维码和复制按钮使用选定的真实 LAN IPv4 加学生端口，绝不显示 `0.0.0.0` 或 `localhost`。

## 5. 配置结构与优先级

配置来源只有两级：

1. 内置默认值（本文件与 `docs/config.schema.json`）。
2. 启动参数显式覆盖。

不从外网、注册表、用户目录或运行期可变文件加载配置。启动参数至少包括：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--student-addr` | `0.0.0.0:8080` | 学生监听地址 |
| `--teacher-addr` | `127.0.0.1:8081` | 教师监听地址；必须是 loopback |
| `--iface-ip` | 自动选择 | 指定展示/二维码使用的 LAN IPv4 |
| `--no-open` | `false` | 不自动打开教师页，便于自动化验证 |
| `--version` | - | 打印版本与协议版本后退出 |

`--iface-ip` 必须属于本机非 loopback、非链路本地候选地址；无效值启动失败并列出候选。教师监听地址若非 loopback，直接拒绝启动。

### 5.1 配置字段

| 字段 | 默认值 | 范围/约束 | 用途 |
| --- | --- | --- | --- |
| `student_addr` | `0.0.0.0:8080` | 合法 `host:port` | 学生监听器 |
| `teacher_addr` | `127.0.0.1:8081` | 必须 loopback | 教师监听器 |
| `iface_ip` | 自动 | 本机非 loopback IPv4 | 二维码与复制地址 |
| `planned_students` | 50 | 1—100 | 班级规划展示值，不作为准入上限 |
| `max_participants` | 100 | 1—200 | 在线参与者实际上限 |
| `max_raw_connections` | 120 | ≥ `max_participants` | 未加入连接也计数 |
| `max_identities` | 200 | ≥ `max_participants` | 在线 + 离线身份 |
| `max_teachers` | 4 | 1—16 | 教师连接配额 |
| `max_sessions_per_participant` | 3 | 1—10 | INVITED/NEGOTIATING/ACTIVE 合计 |
| `max_nickname_runes` | 16 | 1—64 | 去除首尾空白后的 Unicode 码点 |
| `invite_timeout` | 30s | 5s—5m | 邀请有效期 |
| `key_timeout` | 15s | 5s—2m | 加密协商有效期 |
| `heartbeat_interval` | 15s | 5s—60s | 应用心跳 |
| `dead_timeout` | 45s | > 心跳 | 失联判定 |
| `resume_window` | 60s | 10s—5m | 同页身份恢复 |
| `ack_timeout` | 5s | 1s—30s | 正常网络回执超时 |
| `write_timeout` | 5s | 1s—30s | 单次 WebSocket 写超时 |
| `join_timeout` | 10s | 3s—60s | 未加入或未认证连接释放 |
| `control_rate_per_sec` | 10 | 1—100 | 每连接控制消息速率 |
| `control_burst` | 20 | 1—200 | 控制消息短时突发 |
| `max_body_bytes` | 4096 | 1—65536 | 用户正文 UTF-8 字节数 |
| `max_ciphertext_bytes` | 32768 | ≥ 4096 | Base64 解码后的密文体 |
| `max_ws_message_bytes` | 65536 | ≤ 1 MiB | 完整 WebSocket 应用消息 |
| `session_history_count` | 200 | 1—2000 | 单会话观察条数 |
| `session_history_bytes` | 1 MiB | ≤ 8 MiB | 单会话观察字节 |
| `global_history_bytes` | 32 MiB | ≤ 256 MiB | 全局观察字节 |
| `closed_session_max` | 200 | 1—2000 | 已关闭会话元数据 |
| `closed_session_ttl` | 10m | 1m—1h | 已关闭会话保留时间 |
| `send_queue_count` | 64 | 1—1024 | 单连接待写条数 |
| `send_queue_bytes` | 256 KiB | ≤ 4 MiB | 单连接待写字节 |
| `chat_rate_per_sec` | 2 | 1—20 | 每人聊天速率 |
| `chat_burst` | 5 | ≥ 1 | 短时突发 |
| `client_history_per_session` | 200 | 1—2000 | 客户端每会话缓存 |
| `client_cache_bytes` | 3 MiB | ≤ 16 MiB | 客户端每页面缓存 |
| `render_interval` | 100 ms | 16—1000 ms | 活动渲染合并 |

所有“字节”统一按字节计数，不按字符或码点。所有时长在协议中可用毫秒整数或 `time` 字符串传输；公开配置使用毫秒整数，内部 Go 配置使用 `time.Duration`。

### 5.2 公开配置投影

`GET /api/config` 只返回协议版本、轮次状态、公开限额和能力，不返回 token、私钥、会话正文、观察记录或教师地址：

```json
{
  "v": 1,
  "protocol_version": 1,
  "round": { "id": "<32-hex>", "status": "RUNNING" },
  "limits": {
    "max_nickname_runes": 16,
    "max_body_bytes": 4096,
    "max_ciphertext_bytes": 32768,
    "max_sessions_per_participant": 3,
    "invite_timeout_ms": 30000,
    "key_timeout_ms": 15000,
    "ack_timeout_ms": 5000,
    "heartbeat_ms": 15000,
    "render_interval_ms": 100,
    "client_history_per_session": 200,
    "client_cache_bytes": 3145728
  },
  "capabilities": { "plaintext": true, "encryption": true, "resume": true }
}
```

前端不得散落硬编码限额；需要时必须从该投影读取适用值。

## 6. 构建环境与命令约定

- Go 构建目标：`GOOS=windows GOARCH=amd64`，先构建前端再编译，产物内嵌全部资源。
- 前端构建：Vite 生产构建，TypeScript 严格模式；依赖通过锁文件安装。
- 可执行文件与资源不依赖当前工作目录。
- 依赖版本以 `go.mod`、`go.sum`、`package-lock.json` 为准，不使用 `latest`。
- 发布前记录实际依赖版本、第三方许可和 EXE SHA-256。

计划中的构建命令（B01/B02 后落地）：

```powershell
npm --prefix web ci
npm --prefix web run build
go build -trimpath -o bin/traffic-classroom.exe ./cmd/traffic-classroom
```

## 7. 已知风险

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| 校园网 AP 隔离 | 手机无法访问电脑 | 现场验证；不把本机自检当远端可达 |
| Windows 防火墙未放行 | 手机超时 | 只放行指定端口；记录操作 |
| 多网卡/虚拟网卡 | 二维码指向错误地址 | 排除 loopback/链路本地；允许显式选择 |
| LAN HTTP 非安全上下文 | `crypto.subtle` 不可用 | 使用本地打包的 TweetNaCl.js |
| 手机后台冻结 | WebSocket 断开、消息不发 | 明确断线状态；同页恢复只恢复身份 |
| 仅测试 Windows 10 | 不能宣称支持 Windows 11 | 发布说明按实测版本声明 |
