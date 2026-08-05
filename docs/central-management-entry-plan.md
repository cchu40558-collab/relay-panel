# 子站 RP Console 接入改造方案

## 1. 文档目的

本文说明如何修改 **Relay Panel 子站程序**，使每台子站能够安全接入 RP Console 总站。

本文是开发和部署方案，不是已经执行的操作。完成改造、发布新子站版本并升级服务器后，用户才能在总站添加这台子站。

当前验证环境：

| 项目 | 当前值 |
| --- | --- |
| 子站服务器 | `153.75.235.141` |
| 当前子站版本 | `v2.0.18` |
| 子站随机管理路径 | `/otJusMQxf1caAFzjHk7pVC` |
| 子站程序当前监听 | HTTP `:2053` |
| 当前主线路 HTTPS | `interserver02.wakeup-ai.top:8443` |
| 新管理域名 | `rp2.wakeup-ai.top` |
| 新管理 HTTPS 端口 | `2083` |

## 2. 已确认的问题

### 2.1 子站 HTTP 管理入口不能直接给总站使用

子站面板当前使用 HTTP `:2053`。总站请求会携带“总站只读令牌”，HTTP 会明文传输该令牌，因此总站必须拒绝 HTTP 子站。

### 2.2 现有主线路 HTTPS 不能代理管理面板

现有 `interserver02.wakeup-ai.top:8443` 的 Nginx 配置仅代理：

```text
/line-1-ws                                  -> Xray 本地端口
/otJusMQxf1caAFzjHk7pVC/rp/sub/             -> 订阅接口
```

它没有代理完整管理路径：

```text
/otJusMQxf1caAFzjHk7pVC/                    -> 127.0.0.1:2053
```

所以总站访问管理 API 时收到 Nginx `404`。

### 2.3 随机管理路径与总站 API 认证不兼容

子站已有总站只读 API 的代码和数据模型，但认证判断只识别根路径：

```text
/panel/api/central/capabilities
/panel/api/central/summary
/panel/api/central/lines
```

当子站启用随机管理路径后，请求实际为：

```text
/otJusMQxf1caAFzjHk7pVC/panel/api/central/capabilities
```

当前中间件没有先去掉已配置的管理路径，因而不会把请求认定为总站只读 API，最终返回 `404`。

### 2.4 子站界面没有可见的总站入口

当前部署的精简子站导航中没有“设置/安全”入口。即使后端已有 `CentralAccessToken` 数据模型，用户也无法在正常界面生成、禁用或删除总站只读令牌。

## 3. 改造目标

改造后的每台子站应具备以下能力：

1. 在子站网页中有明确的 **总站接入** 页面，不需要寻找隐藏的安全菜单。
2. 管理员可填写独立管理域名和 HTTPS 端口，例如 `rp2.wakeup-ai.top:2083`。
3. 管理员可在页面上传 Cloudflare Origin Certificate 和 Private Key；首次应用必填，后续不重新上传则保留原证书。
4. 子站自动创建自己的 Nginx HTTPS 管理站点，转发到本机 `127.0.0.1:2053`。
5. 子站自动校验证书、测试 Nginx、重载 Nginx；失败时恢复旧证书、旧配置和旧防火墙状态。
6. 子站自动提供只读令牌的创建、禁用和删除，并只允许令牌访问三个固定 API。
7. 总站只使用 HTTPS 加密请求，绝不支持 HTTP 降级。
8. 主线路、Reality、订阅、Xray 和现有 Nginx 线路配置不受影响。

## 4. 最终访问结构

```text
RP Console 总站
    |
    | HTTPS + Bearer 总站只读令牌
    v
Cloudflare: rp2.wakeup-ai.top:2083
    |
    | HTTPS，Full (strict)
    v
子站 Nginx: 2083
    |
    | 仅本机 HTTP 转发
    v
Relay Panel: 127.0.0.1:2053
    |
    +-- /随机路径/panel/api/central/capabilities
    +-- /随机路径/panel/api/central/summary
    +-- /随机路径/panel/api/central/lines
```

`2053` 不再应作为公网管理入口。Xray 的 `443`、主线路的 `8443` 和新管理入口的 `2083` 彼此独立。

## 5. 操作边界

### 5.1 用户在 Cloudflare 完成的操作

