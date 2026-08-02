# Relay Panel 线路监控改造方案

## 1. 本次目标

把线路列表和线路详情页改成新的监控结构，参考 3x-ui 现有的流量统计、速度展示和出站检测思路，但不要做复杂大盘。

本次只改两类核心线路：

- Cloudflare 主线路
- Reality 直连

Trojan 暂时不继续扩展。

## 2. 最终页面结构

### 2.1 线路列表页

列表页只做快速判断，不展示完整链路结构。

表格列固定为：

```text
状态 | 线路 | 实时吞吐 | 入站延迟 | 出站延迟 | 累计流量 | 操作
```

说明：

- `实时吞吐`：分两行显示，不写“上行/下行”汉字。

```text
↑ 128 KB/s
↓ 1.4 MB/s
```

- `入站延迟`：用户侧访问线路入口的延迟。
- `出站延迟`：服务器通过住宅出口访问测试网站的延迟。
- `累计流量`：该线路累计使用流量。
- `操作`：保留 `检测`、`分享`、`详情`。

列表页不再显示：

- 入口地址
- 链路结构
- 复杂检测详情

### 2.2 线路详情页

详情页按已经确认喜欢的第二张图来做。

页面结构：

```text
返回线路列表
线路名称
线路简短结构

运行状态
链路结构
三段监控
检测记录
```

顶部操作按钮：

```text
检测 | 分享 | 保存并应用
```

详情页展示完整链路结构，例如：

```text
手机/客户端 -> Cloudflare -> Nginx -> Xray 入站 line-1-in -> Xray 出站 line-1-out -> 住宅代理 -> 测试网站
```

三段监控：

```text
第1段 入站侧：用户/CDN -> 服务器入口
第2段 出站侧：服务器 -> 住宅代理
第3段 目标访问：住宅代理 -> 测试网站
```

## 3. 延迟检测策略

### 3.1 不做自动延迟轮询

延迟不做 30 秒自动检测，也不做实时滚动检测。

原因：

- 延迟检测会主动访问测试地址。
- 频繁检测会增加住宅出口请求。
- 频繁检测可能让延迟数据看起来很热闹，但实际价值不高。
- 出站检测过于频繁也可能影响代理服务稳定性。

最终策略：

```text
只有用户点击“检测”时才检测延迟。
```

列表页和详情页只显示：

```text
上一次检测结果
```

如果从未检测过：

```text
--
```

### 3.2 入站延迟

入站延迟表示：

```text
用户侧 -> 线路入口
```

注意：服务器自己测自己，不能代表真实用户到服务器的延迟。

更稳的做法：

- 前端在浏览器里点击 `检测` 时，请求一个轻量接口。
- 用浏览器实际请求耗时近似表示入站延迟。
- Cloudflare 主线路测到的是 `浏览器 -> Cloudflare -> 源站面板接口` 的实际访问耗时。
- Reality 直连线路无法完全用浏览器模拟代理协议入口，可以先显示面板访问入口检测结果，后续再考虑客户端配合。

第一版建议：

```text
入站延迟 = 浏览器点击检测时访问面板检测接口的耗时
```

如果这个定义后面觉得不准确，再单独升级。

### 3.3 出站延迟

出站延迟表示：

```text
服务器 -> 住宅出口 -> 测试网站
```

这个由服务器检测，比较准确。

检测动作：

- 找到线路的出站 tag：`line-{id}-out`
- 用该出站访问测试网站
- 返回访问耗时

建议默认测试网站：

```text
https://www.gstatic.com/generate_204
```

后续可以在配置里允许用户改。

## 4. 参考 3x-ui 的能力

### 4.1 实时吞吐

参考 3x-ui 的 Xray stats 流量机制：

- 后端定时从 Xray API 获取 traffic delta。
- Xray 返回的是流量差值。
- 前端按固定间隔换算成速度。
- 3x-ui 前端已有类似 `↑ / ↓` 的速度展示组件。

Relay Panel 不需要照搬所有页面，只借用核心思想。

### 4.2 累计流量

参考 3x-ui 的 inbound/outbound traffic 统计方式。

Relay Panel 每条线路已有稳定 tag：

```text
入站 tag：line-{id}-in
出站 tag：line-{id}-out
```

所以可以按 tag 聚合流量。

### 4.3 出站检测

参考 3x-ui 的 outbound test / observatory 思路。

