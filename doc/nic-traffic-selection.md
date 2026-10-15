# 后台可选网卡统计流量：设计方案与改动清单

> 状态：设计已确认（决策 1/2/3 已定）；**P0、P1 均已实施**，P2（可选）未做
> 涉及模块：`proto` / `cmd/agent` / `model` / `service/dao` / `service/rpc` / `cmd/dashboard` / `resource`

## 零、实施记录

### 变更：删除 Agent 的 `-n` 网卡白名单参数

**动机**：`-n` 与后台的网卡选择功能重叠，且两者不一致时会静默失效——
若 Agent 配了 `-n eth1` 而管理员在后台选了 `eth0`，`sumSelectedNICs` 匹配不到
任何网卡，返回 `0/0` 且 `hasNICs=true`（不会回落到聚合字段），结果是网速与
累计恒为 0 且服务端不报错。删掉 `-n` 后 Agent 永远上报全部网卡，
服务端的选择必然匹配得到，这个失效模式从根上消失。

**已删除**

- `cmd/agent/main.go`：`nicAllowlist` 变量、`-n` flag 定义、`SetNICAllowlist` 调用。
- `cmd/agent/monitor/monitor.go`：`nicAllowlist` 原子变量、`SetNICAllowlist`、
  `currentNICAllowlist`、`shouldIncludeNIC`；`sumNetworkCounters` 与
  `collectNICCounters` 去掉 allowlist 参数。
- `cmd/agent/monitor/myip.go`：`getLocalIPv6` 改用 `isVirtualNIC` 过滤网卡。
- `script/install-agent.sh`：`NIC_ALLOWLIST` 变量、`-n|--nic` 选项、usage 行、
  安装摘要输出、`agent_flags` 拼接、macOS plist 的 `nic_args`。
- `cmd/agent/monitor/monitor_test.go`：`TestSumNetworkCountersUsesAllowlist`、
  `TestCollectNICCountersRespectsAllowlist` 两个用例，以及各处 `SetNICAllowlist` 调用。
- `README.md`：`--nic eth0,en0` 的说明改为指向后台的网卡选择。

**行为变化**

- Agent 侧的聚合口径从「`-n` 白名单，或默认过滤虚拟网卡」变为**固定过滤虚拟网卡**。
  这与服务端「留空 = 自动」的默认口径一致，所以老面板（读聚合字段）行为不变。
- 曾配过 `-n eth0` 的节点：该参数现在会被 `flag.Parse()` 当成未知参数直接
  `os.Exit(2)`。**存量部署升级前必须先改服务文件**（删除 `-n` 及其值），
  否则 systemd/launchd 会陷入重启崩溃循环。当前项目为单人自用、未实际使用该参数，
  因此接受这个破坏性变更。

**兼容性矩阵中已失效的两行**（第七节原文保留作历史记录）：
「新 Agent + 旧面板」不再有「Agent 仍按 `-n` 过滤」的说法；
「任一 + 任一」的「`-n` 参数语义不变」已不成立。

### 修复：P1 的网卡选择器渲染成纯文本列表

**现象**：基于最新代码启动服务后，「流量统计网卡」显示为一串网卡名，
而不是可点开的多选下拉框。

**根因**：模板用的是 `<select multiple class="ui ... nk-server-nic-select">`，
而 Semantic UI 2.4.1 初始化 `<select>` 时的行为是（见 `semantic.min.js` 的
`setup.select`）：

```js
$module = $('<div />')
  .attr('class', $select.attr('class'))   // 把 select 的 class 复制给新 div
  .addClass('selection').addClass('dropdown')
  .html(templates.dropdown(values))
  .insertBefore($select);
...
$select.removeAttr('class').detach().prependTo($module);  // select 被移进 div，class 被摘掉
```

也就是说初始化之后，`.nk-server-nic-select` 匹配到的是**生成的 div**，
而 `<select>` 已经失去 class 并被移进该 div 内部。于是
`modal.find('.nk-server-nic-select')` 拿到的是 div，随后的 `empty()` 把
div 的子节点（隐藏的 select、搜索框、`.menu`）全部清空，再把 `<option>`
直接 append 到 div 上——游离的 `<option>` 被浏览器当纯文本渲染，
于是看到的就是一串网卡名。