为每台子站添加一条 DNS A 记录。例如：

| 字段 | 值 |
| --- | --- |
| Name | `rp2` |
| Type | `A` |
| IPv4 | `153.75.235.141` |
| Proxy status | 已代理，橙色云朵 |
| TTL | 自动 |

本次示例 DNS 已创建。Cloudflare 橙色云朵支持 `2083` 作为 HTTPS 端口。

### 5.2 用户在云厂商控制台完成的操作

为子站所在 VPS 的云防火墙新增一条入站规则：

| 协议 | 端口 | 来源 |
| --- | --- | --- |
| TCP | `2083` | `0.0.0.0/0` |

这是云厂商的第一层防火墙，子站程序无法替用户修改。不要新增 `2053/tcp` 公网规则。

### 5.3 子站 RP 程序完成的操作

子站程序负责：

- 保存总站接入配置，但绝不把私钥原文存进数据库。
- 写入本程序专用的证书目录和 Nginx 配置文件。
- 检查、应用和回滚 Nginx。
- UFW 已启用时放行 `2083/tcp`。
- 创建和管理总站只读令牌。
- 修复带随机管理路径时的总站 API 认证。
- 将面板 HTTP 监听收口到 `127.0.0.1:2053`。

## 6. 新增子站页面设计

在子站左侧导航新增一级菜单：

```text
总站接入
```

不要放到隐藏菜单或仅放在旧版“安全”页内。页面分为三个区块。

### 6.1 区块一：HTTPS 管理入口

字段：

| 字段 | 输入规则 | 示例 |
| --- | --- | --- |
| 管理域名 | 必填，只填域名，不填协议、端口、路径 | `rp2.wakeup-ai.top` |
| 公网 HTTPS 端口 | 必填，默认 `2083`，范围 `1-65535` | `2083` |
| 随机管理路径 | 只读显示，来自现有 `XUI_INIT_WEB_BASE_PATH` | `/otJusMQxf1caAFzjHk7pVC` |
| Origin Certificate | 首次必传 PEM；后续留空保留旧值 | `origin.crt.txt` |
| Origin Private Key | 首次必传 PEM；后续留空保留旧值 | `origin.key.txt` |

按钮：

```text
保存并应用
停用 HTTPS 管理入口
```

页面状态必须显示：

- 当前域名和端口。
- 当前证书到期时间和 SHA-256 指纹前缀。
- 当前 Nginx 配置路径。
- 上次应用时间。
- 最新应用成功或失败原因。

私钥上传后只能用于写入服务器专用 TLS 文件，网页刷新、详情接口和诊断日志都不得回显私钥。

### 6.2 区块二：总站只读令牌

字段和动作：

| 功能 | 行为 |
| --- | --- |
| 令牌名称 | 例如 `RP Console`，用于管理员识别 |
| 生成总站只读令牌 | 创建 48 字符随机令牌，只显示原文一次 |
| 复制 | 仅在刚创建弹窗中可复制 |
| 启用/停用 | 停用后总站下一次请求立即失效 |
| 删除 | 删除后不可恢复，本站不能再读取该令牌 |

令牌数据库中只保存 SHA-256 哈希、创建时间、最后使用时间、最后来源 IP 和启用状态，不保存可恢复的令牌原文。

### 6.3 区块三：连接信息

成功应用后显示可复制但不含令牌的固定信息：

```text
管理域名：rp2.wakeup-ai.top
端口：2083
管理基础路径：/otJusMQxf1caAFzjHk7pVC
总站 API 基址：https://rp2.wakeup-ai.top:2083/otJusMQxf1caAFzjHk7pVC
```

用户在总站添加服务器时只需按此信息填写，令牌由区块二单独复制。

## 7. 后端数据模型

新增独立模型 `CentralManagementEndpoint`。不要复用线路 Profile，也不要把管理入口绑定到某一条 Cloudflare 主线路。

建议字段：

