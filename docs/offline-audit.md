# 离线资源审计（I05）

- 日期：2026-10-23
- 构建：`0.1.0-dev`，见 `docs/progress.md`
- 目标：确认运行期不依赖外网脚本、字体、二维码 API、运行时安装或遥测。

## 1. 运行期资源

| 资源 | 来源 | 结果 |
| --- | --- | --- |
| 学生 HTML/CSS/JS | `go:embed` 内嵌 | 本地 |
| 教师 HTML/CSS/JS | `go:embed` 内嵌 | 本地 |
| TweetNaCl.js | `web/package-lock.json` 打包进学生 JS | 本地 |
| qrcode | `web/package-lock.json` 打包进教师 JS | 本地 |
| 字体 | CSS 系统字体栈（Segoe UI / Microsoft YaHei / system-ui） | 不请求远程字体 |
| 图标 | 内联 data URI SVG | 本地 |
| 二维码 | 浏览器本地生成，不调用在线 API | 本地 |
| 网络请求 | `fetch`/WebSocket 仅同源学生或教师入口 | 本地 |
| 遥测/统计 | 无 | 无 |

## 2. 源码扫描

对 `web/src` 运行期源码（排除测试）与两个 HTML 扫描 `http://` / `https://`：

- `web/src/graph/graph.ts`：SVG 命名空间 `http://www.w3.org/2000/svg`，不产生网络请求。
- `web/src/shared/url.ts`：构造 LAN `http://<ip>:<port>/`，用于二维码和复制。
- `web/index.html`、`web/teacher.html`：内联 data URI favicon 中的 SVG 命名空间。

未发现远程脚本、样式、字体、图片、二维码 API 或遥测地址。

## 3. 内容安全策略

内嵌页面由 `internal/web` 设置：

```text
default-src 'self';
script-src 'self';
style-src 'self' 'unsafe-inline';
img-src 'self' data:;
connect-src 'self' ws: wss:;
base-uri 'none';
frame-ancestors 'none';
form-action 'self'
```

`script-src` 不允许远程脚本；`img-src` 仅允许同源与内联图片；连接只允许同源 HTTP 与 WebSocket。

## 4. 验证方式

```powershell
# 扫描运行期前端源码
Get-ChildItem web/src -Recurse -Include *.ts,*.css |
  Where-Object { $_.FullName -notmatch '\.test\.' } |
  Select-String -Pattern 'https?://'

# 检查 HTML
Select-String -Path web/index.html,web/teacher.html -Pattern 'https?://'
```

离线页面加载的最终验收在 J07 执行：保留 LAN、阻断 WAN 后重新打开页面，验证二维码、明文、加密和拓扑仍可用。本审计是静态检查，不替代 J07 的浏览器实测。