雪上加霜的是 `refresh` 并不会重新读取 `<select>` 的 option
（`refresh` = `refreshSelectors` + `refreshData`，只重新缓存 DOM 引用），
所以「先初始化、再动态加 option」这条路在 select 上根本走不通。

**修复**：

1. 模板改成 div + 隐藏 input 的结构（与计划任务的服务器选择器一致，
   那是本仓库已验证可用的写法），不再用 `<select multiple>`。
2. `loadServerNICs` 改为往 `.menu` 里 append `.item` div，再 `refresh`；
   对 div 结构 `refresh` 会重新查询 `F.find(selector.item)`，动态项能被识别。
3. 提交时不再依赖隐藏 input 的序列化格式，改用
   `dropdown('get values')` 读选中项。该 API 在 div + multiple 下返回数组
   （`get value` 返回的是逗号拼接的字符串，`get values` 才 split 成数组），
   无选中时返回空串，代码里做了兜底。
4. `fillServerBillingForm` 的排除范围从 `.nk-server-nic-select` 扩大到
   `.nk-server-nic-select, .nk-server-nic-select *`，避免它去清空
   dropdown 内部的隐藏 input。

**另一个独立问题：改了静态文件却没更新缓存版本号。**
`footer.html` 的 `main.js?v2026090207` 与 `header.html` 的
`main.css?v2026092802` 是手工维护的缓存击穿参数（历史上每次改动都会递增）。
本次改造改了 `main.js` / `main.css` 但没有递增，浏览器会继续用旧文件。
已一并更新为 `v2026101601`。**后续改这两个文件时必须同步递增。**

### P1 实施记录

实施范围与第五节一致，实际落地情况与偏差如下。

**已落地**

- `model/server.go`：`Server.TrafficNICs string`；`ParseNICSelection` / `FormatNICSelection`；
  `Marshal()` 带上该字段；`MaxTrafficNICs = 16`、`MaxNICNameLength = 64`。
- `service/dao/server_nic.go`：新增 `sumSelectedNICs`，返回选中集合的求和值与
  「实际参与求和的网卡名指纹」`nicKey`。
- `service/dao/traffic.go`：`serverTrafficTracker` 增加 `lastNICKey` / `lastRawAt`；
  `ProcessServerTraffic` 增加 `nicKey` 参数并改为返回 `trafficResult`
  （累计值 + 重算速率 + 快照）；新增 `counterDeltaForSpeed` / `ratePerSecond`。
- `service/dao/dao.go`：`UpdateServerState` 读出 `TrafficNICs` 并解析后传给
  `ObserveServerMetric`；`ObserveServerMetric` 按选中集合求和、口径变化时重设基线、
  用重算速率覆盖 `runtime.State`；`serverMetricBucket` 增加 `lastNICKey`；
  新增 `ServerRuntimeOf`。
- `cmd/dashboard/controller/member_api.go`：`serverForm.TrafficNICs []string`；
  `normalizeNICSelection` 校验（去空白、去重、限长限量）；`addOrEditServer` 落库；
  `/nic` 接口补 `Selected` 与 `Stale`。
- 前端：`component/server.html` 的只读清单换成多选；`main.js` 的 `loadServerNICs`
  填充选项并回填已选；`showFormModal` 的序列化支持多值字段；`fillServerBillingForm`
  跳过网卡选择器；`main.css` 移除上一版只读清单的样式。

**与设计的偏差**

1. **`Marshal()` 下发的是存储原样的逗号分隔字符串，不是 JSON 数组**。
   设计里只写了「带上该字段」。改成数组会破坏 `model/server_test.go` 里
   `TestServerMarshal` 的既有不变量（`Marshal` 输出可原样反序列化回 `Server`），
   所以保持字符串，由前端 `split(',')`。`/nic` 接口的 `Selected` 仍是数组。