| 字段 | 说明 |
| --- | --- |
| `Enabled` | 是否启用 HTTPS 管理入口 |
| `Domain` | 管理域名，标准化为小写 |
| `Port` | Nginx HTTPS 监听端口 |
| `BasePath` | 从服务器实际管理路径读取，仅用于展示和 Nginx 路由 |
| `CertificatePath` | 服务器证书绝对路径，不保存 PEM 到数据库 |
| `KeyPath` | 服务器私钥绝对路径，不保存 PEM 到数据库 |
| `CertificateSHA256` | 证书 DER 指纹 |
| `CertificateNotAfter` | 证书到期时间 |
| `AppliedAt` | 最近一次成功应用时间 |
| `LastError` | 最近失败的简短、安全错误信息 |

数据库迁移必须保持幂等。没有此记录的旧子站仍按现有方式运行，不得因升级自动改写 Nginx 或打开端口。

## 8. 证书与文件路径

新增独立目录，不能借用或删除线路的证书目录：

```text
/etc/line-panel/central-management/
├── origin.crt       root:root 0644
└── origin.key       root:root 0600
```

Nginx 配置文件固定为：

```text
/etc/nginx/conf.d/line-panel-central-management.conf
```

这样用户点击任意线路的“重新应用”、停用线路或删除线路时，都不会覆盖总站管理入口。反过来停用总站管理入口也不能删除任何线路 Nginx 文件、Xray 入站、订阅路径或线路证书。

## 9. Nginx 配置生成

管理入口使用独立 server block。示例：

```nginx
server {
    listen 2083 ssl http2;
    server_name rp2.wakeup-ai.top;

    ssl_certificate /etc/line-panel/central-management/origin.crt;
    ssl_certificate_key /etc/line-panel/central-management/origin.key;
    ssl_protocols TLSv1.2 TLSv1.3;

    location ^~ /otJusMQxf1caAFzjHk7pVC/ {
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_pass http://127.0.0.1:2053;
    }

    location / {
        return 404;
    }
}
```

要求：

- 只代理当前随机管理路径，根路径及其他路径返回 `404`。
- 不添加 Xray WebSocket、订阅、线路入站或其他无关 location。
- 不监听 `443`，因为当前子站 Xray 已占用 `443`。
- 不修改现有 `8443` 主线路 Nginx 文件。
- 应检查管理端口与现有 TCP 监听是否冲突；冲突时拒绝应用并提示用户选择其他 Cloudflare 支持的 HTTPS 端口。

若 Cloudflare 仍保持橙色云朵，端口必须限制为 Cloudflare 支持的 HTTPS 端口：

```text
443、2053、2083、2087、2096、8443
```

## 10. “保存并应用”后端步骤

此动作必须可回滚，不能先删除旧配置再尝试新配置。

### 10.1 前置验证，不写入服务器

1. 校验域名格式，拒绝 URL、路径、端口、通配符、空格和内网地址写法。
2. 校验端口范围、端口冲突和 Cloudflare HTTPS 端口白名单。
3. 读取当前随机管理路径；为空或 `/` 时拒绝启用，要求先配置随机路径。
4. 首次启用时要求同时上传证书和私钥。
5. 用结构化 TLS 解析验证 PEM、证书私钥配对、未过期和证书 SAN 覆盖管理域名。
6. 后续更新若没有新文件，读取现存证书并重新验证其域名和有效期。
7. 检查 Nginx 可执行文件和 Linux 环境。

### 10.2 快照旧状态

在任何写入前记录：

1. 原管理 Nginx 配置是否存在及其完整内容。
2. 原证书和私钥是否存在及其完整内容、权限。
3. UFW 是否启用，`2083/tcp` 是否原本已允许。
4. 原数据库 `CentralManagementEndpoint` 记录。

快照仅保存在服务器内存或受限临时目录。私钥不得进入诊断日志、普通数据库字段或浏览器返回内容。

### 10.3 应用步骤

1. 使用临时文件原子写入新证书、私钥和 Nginx 配置。
2. 设置证书 `0644`、私钥 `0600`、目录 `0700`。
3. 执行 `nginx -t`。
4. UFW 启用时执行幂等放行 `ufw allow 2083/tcp`。
5. 重载 Nginx：优先 `systemctl reload nginx`，失败时使用 `service nginx reload`。
6. 使用本机 HTTPS 连通性检查确认 Nginx 监听了目标端口，并且随机管理路径可到达 Relay Panel。
7. 所有步骤成功后才更新数据库状态和页面成功事件。

### 10.4 任一步失败时的回滚

