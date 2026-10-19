# 首页节点卡片导出为图片：设计方案与改动清单

> 状态：设计已确认（决策 1/2/3 已定）；**P0、P1 已实施**，P2 未做
> 涉及模块：`resource`（`static` / `template`）/ `cmd/dashboard`（仅测试）

## 零、实施记录

### P0：单卡片导出 PNG

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
   是 `display: grid`（`main.css:1513`），把按钮作为其子元素会多占一行。
   改为卡片内绝对定位到右上角。
2. **新增 `.nk-node-card .nk-node-header-main { padding-right: 1.5rem }`**（设计未提）。
   否则按钮会压住第一行右侧的「运行时长」文本。按钮用 `opacity` 显隐、始终占位，
   所以预留空间不会造成悬停抖动。（此预留内边距在 P1 已被移除，见零节 P1 偏差 6。）
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

### P1：复制到剪贴板 / 签名带 / 桌面布局

**已落地**

- `resource/static/card-export.js`：
  - 拆出 `render(cardEl, options)`（只渲染，返回 `{ blob, fileName }`），
    `capture()`（渲染 + 下载）与新增的 `copy()`（渲染 + 写剪贴板）都复用它。
  - 新增 `canCopy()`：要求 `isSecureContext` + `ClipboardItem` + `clipboard.write`。
  - 新增 `withFooter(canvas, brand, scale, logo)`：把成品 canvas 垫在主题背景色上、
    底部留出一条签名带。左侧 logo + 品牌名（垂直居中）；右侧上下两行，上行
    GitHub 仓库、下行生成时间，整块右对齐并垂直居中（见偏差 2、3、7、8、9）。
  - 新增 `loadLogo()` / `sizedLogoUrl()`：加载 `logo.svg` 作签名带左侧标记，
    失败时退回强调色圆点（见偏差 7）。
  - 新增 `githubMarkPath()` / `drawGithubMark()`：用 `Path2D` 画 GitHub mark，
    不依赖图标字体；画不出来时仓库地址退回完整写法（见偏差 8）。
  - 新增 `truncate()`：品牌名放不下时截断加省略号，避免压到右侧。
  - 新增 `themeColors()`：从 `:root` 读 `--nk-bg` / `--nk-text` / `--nk-muted` /
    `--nk-accent`，读不到就退回浅色默认值，导出图跟随当前主题。
  - 新增 `watermarkStamp(now)`：`YYYY-MM-DD HH:mm`。
  - `render()` 用 `windowWidth` 固定桌面布局（见偏差 1）。
- `resource/template/theme-default/home.html`：
  - 导出按钮改为 `.nk-card-tools` 工具条，内含导出按钮与（条件渲染的）复制按钮，
    两者都带 `data-html2canvas-ignore="true"`。
  - `data()` 加 `copied: {}`；`computed` 加 `canCopyImage`；`methods` 加 `copyCard`。
  - 新增 `const brandName = {{.Conf.Site.Brand}}`，作为水印的品牌名。
- `resource/static/main.css`：`.nk-card-export` 拆成 `.nk-card-tools`（容器，负责
  hover / 触摸显隐）与 `.nk-card-tool`（按钮）；工具条改为绝对定位悬浮，
  不再为它预留内边距（见偏差 6）。
- `resource/template/common/header.html`：`main.css` 缓存版本号
  `v2026101901` → `v2026101903`（P1 功能一次、工具条样式一次）。
- `resource/template/theme-default/home.html`：`card-export.js` 缓存版本号
  `v2026101901` → `v2026101906`。**这一步 P1 时漏了**，导致浏览器一直用 P0
  缓存下来的旧脚本，导出图上看不到水印也点不到复制（见遗留问题 1）。
- `cmd/dashboard/controller/template_test.go`：`TestHomeCardExportAssets` 的断言
  同步到新符号。

**与设计的偏差**