2. **选择器不按 `Supported` 硬禁用，改为「有选项即可用」**。设计写的是
   「`Supported=false` 时禁用选择器」，但禁用后 `serializeArray` 会跳过该字段，
   保存就把已配置的网卡清空了——正是设计里想避免的「静默丢配置」。
   实际规则：选项 = 已上报网卡 ∪ 已保存选择；只要还有选项就可编辑，
   没有选项时自然为空、清空也无损失。
3. **`Stale` 是新增字段**（设计未提）。网卡超过 `nicStaleAfter = 10min`
   未出现在上报里就标记「已失联」，前端在选项标签上提示，但**不**移除该选项。

**速率口径的行为变化（需注意）**

改造前分钟指标桶记录的是 Agent 上报的 `NetInSpeed/NetOutSpeed`；现在一律以服务端
按选中集合重算的值为准。`service/dao/server_public_snapshot_test.go` 里
`TestObserveServerMetricAggregatesMinuteBuckets` 的期望值已相应更新
（200/300 350/500 → 8/17 10/21）。这是本次改造的预期结果，不是回归。

**验证结果**

```
go build ./...   # 通过
go vet ./...     # 通过
go test ./...    # 除下述 1 项外全部通过
```

P1 新增用例：`service/dao/server_nic_selection_test.go`（选中集合求和、口径变化的
基线重设、虚拟网卡出现时的基线重设、速率重算、老 Agent 回落）、
`cmd/dashboard/controller/server_nic_selection_test.go`（校验、落库、清空、
`Selected`/`Stale`、`Marshal`）。

`cmd/agent/monitor` 的 `TestGetHostCPUIsNonNil` 仍失败，原因同 P0（受限环境下
`host.Info()` 返回 nil 导致 `GetHost` 空指针），与本改造无关。

### P0 实施记录

实施范围与第四节一致，实际落地情况与偏差如下。

**已落地**

- `proto/Probe.proto`：新增 `NICCounter`，`State` 挂 `nic_counters = 11`。
- `model/host.go`：新增 `NICCounter`；`HostState.NICs []NICCounter \`json:"-"\``；`PB()` / `PB2State()` 双向映射。
- `model/server_nic.go`（新增）：`ServerNIC` 表。
- `cmd/agent/monitor/monitor.go`：抽出 `isVirtualNIC`；新增 `collectNICCounters`；`TrackNetworkSpeed` 写入 `nicSnapshot`；`GetState` 填充 `NICs`。
- `cmd/agent/main.go`：更新 `-n` 帮助文案。
- `service/dao/server_nic.go`（新增）：`syncServerNICs` / `UpsertServerNICs` / `ServerNICList` / `DeleteServerNICs`。
- `service/dao/dao.go`：`serverNICSeen` 去重缓存 + `InitServerRuntimeState` / `DeleteServerRuntime` 维护；`cloneState` 深拷贝 `NICs`；`ObserveServerMetric` 调用 `syncServerNICs`。
- `cmd/dashboard/main.go`：`AutoMigrate` 注册 `model.ServerNIC{}`。
- `cmd/dashboard/controller/member_api.go`：`GET /api/server/:id/nic`；删除服务器时清理网卡记录。
- `resource/template/component/server.html` + `resource/static/main.js` + `resource/static/main.css`：套餐规格 tab 的只读网卡清单。

**与设计的偏差**

1. **写库去重比设计更保守**：除「集合变化才写」外，还加了 `serverNICRefreshInterval = 10min`，
   集合长期不变时也定期推进 `last_seen`。否则稳定在用的网卡会被后台误判成「已消失」而被置灰。
2. **`syncServerNICs` 的调用点**：设计写在 `ObserveServerMetric` 里，实际即如此；它先于
   `serverMetricLock` 取 `serverNICSeenLock`，锁序为 `serverMetricLock → serverNICSeenLock`，
   无反向路径，不存在死锁。
3. **未做 `Supported` 之外的老 Agent 探测**：`GET /api/server/:id/nic` 的 `Supported` 直接取
   `len(List) > 0`，即「库里有没有网卡记录」，与设计一致。

