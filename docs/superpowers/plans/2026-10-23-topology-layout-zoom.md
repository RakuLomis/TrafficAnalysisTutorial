# 拓扑黄金角布局与缩放实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 3 个及以上节点的会话边不共线、可分别点击，并为学生/教师拓扑增加可读的字号、缩放、平移、全屏和点击聚焦。

**Architecture:** `layout.ts` 生成固定黄金角同心环槽位并稳定分配；`zoom.ts` 提供纯函数变换；`GraphView` 用 SVG viewport 组承载缩放/平移、工具栏、全屏、语义标签和边热区；学生/教师入口在选中会话时调用聚焦。

**Tech Stack:** TypeScript 5.9、原生 SVG/DOM、Vitest 5、Vite 7、Playwright（人工 E2E 验证）。

**Spec:** `docs/superpowers/specs/2026-10-23-topology-layout-zoom-design.md`

## Global Constraints

- 不新增运行期依赖；不改变 WebSocket 协议、Hub 或观察统计。
- 画布保持 `viewBox="0 0 1000 1000"`；最多 100 个在线节点。
- 新节点不得移动已有节点；刷新/重渲染不随机跳动。
- 学 生只能选择自己的会话边；教师可选择任意边。
- 缩放范围 0.6—3.0；全屏可 ESC 退出。
- 标签、按钮和边热区必须兼容 360px 学生页和教师投屏。
- 遵循 `prefers-reduced-motion`；所有文本继续用 `textContent`。

## Review Focus

- 三节点两两相连时三条边重合：必须用非共线槽位与宽点击热区解决，并由布局测试和教师 E2E 覆盖。
- 缩放锚点漂移/平移丢失全图：用 `zoomAt`/`clampPan` 单元测试和生活化 E2E 覆盖。
- 100 节点标签互相遮挡：用 `labels-minimal` 阈值和缩放后恢复标签覆盖。
- 手机/全屏交互：按钮与 ESC、360px 布局、无横向溢出必须回归。
- 现有明文/加密/教师观察/方向脉冲：重构 GraphView 后必须跑原有 E2E。

---

### Task 1: 黄金角稳定槽位

**Files:**
- Modify: `web/src/graph/layout.ts`
- Modify: `web/src/graph/layout.test.ts`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `GRAPH_WIDTH: 1000`, `GRAPH_HEIGHT: 1000`, `GRAPH_MARGIN: 70`
  - `goldenAngleSlots(): Point[]`（100 个固定槽位）
  - `assignNodePositions(existing: Map<string, Point>, ids: string[]): Map<string, Point>`

- [ ] **Step 1: 写失败测试**

在 `layout.test.ts` 增加：

```ts
it('spreads the first three nodes without collinearity', () => {
  const positions = assignNodePositions(new Map(), ['a', 'b', 'c'])
  const [a, b, c] = ['a', 'b', 'c'].map((id) => positions.get(id)!)
  expect(Math.abs((b.x - a.x) * (c.y - a.y) - (b.y - a.y) * (c.x - a.x))).toBeGreaterThan(1000)
})

it('creates 100 unique slots with reasonable spacing', () => {
  const slots = goldenAngleSlots()
  expect(slots).toHaveLength(100)
  const keys = new Set(slots.map((p) => `${Math.round(p.x)}:${Math.round(p.y)}`))
  expect(keys.size).toBe(100)
  for (let i = 0; i < slots.length; i++) {
    for (let j = i + 1; j < slots.length; j++) {
      expect(Math.hypot(slots[i].x - slots[j].x, slots[i].y - slots[j].y)).toBeGreaterThan(70)
    }
  }
})

it('is deterministic and preserves existing node positions', () => {
  const first = assignNodePositions(new Map(), ['a', 'b'])
  const second = assignNodePositions(first, ['a', 'b', 'c'])
  expect(second.get('a')).toEqual(first.get('a'))
  expect(second.get('b')).toEqual(first.get('b'))
  const again = assignNodePositions(new Map(), ['a', 'b'])
  expect(again).toEqual(first)
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm --prefix web test -- --run src/graph/layout.test.ts`
Expected: FAIL，`goldenAngleSlots` 未定义或旧布局共线/槽位不足。

- [ ] **Step 3: 实现槽位**

在 `layout.ts`：