1. **固定桌面布局用 `windowWidth`，不是 iframe**。html2canvas 内部会把文档克隆进一个
   宽度等于 `windowWidth` 的 iframe，媒体查询正是按这个宽度求值，所以直接把
   `windowWidth` 抬到 `DESKTOP_MIN_WIDTH = 1180` 就够了，比「克隆塞进固定宽度
   iframe」简单得多，也不用管 iframe 的生命周期。取值是
   `Math.max(window.innerWidth, 1180)`：宽屏保持真实视口，窄屏 / 移动端一律按桌面
   布局导出。
2. **水印改成卡片之外的签名带，不是盖在卡片上的胶囊**。第一版是右下角的半透明
   深色底衬 + 白字，像贴上去的贴纸，压在卡片内容上；现在把成品 canvas 垫在
   `--nk-bg` 背景上、底部多留 46px 画一条签名带：左「logo + 品牌」右「仓库 / 时间」。
   好处是留白在卡片之外，不遮内容，配色直接取主题变量，浅色 / 暗色都自带对比，
   也不受 `backgroundColor: null`（透明背景）影响。代价是导出图比卡片高 46px。
3. **签名带时间精确到分钟**（`YYYY-MM-DD HH:mm`），比文件名里的秒级时间戳短，够用。
4. **复制按钮按 `canCopyImage` 条件渲染**，不满足安全上下文时直接不出现，而不是
   点了才报错；`copyCard` 里仍保留一次 `canCopy()` 兜底。
5. **`.nk-card-export` 类名已废弃**，P0 记录里的类名与 `1.5rem` 内边距已过时。
6. **工具条悬浮，不再占位**。P0/P1 早期给 `.nk-node-header-main` 加了
   `padding-right`（`1.5rem` → 两个按钮时 `3.5rem`），把「运行时长」从右边缘
   挤开。现改为：工具条绝对定位悬浮在右上角、不参与布局，「运行时长」始终
   贴右边缘；悬停时工具条淡入（`opacity` + `translateY/scale` 微动效），
   同时把「运行时长」淡出，避免两层文字叠在一起。触摸设备（`hover: none`）
   工具条常显，「运行时长」同样淡出。
7. **logo 用 data URL 注入尺寸，不改 `logo.svg`**。`logo.svg` 只有 `viewBox`、
   没有 `width`/`height`，部分浏览器（Firefox）不会把这种 SVG 画进 canvas。
   这里读出 SVG 文本、补上 `width`/`height` 再转成 data URL 交给 `Image`；
   `fetch` 不可用 / 失败 / 图片加载失败都退回原始地址，再失败才退回强调色圆点。
   之所以不直接给 `logo.svg` 加尺寸：它还用在顶栏、登录页与 hotaru / daynight
   主题的密码页，其中 hotaru 的 `.ui.image` 没有显式尺寸，改资源会连带改那些页面。
8. **GitHub 地址用 `Path2D` 画 mark，不用图标字体**。semantic-ui 里虽然有
   `i.icon.github`，但那是图标字体字形，画到 canvas 上要先确保字体已加载、还要
   按码点取字，比内联一段官方 mark 路径脆弱。`Path2D` 不可用或解析失败时，
   地址退回 `github.com/r0n9/nodekeep`（带前缀，不靠图标也读得懂）。
9. **右侧分两行，不是一行「仓库 · 时间」**。仓库名和时间挤在一行时右侧过宽，
   把品牌名的可用空间压得很紧。改成上行仓库、下行时间后，右侧块宽度只取两行
   里较宽的那行，品牌名能多留一截；两行整块垂直居中，左侧 logo + 品牌仍按
   签名带中线对齐。签名带高度因此从 36px 加到 46px（`FOOTER_HEIGHT`），
   行距用 `FOOTER_LINE_GAP`（4px）。**放不下时只丢仓库那一行**，时间单独居中，
   品牌名再按剩余空间截断。

**验证结果**

```
GOCACHE=/tmp/nk-gocache go build ./...                                  # OK
GOCACHE=/tmp/nk-gocache go vet ./cmd/dashboard/...                      # OK
GOCACHE=/tmp/nk-gocache go test ./cmd/dashboard/controller/             # PASS
```