**proto 生成的实际情况**

- `script/proto.sh` 保持原样（旧式 `plugins=grpc:`），未做方案 B 的工具链升级。
- 现有 `proto/Probe.pb.go` 由 **protoc-gen-go v1.25.0 + protoc v3.14.0** 生成，而该组合的
  `plugins=grpc` 支持在 protoc-gen-go v1.25 已被移除（v1.25 起 gRPC 由 `protoc-gen-go-grpc` 生成），
  本机无法复现。实际改用 **protoc-gen-go v1.23.0 + protoc 3.21.12** 重新生成，
  产物结构（`file_proto_Probe_proto_*` 命名、`grpc.SupportPackageIsVersion6`）与仓库现有文件一致，
  生成的 gRPC 接口签名不变。
- 由此产生的机械性 diff：`file_proto_probe_proto_*` → `file_proto_Probe_proto_*`（历史产物是手工改过大小写的），
  以及版本注释行变化。
- **附带删除**：原文件中有一行手工加入且**从未被引用**的
  `const defaultUserAgent = "Mozilla/5.0 (compatible; nodekeep/2.1; ...)"`，重新生成后消失。
  已确认全仓库无引用，属死代码。

**验证结果**

```
go build ./...   # 通过
go vet ./...     # 通过
go test ./...    # 除下述 1 项外全部通过
```

- `cmd/agent/monitor` 的 `TestGetHostCPUIsNonNil` **失败，且与本次改动无关**：
  该用例在**未改动的 HEAD 代码**上同样失败。根因是受限环境下 `host.Info()` 返回
  `getting boot time: operation not permitted` 且 info 为 nil，`GetHost` 随即空指针解引用。
  这是既有隐患（`GetHost` 未判空），不在本次范围内。
- 本机 cgo 依赖 `/usr/bin/clang` 被 Xcode 许可拦截，需要
  `CC=/Library/Developer/CommandLineTools/usr/bin/clang` +
  `SDKROOT=/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk` 才能跑 sqlite 相关测试。

## 一、背景与目标

### 现状

- Agent 侧过滤网卡：`cmd/agent/monitor/monitor.go:213` 的 `shouldIncludeNIC` 用 `excludedNICPrefixes`（`monitor.go:31`）前缀黑名单排除 `lo/docker/veth/...`，或按启动参数 `-n` 白名单（`cmd/agent/main.go:102`）只保留指定网卡。
- `sumNetworkCounters`（`monitor.go:201`）把入选网卡的 `BytesRecv/BytesSent` 求和，作为**单一聚合值**上报。
- 服务端完全看不到网卡名，只拿到聚合计数（`proto/Probe.proto` 的 `State.net_in_transfer/net_out_transfer`），再由 `ProcessServerTraffic`（`service/dao/traffic.go:122`）算增量、落库。

### 目标

1. 后台能拿到某个节点的网卡列表（含虚拟网卡，供人工判断）。
2. 管理员在后台勾选一个或多个网卡，这些网卡的流量才计入统计。
3. 改动要能兼容新旧 Agent / 新旧面板混跑，默认行为不变。

## 二、关键决策

### 决策 1：过滤放在服务端，Agent 上报每张网卡的原始计数（方案 A）

| 方案 | 做法 | 评价 |
|---|---|---|
| **A（已选）服务端过滤** | Agent 每 2s 上报**所有**网卡的原始计数，服务端按选中集合求和后再走现有增量逻辑 | 选择是纯服务端配置，**即时生效**、无需下发通道、离线节点也能改；代价是 `State` 报文变大（约 8 张网卡 × ~24B ≈ 200B，可忽略） |
| B Agent 过滤 | 服务端把选中列表下发给 Agent，Agent 侧 `SetNICAllowlist` | 报文不变，但要新增下发通道（`TaskTarget.Send`，`service/dao/dao.go:66`）、Agent 本地持久化、离线/重启/重连同步，复杂度显著更高 |

选 A。B 的唯一优势是省流量，而这里流量根本不是瓶颈。