但本项目不要做自动 observatory 轮询，第一版只做点击检测。

## 5. 后端修改方案

### 5.1 新增线路监控数据结构

建议新增结构，例如：

```go
type LineMetrics struct {
    LineID             int    `json:"lineId"`
    InboundTag         string `json:"inboundTag"`
    OutboundTag        string `json:"outboundTag"`
    InboundSpeedUp     int64  `json:"inboundSpeedUp"`
    InboundSpeedDown   int64  `json:"inboundSpeedDown"`
    OutboundSpeedUp    int64  `json:"outboundSpeedUp"`
    OutboundSpeedDown  int64  `json:"outboundSpeedDown"`
    TotalTraffic       int64  `json:"totalTraffic"`
    InboundLatencyMs   int64  `json:"inboundLatencyMs"`
    OutboundLatencyMs  int64  `json:"outboundLatencyMs"`
    LastCheckedAt      int64  `json:"lastCheckedAt"`
}
```

第一版列表页的 `实时吞吐` 建议显示入站侧速度：

```text
inboundSpeedUp / inboundSpeedDown
```

详情页可以同时展示入站和出站。

### 5.2 新增接口

建议新增：

```text
GET  /panel/api/lines/metrics
GET  /panel/api/lines/{id}/metrics
POST /panel/api/lines/{id}/check
```

接口职责：

- `/lines/metrics`：给线路列表页读取所有线路的上次监控数据。
- `/lines/{id}/metrics`：给线路详情页读取单条线路监控数据。
- `/lines/{id}/check`：用户点击检测时执行延迟检测，并刷新检测记录。

### 5.3 检测接口行为

用户点击某一行 `检测`：

```text
POST /panel/api/lines/{id}/check
```

后端执行：

1. 找到线路。
2. 校验线路是否已经应用。
3. 获取 `line-{id}-in` 和 `line-{id}-out`。
4. 检测出站延迟。
5. 保存检测结果。
6. 返回最新线路详情和 metrics。

不要在后台定时跑延迟检测。

### 5.4 数据保存

建议在 `LineProfile` 上新增几个字段，或者新增单独表。

第一版为了简单，可以先放在 `LineProfile`：

```go
LastInboundLatencyMs  int64
LastOutboundLatencyMs int64
LastCheckedAt         int64
```

流量数据可以不一定写入 `LineProfile`，优先从现有 traffic 表按 tag 读取。

如果后续需要历史曲线，再新增独立表。

## 6. 前端修改方案

### 6.1 主要修改文件

```text
frontend/src/pages/lines/LinesPage.tsx
frontend/src/pages/lines/LinesPage.css
```

为了减少 `LinesPage.tsx` 继续变大，建议新增组件：

```text
frontend/src/pages/lines/components/LineSpeedCell.tsx
frontend/src/pages/lines/components/LineChainDiagram.tsx
frontend/src/pages/lines/components/LineMonitorCards.tsx
frontend/src/pages/lines/components/LineMetricText.tsx
```

### 6.2 列表页改法

当前列表页需要调整：

- 去掉 `链路结构` 列。
- 去掉 `入口` 列。
- 新增 `实时吞吐` 列。
- 新增 `入站延迟` 列。
- 新增 `出站延迟` 列。
- `累计流量` 放到延迟后面。

表格列顺序：

```text
状态
线路
实时吞吐
入站延迟
出站延迟
累计流量
操作
```

`实时吞吐` 组件显示：

```tsx
<LineSpeedCell up={metrics.inboundSpeedUp} down={metrics.inboundSpeedDown} />
```

渲染效果：

```text
↑ 128 KB/s
↓ 1.4 MB/s
```

### 6.3 详情页改法

详情页保留现有：

- 返回线路列表
- 编辑
- 检测
- 分享
- 保存并应用
- 高级部署信息

新增或调整：

- `运行状态`：展示状态、入口、累计流量、入站延迟、出站延迟。
- `链路结构`：使用黑线节点图。
- `三段监控`：三个小卡片。
- `检测记录`：复用现有 logs/checks，展示最近检测结果。

### 6.4 检测按钮

点击 `检测` 时：

1. 前端记录当前时间。
2. 请求 `/panel/api/lines/{id}/check`。
3. 请求返回后计算浏览器耗时，作为入站延迟候选值。
4. 后端返回出站延迟。
5. 更新列表和详情页缓存。