用 DOM stub（不是 jsdom：`window` / `document` 的最小实现 + stub `html2canvas`）
跑了 `card-export.js` 的 P1 行为，61/61 通过：

| 场景 | 结果 |
|---|---|
| `watermarkStamp` | `2026-10-19 16:08`；旧的 `watermarkText` 已移除 |
| `canCopy` | 缺 `isSecureContext` / `ClipboardItem` / `clipboard.write` 任一即为 false |
| `render` 失败路径 | 无卡片、无 `html2canvas` 各自拒绝，文案可读 |
| `render` 成功路径 | 返回 blob 与 `nodekeep-<名>-<时间戳>.png`；离屏容器已清理；不触发下载 |
| 桌面布局 | `innerWidth=0` → `windowWidth=1180`；`innerWidth=1600` → `windowWidth=1600` |
| `capture` | 触发一次 `<a download>`，文件名正确 |
| `copy` | 写入 1 个 `ClipboardItem`，含 `image/png`；非安全上下文直接拒绝 |
| 签名带 | 画布加高 `46 × scale`、宽度不变；左侧 logo（`15 × scale`、贴左边距、垂直居中）+ 品牌（正文色、按带中线对齐），右侧两行右对齐贴 `14 × scale` 边距：上行 GitHub mark（`12 × scale`）+ 仓库（弱化色），下行时间（弱化色），两行间距 `fontPx + 4 × scale` |
| logo 来源 | 默认走补过尺寸的 data URL、只加载一次；无 `fetch` / `fetch` 失败 → 原始地址；data URL 加载失败 → 重试原始地址 |
| logo 降级 | 无 `Image` 或加载失败 → 退回强调色圆点，品牌名照常 |
| GitHub mark | 有 `Path2D` → 画 mark + 短名 `r0n9/nodekeep`；无 `Path2D` → 不画 mark、退回 `github.com/r0n9/nodekeep` |
| 签名带边界 | 品牌为空时只留 logo 与右侧；放不下时丢仓库那一行、时间单独垂直居中；真实最小卡片（320 CSS px × scale 2）两行都放得下；超长品牌截断加省略号且不压到右侧；暗色主题取到暗色文字；读不到主题变量也能画 |

> 图片的像素级效果（进度条、图标、国旗、网络曲线、签名带观感、浅色 / 暗色）
> 仍需人工核对，见第六节手动清单。

## 一、背景与目标

### 现状

- 首页卡片视图由 `resource/template/theme-default/home.html:38` 的
  `<article class="nk-node-card">` 渲染，`v-for="server in group.data"`，
  `:key="server.ID"`。
- 卡片内容分四块：头部（`home.html:52`）、CPU/内存/硬盘三条进度条
  （`home.html:84`）、网络实时块（`home.html:102`，内含 `network-chart`
  组件与周期流量 HP 条）、以及可折叠的「节点详情」
  （`home.html:134` 的 `<details class="nk-node-details">`）。
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
  （`home.html:137`，`[open]` 下该箭头朝上，静态图里像「可收起」），
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

> 本节是 P0 的设计原稿，代码块里的 `.nk-card-export` / `capture()` 已被 P1 重构
> （工具条 + `render()` / `copy()`），以零节的 P1 实施记录为准。

### 模板改动（`resource/template/theme-default/home.html`）

- 卡片内（`.nk-node-card`）加一个下载按钮，绝对定位到卡片右上角。
  **不要放进 `.nk-node-header`**：它是 `display: grid`（`main.css:1513`），
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
  <script src="/static/card-export.js?v2026101906"></script>
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
  因此预留空间不会造成悬停抖动。（P1 已改为悬浮工具条 + 运行时长淡出，
  不再预留内边距，见零节 P1 偏差 6。）

### 缓存版本号（必须同步递增）

本仓库的手工缓存击穿参数，改了对应文件就要递增：