> 注意：**A 和 B 都必须解决计数器跳变问题**（见第九节风险 1），所以 B 并不因此更省事。

### 决策 2：网卡「虚拟」标记由 Agent 判定，不下沉黑名单到服务端

Agent 复用现有 `shouldIncludeNIC` 逻辑，给每张网卡打一个 `virtual` 布尔标记。服务端默认口径 = 「所有非虚拟网卡」，与今天的行为**逐字节等价**，且不必把 `excludedNICPrefixes` 在服务端再抄一份。后台仍展示虚拟网卡，允许人工显式勾选（比如确实想统计 `docker0`）。

### 决策 3：配置存储位置

- **选中列表** → 新增 `model.Server.TrafficNICs string`（逗号分隔，空 = 自动）。
  **不放进 `ServerBilling.Extra`**：`serverForm.hasBillingData()`（`cmd/dashboard/controller/member_api.go:185`）会把任何非空计费字段当成「要建订阅」，用户只选网卡却凭空多出一条空订阅记录，语义也不对（这是监控配置，不是订阅信息）。
- **网卡清单**（Agent 上报的）→ 新增 `model.ServerNIC` 表，`uniqueIndex(server_id, name)`，字段 `Name / Virtual / LastSeen`。
  理由：面板重启后、Agent 离线时后台仍要能配置，所以必须持久化；独立表比 JSON blob 更好查询、能记录 `last_seen`（用于把已消失的网卡置灰），且与 `ServerMetric/ServerTrafficDaily` 的风格一致。`AutoMigrate` 已在 `cmd/dashboard/main.go:56` 统一注册，加一行即可。

## 三、协议改动（`proto/Probe.proto`）

只动 `State`，不动 `Host` —— 网卡清单可以从 `State` 的网卡集合自然推导，没必要再加一份。

```proto
message NICCounter {
    string name       = 1;
    uint64 bytes_recv = 2;
    uint64 bytes_sent = 3;
    bool   virtual    = 4;
}

message State {
    // ... 现有字段不动，11 号位空闲
    repeated NICCounter nic_counters = 11;
}
```

`net_in_transfer` / `net_out_transfer` **保留并继续上报**（老面板 / 降级路径要用）。

### proto 生成步骤

**现状**：`script/proto.sh` 只有一行 `protoc --go_out="plugins=grpc:." proto/*.proto`，是旧式写法（protoc-gen-go v1.25.0，messages + grpc 合并生成到单个 `proto/Probe.pb.go`）。本机 `protoc` 和 `protoc-gen-go` 都未安装，`$(go env GOPATH)/bin` 里只有 `gopls`。

**方案 A（推荐，本改动用）—— 保持旧式生成，diff 最小**

```sh
brew install protobuf                                           # 提供 protoc
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.25.0 # 与现有产物版本一致
cd /Users/rong/GolandProjects/nodekeep
PATH="$PATH:$(go env GOPATH)/bin" sh script/proto.sh
go build ./... && go test ./...
```

产物仍是单个 `proto/Probe.pb.go`，`proto.sh` 无需修改，`pb.NewProbeServiceClient` / `pb.RegisterProbeServiceServer`（`cmd/agent/main.go:155`、`cmd/dashboard/rpc/rpc.go:16`）签名不变。

**方案 B（后续单独做）—— 升级到现代工具链**

```sh
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
# script/proto.sh 改为：
# protoc --go_out=. --go_opt=paths=source_relative \
#        --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/*.proto
```

会拆成 `Probe.pb.go` + `Probe_grpc.pb.go` 两个文件；`github.com/golang/protobuf` 目前**只**被 `proto/Probe.pb.go:11` 引用，升级后可一并从 `go.mod` 移除。建议不要和本次功能改动混在一个 PR 里。

> `brew install` / `go install` 需要联网，需批准执行。

## 四、P0：管道打通 + 后台只读展示

### 协议

- `proto/Probe.proto` — 新增 `NICCounter` message，并在 `State` 上挂字段 11（现用 1、3~10，2 号位已废弃）。
- 重新生成 `proto/Probe.pb.go`（见第三节）。

### 数据模型

