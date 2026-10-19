# 首页节点卡片导出为图片：设计方案与改动清单

> 状态：设计已确认（决策 1/2/3 已定）；**P0 已实施**，P1/P2 未做
> 涉及模块：`resource`（`static` / `template`）/ `cmd/dashboard`（仅测试）

## 零、P0 实施记录

**已落地**

- `resource/static/vendor/html2canvas/`（新增）：`html2canvas.min.js`（1.4.1，约 194KB）
  与上游 `LICENSE`，取自 `npm pack html2canvas@1.4.1`。
- `resource/static/card-export.js`（新增）：`window.NodeKeepCardExport.capture(cardEl, options)`。
  内含离屏克隆、`<details>` 处理、canvas 位图搬运、字体等待、`toBlob` 下载、
  文件名清洗，以及 `toDataURL` 抛错（画布被污染）时跳过该 canvas 的兜底。
- `resource/template/theme-default/home.html`：卡片内加 `.nk-card-export` 按钮
  （带 `data-html2canvas-ignore="true"`）；`data()` 加 `exporting: {}`；
  `methods` 加 `exportCard`；文件底部引入 vendor 与 `card-export.js`。
- `resource/static/main.css`：`.nk-node-card` 加 `position: relative`；
  新增 `.nk-card-export` 样式与触摸设备常显规则。
- `resource/template/common/header.html`：`main.css` 缓存版本号
  `v2026101604` → `v2026101901`。
- `cmd/dashboard/controller/template_test.go`：新增 `TestHomeCardExportAssets`。

**与设计的偏差**

1. **按钮挂在 `.nk-node-card` 上，不是 `.nk-node-header` 内**。`.nk-node-header`
   是 `display: grid`（`main.css:1456`），把按钮作为其子元素会多占一行。
   改为卡片内绝对定位到右上角。
2. **新增 `.nk-node-card .nk-node-header-main { padding-right: 1.5rem }`**（设计未提）。
   否则按钮会压住第一行右侧的「运行时长」文本。按钮用 `opacity` 显隐、始终占位，
   所以预留空间不会造成悬停抖动。
3. **`main.js` 的缓存版本号未递增**。本次没有改动 `main.js`，
   按仓库「改了才递增」的约定保持 `v2026101601` 不动。
4. **`exporting` 用 `$set` / `$delete` 维护**。Vue 2 侦测不到对象新增键，
   直接赋值不会触发按钮的 `:disabled` 与 spinner 更新。

**验证结果**

```
GOCACHE=/tmp/nk-gocache go build ./...                                  # OK
GOCACHE=/tmp/nk-gocache go vet ./cmd/dashboard/...                      # OK
GOCACHE=/tmp/nk-gocache go test ./cmd/dashboard/controller/ -v          # PASS
```

用 jsdom 对 `card-export.js` 做了行为验证（stub 掉 `html2canvas` 与 `toBlob`）：

| 场景 | 结果 |
|---|---|
| 详情展开 | `details` 保留、箭头已删、详情网格在、canvas 已换成 `img` |
| 详情折叠 | 整块 `details` 已移除、无箭头、无详情网格 |
| 原卡片 | 未被改动（`open` 状态与箭头都保持原样） |
| 离屏容器 | 渲染后被清理 |
| 文件名 | `node-a/../evil:name` → `nodekeep-node-a_.._evil_name-<时间戳>.png` |

> `cmd/agent/monitor` 的 `TestFetchIPParsesJSONAndPlainText` 在沙箱内失败
> （`bind: operation not permitted`，不允许监听端口），与本次改动无关。

## 一、背景与目标

### 现状

- 首页卡片视图由 `resource/template/theme-default/home.html:38` 的
  `<article class="nk-node-card">` 渲染，`v-for="server in group.data"`，
  `:key="server.ID"`。
- 卡片内容分四块：头部（`home.html:39`）、CPU/内存/硬盘三条进度条
  （`home.html:71`）、网络实时块（`home.html:89`，内含 `network-chart`
  组件与周期流量 HP 条）、以及可折叠的「节点详情」
  （`home.html:121` 的 `<details class="nk-node-details">`）。
- 网络曲线由 `resource/static/network-chart.js` 的 `network-chart` 组件用
  uPlot 绘制到 `network-chart.js:111` 的 `<div ref="canvas" class="nk-network-canvas">`
  里（实为 `<canvas>`）。
- 卡片样式在 `resource/static/main.css`，主题通过 `<html data-theme>` 上的
  CSS 变量切换（`resource/template/common/header.html:27` 的启动脚本）。

### 目标

1. 卡片视图下，每张卡片支持「导出为图片」并下载 PNG。
2. 图片内容与卡片当前展示一致：**详情已展开则包含详情，未展开则不包含**。
3. 纯前端实现，不改后端接口、不引入 headless 浏览器。
4. 游客页（查看密码鉴权）同样可用。

## 二、关键决策

### 决策 1：前端 DOM 光栅化，不用服务端渲染