- `resource/template/common/header.html:37` — `main.css?v2026101903`
- `resource/template/common/footer.html:21` — `main.js?v2026101601`

（`card-export.js` 是新文件，首次引入的 query 版本号自定即可。它的 query 版本号
在 P1 时忘了跟着内容一起递增，导致浏览器一直用 P0 的缓存脚本、导出图没有水印，
现已补到 `v2026101906`，见零节 P1 与遗留问题 1。）

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
   默认国旗是 `/static/logo.svg`（`main.css:4473`），均同源，不会污染 canvas。
5. **不要直接截实时 DOM**。直接展开实时卡片会让卡片抖一下再弹回；
   离屏克隆方案天然避免。
6. **导出宽度取卡片当前渲染宽度**（下限约 320px），保证与屏幕所见一致。
   P1 已实现「不管设备都输出桌面版布局」：`render()` 把 `windowWidth` 抬到
   `DESKTOP_MIN_WIDTH = 1180`（html2canvas 的克隆 iframe 宽度即媒体查询视口），
   不用自己塞 iframe。见零节 P1 实施记录的偏差 1。
7. **截图库加载失败要可见**。`window.html2canvas` 不存在时按钮隐藏或点击提示，
   不能静默失败。

## 六、测试清单

- `cmd/dashboard/controller/template_test.go`：在现有渲染测试
  （`TestServeWebTemplateParse` / `TestNodeDetailPageAndMetricsEndpoint`）里
  补断言，确认首页 HTML 含 `.nk-card-tools` / `.nk-card-tool` 按钮、
  `exportCard` / `copyCard` / `canCopyImage`、`brandName` 与 `card-export.js` 引用。
- 手动：浅色 / 暗色各导一张，核对进度条、图标、国旗、网络曲线、详情网格
  是否与卡片一致；展开与折叠两种状态各导一张，确认决策 3 生效。
- 手动：核对卡片下方的签名带（左「logo + 品牌」垂直居中，右「GitHub 仓库」在上、
  「时间」在下）在浅色 / 暗色下都清晰、logo 没有变形、与卡片左右内边距对齐；
  窄屏（或移动端）导出仍是桌面版布局（详情多列、内边距不变）。
- 手动：HTTPS / localhost 下复制按钮出现且能粘贴出 PNG；HTTP 下按钮不出现。
- 手动：卡片未悬停时「运行时长」贴右边缘、不被工具条挤开；悬停时工具条浮出、
  两个图标居中，触摸设备上工具条常显。
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

- **P0（已实施）**：单卡片导出 PNG（详情按 `open` 状态决定），vendor `html2canvas`，
  按钮 + 下载 + 缓存版本号递增。
- **P1（已实施）**：复制到剪贴板（`navigator.clipboard.write` + `ClipboardItem`，需 HTTPS）；
  卡片下方加签名带（左 logo + 品牌名，右 GitHub 仓库 / 生成时间两行）；固定桌面布局
  （用 html2canvas 的 `windowWidth`，不是 iframe）。见零节 P1 实施记录。
- **P2（未做，可选）**：列表视图导出；批量导出；导出尺寸选项。

## 九、遗留问题

1. ~~**`card-export.js` 的 query 版本号没递增**~~ **已修**。P0 提交（`d2c3a6d`）里是
   `card-export.js?v2026101901`，P1 改了文件内容但没动这个版本号，已缓存旧脚本的
   浏览器拿不到 `copy()` / 水印 / `windowWidth`——表现就是导出图上没有水印。
   现已递增到 `v2026101906`。**注意**：已经缓存过 `v2026101901` 的浏览器这次能拿到
   新脚本（URL 变了），但如果之后再改 `card-export.js`，记得同步递增。
2. **签名带与复制没有自动化测试**。零节的 P1 验证是一次性的 DOM stub 脚本，
   没进仓库；`TestHomeCardExportAssets` 只做字符串断言。
3. **P2 未做**：列表视图导出、批量导出、导出尺寸选项。