- `model/host.go`
  - 新增 `type NICCounter struct { Name string; BytesRecv uint64; BytesSent uint64; Virtual bool }`。
  - `HostState` 增加 `NICs []NICCounter`，**必须带 `json:"-"`**：`PublicServerRuntime.State` 是 `*HostState` 且直接 JSON 序列化给游客（`resource/template/theme-default/home.html:344` 的 `const initData = {{.Servers}}`），不打标签会把节点全部网卡名泄露给匿名访客。
  - `PB()`（`model/host.go:25`）与 `PB2State()`（`model/host.go:39`）双向映射 `NICs`。
- `model/server_nic.go`（新增）
  ```go
  type ServerNIC struct {
      Common
      ServerID uint64 `gorm:"uniqueIndex:idx_server_nic;index"`
      Name     string `gorm:"uniqueIndex:idx_server_nic"`
      Virtual  bool
      LastSeen time.Time
  }
  func (ServerNIC) TableName() string { return "server_nics" }
  ```
- `cmd/dashboard/main.go:56` — `AutoMigrate(...)` 列表里加 `model.ServerNIC{}`。

### Agent

- `cmd/agent/monitor/monitor.go`
  - 新增 `collectNICCounters(nc []net.IOCountersStat) []model.NICCounter`：遍历**全部**网卡，`Virtual` 复用现有前缀判定（把 `shouldIncludeNIC`（`monitor.go:213`）里的黑名单循环抽成 `isVirtualNIC(name string) bool` 供两处共用）；若 `-n` 白名单已设置，则只上报白名单内的网卡（当作硬上限）。
  - 用 `atomic.Value`（新增 `nicSnapshot`，与现有 `netInSpeed/netInTransfer` 等 `uint64` 原子变量并列，`monitor.go:21`）存最近一次明细，在 `TrackNetworkSpeed`（`monitor.go:168`）里写入。
  - `GetState`（`monitor.go:86`）填充 `HostState.NICs`；聚合字段 `netInTransfer/netOutTransfer/netInSpeed/netOutSpeed` 的计算逻辑**完全不动**。
  - `cmd/agent/main.go:102` 更新 `-n` 的帮助文案（说明一般不需要配，后台选即可）。
- Agent 侧**不需要**新增下发通道、不需要本地持久化配置。

### 服务端

- `service/dao/server_nic.go`（新增）：`UpsertServerNICs(serverID, counters, now)`、`ServerNICList(serverID)`、`ServerNICsOf(serverID)`。
- `service/dao/dao.go`
  - 新增 `serverNICSeen map[uint64]map[string]struct{}` + 独立 mutex（或复用 `serverMetricLock`），在 `InitServerRuntimeState`（`dao.go:74`）里初始化。
  - `ObserveServerMetric`（`dao.go:359`）：把本次上报的网卡名集合与 `serverNICSeen[serverID]` 比对，**仅集合变化时**才 upsert `ServerNIC`，避免每 2s 写库。
  - `cloneState`（`dao.go:105`）：目前是 `clone := *s` 浅拷贝，加 slice 字段后建议改成深拷贝 `clone.NICs = append([]NICCounter(nil), s.NICs...)`。当前因为每次上报都新建 slice、旧 slice 不再被写，还不构成竞态，但属于隐患。
- `cmd/dashboard/controller/member_api.go`
  - `serve()`（`member_api.go:26`）注册 `mr.GET("/server/:id/nic", ma.serverNICs)`。
  - 新增 `serverNICs`：返回 `{List: [{Name, Virtual, LastSeen}], Selected: [...], Supported: bool}`；`Supported = len(List) > 0`，老 Agent 为 `false`。

### 后台 UI（只读）

- `resource/template/component/server.html` — 「套餐规格」tab 的流量区块（`TrafficVol` 附近，`component/server.html:168`）加一个只读展示位 + 提示「该节点 Agent 版本过旧，未上报网卡明细」的占位。
- `resource/static/main.js` — `addOrEditServer`（`main.js:203`）里异步 `GET /api/server/:id/nic` 填充；虚拟网卡打「虚拟」标签。