1. 恢复旧证书、私钥和管理 Nginx 文件。
2. 执行 `nginx -t` 并重载旧 Nginx 配置。
3. 仅当本次新增了 UFW 规则且旧状态没有该规则时，删除本次新增规则。
4. 恢复旧数据库记录。
5. 页面显示具体阶段，例如“证书不匹配”“端口已占用”“Nginx 校验失败”“云防火墙需手工放行”。
6. 诊断日志记录阶段、时间和已执行回滚，不记录私钥或令牌原文。

## 11. 收口 HTTP `2053`

新增配置或环境变量，使子站网页服务绑定：

```text
127.0.0.1:2053
```

不要继续绑定 `0.0.0.0:2053` 或 `[::]:2053`。执行顺序：

1. 先成功应用新 HTTPS 管理入口并完成本机连通性检查。
2. 再把 Relay Panel HTTP 服务改为仅本机监听。
3. 重启 `line-panel.service`。
4. 检查 `ss -lntp`，确认 `2053` 只显示 `127.0.0.1:2053` 或 `[::1]:2053`。
5. 在云防火墙中删除旧的公网 `2053/tcp` 规则；UFW 启用时也删除 `2053/tcp` 放行规则。

如果 HTTPS 管理入口未成功，不能收口 `2053`，否则管理员可能失去面板访问能力。

## 12. 总站只读 API 修复

### 12.1 路径归一化

修改 `checkAPIAuth` 和 `isCentralReadOnlyPath`：先根据服务器已配置的随机管理路径移除前缀，再对标准 API 路径做精确匹配。

必须支持：

```text
/panel/api/central/capabilities
/otJusMQxf1caAFzjHk7pVC/panel/api/central/capabilities
```

不得使用宽泛的字符串包含判断，例如只要 URL 中出现 `central` 就放行。路由本身和归一化后的路径都必须精确匹配三个固定 GET 端点。

### 12.2 令牌权限边界

总站只读令牌仅允许：

```text
GET /panel/api/central/capabilities
GET /panel/api/central/summary
GET /panel/api/central/lines
```

以下请求必须拒绝：

- 所有 POST、PUT、PATCH、DELETE。
- 线路部署、线路编辑、重新应用、Xray 重启。
- 用户管理、订阅管理、系统设置和普通 API。
- 普通管理员 API Token 访问总站专用 API。

无令牌、错误令牌、已停用令牌：返回 `403`，而不是向外暴露更多子站信息。

## 13. 后端接口设计

新增或整理以下管理接口，均要求已登录子站管理员并通过 CSRF 校验：

```text
GET  /panel/api/setting/centralManagement
POST /panel/api/setting/centralManagement/apply
POST /panel/api/setting/centralManagement/disable

GET  /panel/api/setting/centralAccessTokens
POST /panel/api/setting/centralAccessTokens/create
POST /panel/api/setting/centralAccessTokens/setEnabled/:id
POST /panel/api/setting/centralAccessTokens/delete/:id
```

`apply` 使用 multipart/form-data 接收证书和私钥。返回值只包含域名、端口、证书到期时间、指纹、应用时间、配置路径和安全错误信息。

## 14. 测试计划

### 14.1 单元测试

新增或扩展测试覆盖：

1. 根路径和随机管理路径都能识别三条总站 API。
2. 其他相似路径、POST 请求、普通 API Token 均被拒绝。
3. 创建令牌只返回一次明文，后续列表没有令牌原文。
4. 域名、端口、证书 PEM、私钥配对和 SAN 校验。
5. 端口冲突、无随机路径、非 Cloudflare HTTPS 端口拒绝。
6. Nginx 成功写入、`nginx -t` 失败回滚、reload 失败回滚。
7. UFW 本来关闭、已允许、首次新增、失败恢复四种状态。
8. 停用管理入口不影响线路 Nginx 文件和线路证书。

本地运行：

```powershell
cd D:\tizi\一键部署\一键部署面板
go test ./...
```

### 14.2 服务器验收

在测试子站升级后，依次确认：

```bash
relay-panel version
relay-panel status
ss -lntp | grep -E ':(2053|2083|8443|443)\b'
sudo nginx -t
```

预期：

- `2083` 由 Nginx 监听。
- `2053` 仅监听 `127.0.0.1`。
- `8443` 主线路继续由现有 Nginx 配置监听。
- `443` 继续由 Xray 使用。
- `nginx -t` 成功。