| 方案 | 做法 | 评价 |
|---|---|---|
| **A（已选）前端 DOM 光栅化** | 浏览器里把卡片 DOM 克隆后交给截图库渲染成 canvas，再 `toBlob` 下载 | 零后端改动、所见即所得、游客页可用；代价是引入一个约 200KB 的 vendor 库 |
| B 服务端 headless Chrome | `chromedp` 起无头浏览器渲染页面再截图 | 像素级还原，但要在面板主机/容器里塞一个浏览器，镜像体积与维护成本都不划算，单人自用场景过重 |
| C 纯 canvas 手绘卡片 | 用 canvas API 逐项绘制 | 布局逻辑与 DOM 双份维护，必然漂移，排除 |

选 A。

### 决策 2：是否包含详情，由 `<details>` 的当前 `open` 状态决定

不强制展开、也不固定折叠，而是**照抄截图瞬间的真实状态**：

- 展开 → 图片包含 `.nk-node-detail-grid` 的全部字段。
- 折叠 → 图片只有卡片头部与指标区。

好处：

- 用户自己控制信息量，符合直觉。
- 克隆逻辑变成纯粹的「照抄当前状态」，不需要改实时 DOM 的 `open`
  再还原，也就没有「展开→截图→弹回」的抖动。

### 决策 3：图里不留任何假交互

静态图里不能出现「看起来能点」的元素。规则：

- **展开时**：删掉 `<summary>` 里的 `nk-node-details-toggle` 箭头
  （`home.html:124`，`[open]` 下该箭头朝上，静态图里像「可收起」），
  把 `<summary>` 降级成普通小标题。
- **折叠时**：整块 `<details>` 从克隆里 `remove()`，图里不出现点不动的
  「节点详情 ⌄」折叠条。
- 下载按钮本身加 `data-html2canvas-ignore="true"`，不进图。

## 三、技术选型

选 `html2canvas`，vendor 到 `resource/static/vendor/html2canvas/html2canvas.min.js`，
与 `uplot` / `vue` 同风格带版本号 query 引入。**不走 CDN**（面板可能内网部署）。

选它的依据（已核对本仓库实际用到的特性）：

- 卡片用到的 `CSS 变量` / `box-shadow` / `border-radius` / grid / flex /
  内联 canvas / 图标字体 / 雪碧图背景，它都覆盖。
- `resource/static/main.css` 中**没有** `oklch()` / `color-mix()` / `lab()` /
  `lch()` / `light-dark()` 这类它解析不了的颜色函数（已 grep 确认）。
- `backdrop-filter` 只出现在顶栏（`main.css:165`）与 tooltip（`main.css:388`），
  **不在卡片内**，所以没有已知渲染盲区。

> 备选：`modern-screenshot`（更小、维护更活跃）。若 `html2canvas` 在实测中
> 出现字体或阴影偏差，可替换，`card-export.js` 的封装层不变。

## 四、P0：单卡片导出 PNG

### 模板改动（`resource/template/theme-default/home.html`）

- 卡片内（`.nk-node-card`）加一个下载按钮，绝对定位到卡片右上角。
  **不要放进 `.nk-node-header`**：它是 `display: grid`（`main.css:1456`），
  按钮作为其子元素会多占一行：

  ```html
  <button type="button" class="nk-card-export" data-html2canvas-ignore="true"
          :disabled="!!exporting[server.ID]" @click.stop="exportCard(server)"
          :aria-label="'导出 ' + server.Name + ' 的卡片图片'" title="导出为图片">
      <i :class="exporting[server.ID] ? 'spinner loading icon' : 'download icon'" aria-hidden="true"></i>
  </button>
  ```

- 文件底部脚本区（`home.html:342` 的 uplot 引入旁）加上 vendor 与封装脚本：

  ```html
  <script src="/static/vendor/html2canvas/html2canvas.min.js?v1.4.1"></script>
  <script src="/static/card-export.js?v2026101901"></script>
  ```

- `data()` 里加 `exporting: {}`，`methods` 里加 `exportCard`（见下）。

### 新增 `resource/static/card-export.js`

挂到 `window.NodeKeepCardExport`，导出一个 `capture(cardEl, options)`：

1. 在 `body` 下建一个 `position: fixed; left: -99999px; top: 0` 的离屏容器
   （CSS 全局，变量与主题自动继承），把 `cardEl.cloneNode(true)` 放进去。
2. 处理 `<details>`：`open` → 删 `.nk-node-details-toggle`；非 `open` → `remove()`。
3. 搬运 canvas 位图：把克隆里每个 `<canvas>` 换成
   `<img src="原canvas.toDataURL()">`，并显式设置 `width/height` 保持尺寸。
4. `await document.fonts.ready`，再 `html2canvas(container, { scale: 2, backgroundColor: null })`。
5. `canvas.toBlob` → `URL.createObjectURL` → `<a download>` 触发下载 → `revokeObjectURL`。
6. `finally` 里移除离屏容器。