- 保留 `Point`、`GRAPH_WIDTH`、`GRAPH_HEIGHT`、`GRAPH_MARGIN`。
- 删除 `GRAPH_COLUMNS`/`GRAPH_ROWS` 行优先 `slotPoint`（或仅保留内部使用）。
- 定义环：中心 `(500,500)`，半径/容量 `120/8`、`200/14`、`280/20`、`360/26`、`430/32`。
- 每环按容量均匀取角，环起始角使用黄金角偏移（`137.508° * ring`），避免环间对齐。
- 合并 100 个槽位后，用固定种子 PRNG 洗牌，生成 `goldenAngleSlots()` 的固定顺序。
- `assignNodePositions` 保留 existing；新 ID 依次取首个未占用槽位；超过 100 用原 `fallbackPoint(id)`。

- [ ] **Step 4: 运行测试确认通过**

Run: `npm --prefix web test -- --run src/graph/layout.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/graph/layout.ts web/src/graph/layout.test.ts
git commit -m "feat: use stable golden-angle topology slots"
```

---

### Task 2: 缩放/平移纯函数

**Files:**
- Create: `web/src/graph/zoom.ts`
- Create: `web/src/graph/zoom.test.ts`

**Interfaces:**
- Consumes: 无。
- Produces:
  - `interface Transform { x: number; y: number; scale: number }`
  - `MIN_ZOOM = 0.6`, `MAX_ZOOM = 3`
  - `clampZoom(scale: number): number`
  - `zoomAt(current: Transform, factor: number, anchor: Point): Transform`
  - `fitTransform(bounds: Bounds, viewport: Size, padding: number, maxScale?: number): Transform`
  - `clampPan(current: Transform, bounds: Bounds, viewport: Size): Transform`

- [ ] **Step 1: 写失败测试**

```ts
it('keeps the anchor point fixed while zooming', () => {
  const before = { x: 0, y: 0, scale: 1 }
  const anchor = { x: 500, y: 400 }
  const after = zoomAt(before, 1.5, anchor)
  const worldBefore = { x: (anchor.x - before.x) / before.scale, y: (anchor.y - before.y) / before.scale }
  const worldAfter = { x: (anchor.x - after.x) / after.scale, y: (anchor.y - after.y) / after.scale }
  expect(worldAfter.x).toBeCloseTo(worldBefore.x)
  expect(worldAfter.y).toBeCloseTo(worldBefore.y)
})

it('clamps zoom and fits a bounding box', () => {
  expect(clampZoom(0.1)).toBe(MIN_ZOOM)
  expect(clampZoom(9)).toBe(MAX_ZOOM)
  const fitted = fitTransform(
    { minX: 100, minY: 100, maxX: 300, maxY: 300 },
    { width: 1000, height: 1000 },
    50,
    2,
  )
  expect(fitted.scale).toBeGreaterThan(1)
  expect(fitted.scale).toBeLessThanOrEqual(2)
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `npm --prefix web test -- --run src/graph/zoom.test.ts`
Expected: FAIL，模块不存在。

- [ ] **Step 3: 实现纯函数**

`zoomAt` 使用锚点世界坐标不变：
`nextX = anchor.x - (anchor.x - current.x) * (nextScale / current.scale)`，Y 同理。

`fitTransform` 计算包围盒中心、可用宽高、`scale = clamp(min((w-2p)/bw, (h-2p)/bh, maxScale))`，再居中。

`clampPan` 至少保留包围盒中心落在视口内，避免图完全拖出可见区域。

- [ ] **Step 4: 运行测试确认通过**

Run: `npm --prefix web test -- --run src/graph/zoom.test.ts`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add web/src/graph/zoom.ts web/src/graph/zoom.test.ts
git commit -m "feat: add topology zoom math"
```

---

### Task 3: GraphView 缩放、平移、工具栏与全屏

**Files:**
- Modify: `web/src/graph/graph.ts`
- Modify: `web/src/style.css`

**Interfaces:**
- Consumes: `goldenAngleSlots`/`assignNodePositions`、`Transform`/`zoomAt`/`fitTransform`/`clampPan`。
- Produces:
  - `GraphView.focusSession(sessionID: string): void`
  - `GraphView.fit(): void`
  - 保留 `render(...)`、`pulse(...)`、`reset()`

- [ ] **Step 1: 写/确认验证（DOM 行为）**

无新增 DOM 测试依赖；本任务用构建检查 + Task 5 的 E2E 验证。先在 `layout.test.ts` 保持通过。

- [ ] **Step 2: 重构 viewport**

- SVG 下创建 `<g class="graph-viewport">`，所有边/节点/脉冲放入其中。
- 维护 `#transform`，应用 `translate(x y) scale(k)`。
- `reset()` 重置 transform 和位置。

- [ ] **Step 3: 工具栏**

在 `svg.parentElement` 创建 `.graph-toolbar`（若不存在）：