### 14.3 API 验收

在总站服务器或其他外部位置执行：

```bash
curl -i \
  -H 'Authorization: Bearer 子站总站只读令牌' \
  https://rp2.wakeup-ai.top:2083/otJusMQxf1caAFzjHk7pVC/panel/api/central/capabilities
```

预期 HTTP `200` 和 JSON 成功响应。

随后分别测试 `summary`、`lines`；无令牌和错误令牌必须为 `403`。

### 14.4 线路回归验收

1. 现有 Cloudflare 主线路连接成功。
2. 订阅地址继续可用。
3. Reality 线路不受影响。
4. 重新应用现有主线路后，`line-panel-central-management.conf` 仍存在且 Nginx 配置有效。
5. 停用或删除现有线路后，独立管理配置和令牌不受影响。

## 15. 发布步骤

子站当前版本为 `v2.0.18`。本次子站功能变更建议发布为：

```text
v2.0.19
```

发布顺序：

1. 本地修改后更新 `internal/config/version` 为 `2.0.19`。
2. 执行 Go 测试、前端测试和构建。
3. 检查 `git diff --check`。
4. 仅提交子站 RP 的程序代码、测试和必要文档。
5. 推送 `main`。
6. CI 通过后创建并推送标签 `v2.0.19`。
7. 在单台测试子站升级：

```bash
sudo relay-panel update v2.0.19
```

8. 完成第 14 节全部验收后，再升级其他子站。

不要将未发布的 `main` 直接用于生产服务器升级。

## 16. 用户在测试子站上的最终操作

在子站升级且云厂商防火墙已放行 `2083/tcp` 后：

1. 打开子站 RP 页面，进入 **总站接入**。
2. 填写管理域名：`rp2.wakeup-ai.top`。
3. 填写端口：`2083`。
4. 上传覆盖 `rp2.wakeup-ai.top` 的 Cloudflare Origin Certificate 和 Private Key。
5. 点击 **保存并应用**，等待成功状态。
6. 确认 Cloudflare SSL/TLS 为“完整（严格）”。
7. 在 **总站只读令牌** 区域，新建名为 `RP Console` 的令牌并立即复制。
8. 打开 RP Console 总站，添加服务器并填写：

```text
名称：02-inter
管理 IP 或域名：rp2.wakeup-ai.top
协议：HTTPS
端口：2083
管理基础路径：/otJusMQxf1caAFzjHk7pVC
有效期起：实际启用日期
有效期止：长期则留空
子站总站只读令牌：刚生成的令牌
```

9. 保存后在总站执行检测。总站显示绿色、可读取线路数和累计流量，才算接入完成。

## 17. 回退策略

### 17.1 单次“保存并应用”失败

程序自动恢复旧证书、旧 Nginx 文件和旧 UFW 状态。用户不需要手动复制配置文件，只需根据页面错误修正域名、端口或证书后重试。

### 17.2 子站版本升级失败

使用现有子站维护命令查看状态和回退。具体命令以子站升级脚本实际提供的命令为准；回退后应立即检查：

```bash
relay-panel version
relay-panel status
sudo nginx -t
```

### 17.3 管理入口配置成功但总站无法连接

按顺序检查：

1. Cloudflare DNS `rp2` 是否为橙色云朵并指向正确 IPv4。
2. 云厂商防火墙是否放行 TCP `2083`。
3. 子站 Nginx 是否监听 `2083`。
4. Cloudflare 是否为“完整（严格）”。
5. 管理基础路径是否与子站显示的随机路径完全一致。
6. 总站只读令牌是否仍启用。
7. 总站 URL 是否使用 `https://rp2.wakeup-ai.top:2083`，而不是 HTTP、IP 或 `2053`。

## 18. 明确不做的事情

- 不让总站降级使用 HTTP。
- 不复用普通管理员 API Token 作为总站令牌。
- 不把管理入口塞入线路 Nginx 配置文件。
- 不改动 Xray `443`、现有主线路 `8443`、Reality 或订阅配置。
- 不把 Origin Private Key、总站令牌原文写进数据库、诊断日志或浏览器列表接口。
- 不自动修改云厂商防火墙。
