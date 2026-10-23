# 第三方组件与许可

本文件随发布包提供。项目自身代码的许可由仓库维护者另行决定；以下仅列出运行期与构建期使用的第三方组件。

## 运行期组件

| 组件 | 版本 | 用途 | 许可 |
| --- | --- | --- | --- |
| github.com/coder/websocket | v1.8.13 | WebSocket 服务端 | ISC 风格（见 `licenses/coder-websocket-LICENSE.txt`） |
| TweetNaCl.js | 1.0.3 | 浏览器端 X25519/XSalsa20-Poly1305 | Unlicense（见 `licenses/tweetnacl-LICENSE.txt`） |
| qrcode | 1.5.4 | 本地生成学生入口二维码 | MIT（见 `licenses/qrcode-LICENSE.txt`） |
| Go 标准库 | 1.22.6 | 服务端运行时 | BSD-3-Clause（见 `licenses/go-LICENSE.txt`） |

## 构建期组件

以下组件只在开发构建机使用，不随 EXE 分发：

| 组件 | 版本 | 许可 |
| --- | --- | --- |
| Vite | 7.3.7 | MIT（见 `licenses/vite-LICENSE.md`） |
| TypeScript | 5.9.3 | Apache-2.0（见 `licenses/typescript-LICENSE.txt`） |
| Vitest | 5.0.5 | MIT（见 `licenses/vitest-LICENSE.md`） |
| @types/qrcode | 1.5.6 | MIT（见 `licenses/types-qrcode-LICENSE.txt`） |

## 版本与校验

实际构建的版本、提交、构建时间和 EXE SHA-256 记录在发布目录的 `VERSION.txt` 与 `SHA256SUMS.txt`。