文件名：`nodekeep-<节点名>-<时间戳>.png`，节点名清洗掉 `/ \ : * ? " < > |` 等字符。

### 样式（`resource/static/main.css`）

- `.nk-node-card` 加 `position: relative`（挂绝对定位按钮）。
- 新增 `.nk-card-export`：右上角小图标按钮，桌面端 hover 显示、
  `@media (hover: none)` 常显；`:disabled` 时转圈或降透明度。
- 给 `.nk-node-card .nk-node-header-main` 加 `padding-right`，为按钮预留位置，
  否则会压住第一行右侧的「运行时长」文本。按钮用 `opacity` 显隐、始终占位，
  因此预留空间不会造成悬停抖动。

### 缓存版本号（必须同步递增）

本仓库的手工缓存击穿参数，改了对应文件就要递增：

- `resource/template/common/header.html:37` — `main.css?v2026101604`
- `resource/template/common/footer.html:21` — `main.js?v2026101601`

（`card-export.js` 是新文件，首次引入的 query 版本号自定即可。）

## 五、关键实现细节（坑）

1. **`<details>` 的折叠态不要交给截图库**。浏览器隐藏折叠内容靠 UA shadow DOM 的
   `content-visibility`，不是子元素 `display:none`，html2canvas 的克隆路径未必
   处理正确，可能把折叠内容也画出来。**必须由 `card-export.js` 自己按决策 3 处理**。
2. **canvas 位图不会被 `cloneNode` 复制**。`cloneNode(true)` 只复制标签，
   克隆里的 canvas 是空白的。必须用 `toDataURL()` 搬位图。uPlot 只画路径和文字，
   同源不污染，`toDataURL` 可用。
3. **字体要等就绪再截**。`<i class="hdd outline icon">` 等是图标字体，
   不 `await document.fonts.ready` 会渲染成方框。
4. **图片不跨域**。国旗是 Semantic UI 的 `flags.png` 雪碧图
   （`resource/static/vendor/semantic-ui/themes/default/assets/images/flags.png`），
   默认国旗是 `/static/logo.svg`（`main.css:4408`），均同源，不会污染 canvas。
5. **不要直接截实时 DOM**。直接展开实时卡片会让卡片抖一下再弹回；
   离屏克隆方案天然避免。
6. **导出宽度取卡片当前渲染宽度**（下限约 320px），保证与屏幕所见一致。
   若想「不管设备都输出桌面版布局」，需把克隆放进固定宽度的 `<iframe>` 渲染
   （媒体查询按视口算），属 P1 增强，P0 不做。
7. **截图库加载失败要可见**。`window.html2canvas` 不存在时按钮隐藏或点击提示，
   不能静默失败。

## 六、测试清单

- `cmd/dashboard/controller/template_test.go`：在现有渲染测试
  （`TestServeWebTemplateParse` / `TestNodeDetailPageAndMetricsEndpoint`）里
  补断言，确认首页 HTML 含 `.nk-card-export` 按钮与 `card-export.js` 引用。
- 手动：浅色 / 暗色各导一张，核对进度条、图标、国旗、网络曲线、详情网格
  是否与卡片一致；展开与折叠两种状态各导一张，确认决策 3 生效。
- 回归：导出不影响实时 WS 刷新与卡片折叠状态。

### 验证命令

```sh
GOCACHE=/tmp/nk-gocache go test ./cmd/dashboard/controller/
go build ./...
```

> 直接 `go test` 会因沙箱无法写 `~/Library/Caches/go-build` 报
> `net/http/httptest is not in std`，指定可写 `GOCACHE` 即可。

## 七、风险与必须处理的坑

1. **`open` 状态不持久**。首页 WS 每 2 秒全量替换 `servers`，`v-for` 靠
   `:key="server.ID"` 原地 patch，元素不重建、`open` 能保住；但节点改分组
   （`Tag` 变化）导致跨 `group.data` 移动时元素会被重建，展开状态丢失。
   这是现有行为，非本次引入，只是截图时会暴露。
2. **导出的是点击瞬间的快照**。网络图是最近 60 秒窗口
   （`network-chart.js` 的 `WINDOW_SECONDS = 60`），之后数据变化不反映到已下载的图。
3. **图片跟随当前主题**。暗色导出暗色图；如需固定浅色要额外强制。
4. **新增 vendor 体积**。约 200KB，与 `uplot` 同量级，可接受。
5. **游客页可用**。纯前端实现，卡片本就对游客可见，不新增泄露面
   （`Host.IP` 已 `json:"-"`）。

## 八、建议分期

- **P0**：单卡片导出 PNG（详情按 `open` 状态决定），vendor `html2canvas`，
  按钮 + 下载 + 缓存版本号递增。
- **P1**：复制到剪贴板（`navigator.clipboard.write` + `ClipboardItem`，需 HTTPS）；
  图片角落加水印（品牌名 + 生成时间）；固定桌面布局的 iframe 渲染。
- **P2（可选）**：列表视图导出；批量导出；导出尺寸选项。