## 五、P1：选中配置 + 服务端求和 + 基线重设

### 配置存储

- `model/server.go`
  - `Server` 增加 `TrafficNICs string`（逗号分隔，空 = 自动）。
  - `Marshal()` 带上该字段，供 `{{$server.Marshal}}`（`resource/template/dashboard/server.html`）回填。**不放进 `ServerBilling.Extra`**。
- `cmd/dashboard/controller/member_api.go`
  - `serverForm`（`member_api.go:143`）加 `TrafficNICs []string`（或 `string`）。
  - `hasBillingData()`（`member_api.go:185`）**不要**把 `TrafficNICs` 计入判定，否则只选网卡会凭空建出一条空订阅。
  - `addOrEditServer`（`member_api.go:278`）解析、去空白、去重、长度上限校验后写入 `s.TrafficNICs`。
  - 保存后无需额外钩子：`UpsertServerRuntime`（`dao.go:177`）的 `old.runtime.Server = s` 已经会刷新内存态。

### 服务端求和 + 基线重设（核心）

- `service/dao/dao.go`
  - `UpdateServerState`（`dao.go:259`）：持 `serverLock` 时读出 `s.runtime.Server.TrafficNICs`，随 `state` 一起传给 `ObserveServerMetric`（不引入第二份全局配置 map，也就不需要新的启动加载步骤）。
  - `ObserveServerMetric`：
    ```go
    selIn, selOut, ok := sumSelectedNICs(state.NICs, selected)
    if !ok { selIn, selOut = state.NetInTransfer, state.NetOutTransfer } // 降级：老 Agent
    ```
    `selIn/selOut` 同时喂给 `addServerMetricSample`（`dao.go:681`）和 `ProcessServerTraffic`。
  - 新增 `sumSelectedNICs` + `selectedNICKey(selected, counters)`（选中集合的稳定指纹，用于基线比对）。
  - 服务端重算速率：用 `ProcessServerTraffic` 返回的 `lastRawAt` + `selIn/selOut` 算 `delta/elapsed`，覆盖 `runtime.State.NetInSpeed/NetOutSpeed`，与 `NetInTransfer/NetOutTransfer` 被覆盖的处理方式（`dao.go:396`）一致。Agent 上报的 speed 是按它自己的集合算的，口径可能不一致，不能直接采信。
- `service/dao/traffic.go`
  - `serverTrafficTracker`（`traffic.go:20`）增加 `lastNICKey string` 与 `lastRawAt time.Time`。
  - `ProcessServerTraffic`（`traffic.go:122`）签名加 `nicKey string`；在函数开头比对 `tracker.lastNICKey`，**不一致时只把当前值当新基线、不累加增量**，并同步更新 `lastNICKey`。
  - `serverMetricBucket`（`dao.go:56`）的 `hasLastTransfer` 在同一时刻一并清零（否则分钟桶会算出一个假增量）。

### 后台 UI（可编辑）

- `resource/template/component/server.html` — 把 P0 的只读位换成多选：
  ```html
  <div class="field">
    <label>流量统计网卡</label>
    <select name="TrafficNICs" class="ui multiple search dropdown" multiple></select>
    <p class="nk-form-hint">留空 = 自动统计所有物理网卡（过滤 lo/docker/veth 等虚拟网卡）。仅影响之后的增量，历史数据不追溯。</p>
  </div>
  ```
- `resource/static/main.js` — 填充选项；**已消失的网卡仍保留在选项里并置灰**（否则保存会静默丢掉配置）；`Supported=false` 时禁用选择器。
- 可选：`resource/template/dashboard/server.html` 加一列显示当前选中网卡。

## 六、后台 API 与 UI 汇总

**API**

- `GET /api/server/:id/nic` → `{ List: [{Name, Virtual, LastSeen}], Selected: ["eth0","eth1"], Auto: bool, Supported: bool }`
  `Supported=false` 表示该节点 Agent 版本过旧、没有上报网卡明细，前端禁用选择器并给出提示。
  参照 `serverPayments`（`member_api.go:336`）的写法：弹窗打开时按需拉取，不塞进服务器列表页的 payload。