注意：

- 不做 setInterval 自动检测延迟。
- 页面刷新后显示上一次保存的检测结果。
- 如果检测失败，显示 `异常`，并在检测记录中写明失败原因。

## 7. 本地修改步骤

### 7.1 准备分支

```powershell
cd D:\tizi\一键部署\一键部署面板
git status --short
git switch -c feature/line-metrics-manual-check
```

如果分支已存在：

```powershell
git switch feature/line-metrics-manual-check
```

### 7.2 后端先改

建议顺序：

1. 看 `internal/database/model/line.go`，确认 `LineProfile` 当前字段。
2. 增加保存上次延迟检测结果的字段。
3. 看 `internal/web/controller/line.go`，增加 metrics/check 返回结构。
4. 看 `internal/web/service/line.go`，增加线路 metrics 聚合方法。
5. 复用现有 Xray traffic/outbound 检测能力。
6. 补测试。

后端优先保证：

- 没有应用的草稿线路不会乱测。
- Xray 没运行时返回清楚错误。
- 住宅出口不可用时显示出站异常。
- 检测失败也要写入检测记录。

### 7.3 前端再改

建议顺序：

1. 新增 `LineSpeedCell`。
2. 调整列表 columns。
3. 新增 metrics 查询。
4. 调整检测按钮 mutation。
5. 重做详情页布局。
6. 调整 CSS。

不要一次性大改所有 UI，先让列表页稳定，再做详情页。

### 7.4 本地检查

```powershell
cd D:\tizi\一键部署\一键部署面板
.\scripts\check-dev.ps1
```

如果只想先看前端：

```powershell
cd D:\tizi\一键部署\一键部署面板\frontend
npm.cmd run typecheck
npm.cmd run build
```

如果只想先看后端：

```powershell
cd D:\tizi\一键部署\一键部署面板
go test ./internal/web/controller ./internal/web/service
```

## 8. Git 发布流程

本地检查通过后：

```powershell
cd D:\tizi\一键部署\一键部署面板
git status --short
git add .
git commit -m "Add manual line metrics monitoring"
git push origin feature/line-metrics-manual-check
```

合并到主分支后创建 tag。

如果当前线上是 `v2.0.11`，下一版建议：

```powershell
git tag v2.0.12
git push origin v2.0.12
```

如果这次改动比预期大，也可以用：

```powershell
git tag v2.1.0
git push origin v2.1.0
```

## 9. 服务器升级流程

服务器上升级时明确指定版本，不要用不带版本号的更新命令。

示例：

```bash
relay-panel update v2.0.12
```

升级后检查：

```bash
relay-panel version
systemctl status line-panel --no-pager
line-panel check
ss -ltnp | grep -E ':2053|:2096|:443|:8443'
```

再打开网页确认：

- 线路列表新列正常。
- 点击检测后延迟才刷新。
- 不点击检测时不会自动刷新延迟。
- 实时吞吐能随流量变化。
- 累计流量能正常显示。
- 分享二维码正常。
- 已有两条线路没有被破坏。

## 10. 风险点

### 10.1 入站延迟定义容易误解

入站延迟不是服务器自己 ping 自己。

第一版建议先定义为：

```text
浏览器点击检测接口的耗时
```

这是用户实际访问面板的耗时近似值。

### 10.2 出站延迟不要自动测

不要加 30 秒自动检测。

只在点击检测时测。

### 10.3 不要破坏现有线路

改 metrics 和 UI 时，不要动：

- Cloudflare 线路生成逻辑
- Reality 线路生成逻辑
- share link 生成逻辑
- Nginx apply 逻辑
- Xray apply 逻辑

除非测试发现必须改。

### 10.4 safe apply 仍然要注意

之前已经发现 `ApplyLine` 的失败状态和失败日志可能因为事务回滚而丢失。

这次如果碰到 apply 相关代码，必须顺手确认：

- 应用失败时状态能持久显示为 `apply_failed`
- 失败原因能在页面看到
- Nginx 或 Xray 应用失败不能静默成功

## 11. 推荐执行顺序

第一阶段：

```text
后端 metrics 数据结构
后端 check 接口
前端列表页新列
```

第二阶段：

```text
详情页链路结构
详情页三段监控
检测记录优化
```

第三阶段：

```text
本地检查
Git tag
服务器升级
真实线路验证
```

这样改最稳，不容易一次性改炸。