- 按钮：`−`、百分比、`+`、`适应`、`全屏`；`aria-label` 完整。
- 点击 `+`/`−` 以画布中心缩放 1.2 倍。
- 百分比按钮复位到 1 倍；`适应` 调用 `fit()`。
- 全屏切换父图卡 `.graph-fullscreen`，进入后 fit，退出保留 transform；ESC 退出。

- [ ] **Step 4: 手势**

- `wheel`：`preventDefault`，按指针位置 `zoomAt`。
- Pointer 拖拽：pointerdown 捕获，pointermove 平移，pointerup 释放。
- 双指捏合：记录两个 pointer 的距离与中点，按比例缩放并平移中点。
- 使用 `requestAnimationFrame` 合并 transform 写入。
- `prefers-reduced-motion` 时不使用 CSS 过渡。

- [ ] **Step 5: 构建检查**

Run: `npm --prefix web run typecheck && npm --prefix web run build`
Expected: 成功。

- [ ] **Step 6: 提交**

```bash
git add web/src/graph/graph.ts web/src/style.css
git commit -m "feat: add topology zoom pan and fullscreen"
```

---

### Task 4: 语义标签、边热区与点击聚焦

**Files:**
- Modify: `web/src/graph/graph.ts`
- Modify: `web/src/style.css`
- Modify: `web/src/student/main.ts`
- Modify: `web/src/teacher/main.ts`

**Interfaces:**
- Consumes: `GraphView.focusSession`。
- Produces: 学生/教师在 `selectSession` 时调用 `graph.focusSession(sessionID)`。

- [ ] **Step 1: 边热区**

每个会话渲染：

- 可见 `line.graph-edge`。
- 透明 `line.edge-hit`（`stroke-width: 20`，`pointer-events: stroke`），放在可见线上方。
- 热区 hover 给可见边和端点加 `.hover`；点击调用 `onSelectSession`，随后 `focusSession`。
- 保留教师 `allowAllEdges` 与学生仅本人边可点。

- [ ] **Step 2: 标签层级**

- 节点半径 30；昵称 16px、短编号 12px；白色描边。
- 边标签 13px；本人/选中优先。
- SVG 根根据 `scale < 0.9 || participants.length > 40` 加 `labels-minimal`。
- CSS：`.labels-minimal .graph-node:not(.self):not(.focus):not(:hover) .node-name { display:none }`，边标签同理；放大后自动恢复。
- 点击/选中节点或边时加 `.focus`。

- [ ] **Step 3: 聚焦接入**

- `GraphView.focusSession(sessionID)` 计算两端点 + 标签包围盒，`fitTransform` 到最大 2.2 倍。
- 学生 `selectSession` 与教师 `selectSession` 在 `store/active` 更新后调用 `graph.focusSession(sessionID)`。
- 工具栏“适应”恢复全图。

- [ ] **Step 4: 构建检查**

Run: `npm --prefix web run typecheck && npm --prefix web run build`
Expected: 成功。

- [ ] **Step 5: 提交**

```bash
git add web/src/graph/graph.ts web/src/style.css web/src/student/main.ts web/src/teacher/main.ts
git commit -m "feat: improve topology labels edge hit areas and focus"
```

---

### Task 5: 回归、真实浏览器验证与发布

**Files:**
- Modify: `docs/progress.md`
- Modify: `docs/verification-j.md`（补充图形回归）
- Rebuild: `bin/traffic-classroom.exe`、`dist/traffic-classroom-0.1.0-dev/`

- [ ] **Step 1: 全量自动化**

Run:
```powershell
go vet ./...
go test -race ./...
npm --prefix web test
npm --prefix web run typecheck
npm --prefix web run build
```
Expected: 全部通过。

- [ ] **Step 2: 三节点/三边浏览器验证**

用 Playwright + 系统 Edge：

- 3 名学生 A/B/C 建立 AB、AC、BC 三个会话。
- 教师拓扑显示 3 个节点、3 条边；分别点击三条边能选中不同会话。
- 点击 `+` 后 transform scale 增大；`适应` 回到全图；全屏类切换；ESC 退出。
- 360px 学生页无横向溢出。
- 明文/加密/观察/方向脉冲原有 E2E 回归通过。

- [ ] **Step 3: 重建 EXE 与发布包**

```powershell
pwsh -NoProfile -File scripts/package.ps1 -Version 0.1.0-dev
```
记录新 SHA-256。

- [ ] **Step 4: 更新文档**

在 `docs/progress.md` 记录本次 UI 优化、验证证据与新构建哈希；更新 `docs/verification-j.md` 的图形回归结果。

- [ ] **Step 5: 提交与推送**

```bash
git add docs/progress.md docs/verification-j.md
git commit -m "docs: record topology layout and zoom verification"
git push origin main
```