**UI**

- `resource/template/component/server.html` 的「套餐规格」tab（`TrafficVol` 附近，`component/server.html:168`）加多选。
- `resource/static/main.js:203 addOrEditServer` 里异步填充选项。
- 可选：服务器列表加一列显示当前选中网卡，便于一眼看出哪些节点是自定义口径。

## 七、兼容性与灰度

| Agent | 面板 | 行为 |
|---|---|---|
| 新 | 新 | 完整功能 |
| 新 | 旧 | 旧面板忽略 `nic_counters`，继续用聚合字段（Agent 仍按老黑名单/`-n` 过滤后上报），行为完全不变 |
| 旧 | 新 | 无网卡明细 → `Supported=false`，服务端降级用聚合字段，选择器禁用 |
| 任一 | 任一 | `-n` 参数语义不变，老安装命令（`main.js:447 buildAgentInstallCommand`）无需改动 |

## 八、测试清单

- `cmd/agent/monitor/monitor_test.go`：`isVirtualNIC` 判定；`collectNICCounters` 明细求和 == 聚合值；`-n` 白名单与全量上报的交互。
- `service/dao/traffic_test.go`：选中集合求和；**切换集合后不产生假增量**（P1 回归重点）；老 Agent 无明细时降级；跨天/跨周期行为不变。
- `service/dao/server_nic_test.go`（新增）：集合变化才写库、`LastSeen` 更新。
- `service/dao/server_public_snapshot_test.go`：在 `TestSortedPublicServerSnapshotRedactsSensitiveFields`（`server_public_snapshot_test.go:14`）里补断言，确认 `State` 的 JSON 不含网卡名。
- `cmd/dashboard/controller`：`GET /api/server/:id/nic` 的权限与返回；保存 `TrafficNICs`；`hasBillingData()` 不被网卡选择触发。
- 端到端：新 Agent + 新面板，勾选单网卡 → 观察 `Today/Cycle/Total` 增量与曲线口径一致。

### 验证命令

```sh
go build ./...
go test ./...
go vet ./...
```

## 九、风险与必须处理的坑

1. **选中集合变化会导致计数器跳变（必须修）**。求和口径一变，`rawIn/rawOut` 会阶跃；`positiveCounterDelta`（`dao.go:737`）在 `current < previous` 时会把 `current` 整个当成增量，缩小白名单就会一次性灌入几十 GB 假流量。所以 P1 的「指纹比对 + 重设基线」是**必需项**，不是优化项。
2. **同一个坑今天已经存在**：宿主机上 `docker0` 之类虚拟网卡中途出现/消失，同样会让聚合值阶跃并被计成流量。本次改造顺带把它修掉（指纹里带上实际参与求和的网卡名集合即可）。
3. **`HostState.NICs` 必须 `json:"-"`**，否则网卡名通过公开页面的 `initData` 泄露给匿名访客。
4. **历史数据不追溯**：切换口径只影响之后的增量，`Today/Cycle/Total` 里已有数字不会重算。UI 上要写清楚，否则会被当成 bug。
5. **proto 重新生成的 diff**：见第三节，需要单独验证。不要手改 `pb.go`（raw descriptor 字节串极易出错）。
6. **Windows 网卡名**是 GUID 风格，展示层建议同时显示 `net.Interface` 的描述（若后续需要，可在 `Host` 里补，不必现在做）。
7. **`State` 报文变大**：每 2s 多约 200B，可忽略，但要在压测里确认一下 gRPC 消息大小没触发限制（默认 4MB，远未触及）。

## 十、建议分期

- **P0**：proto + Agent 上报明细 + 服务端清单入库 + 后台只读展示。先把管道跑通，验证多平台网卡名与虚拟标记是否靠谱。
- **P1**：选中配置 + 服务端求和 + 基线重设 + UI 选择器 + 速率重算。功能闭环。
- **P2（可选）**：节点详情页展示每网卡速率曲线；告警规则支持按单网卡设阈值。
