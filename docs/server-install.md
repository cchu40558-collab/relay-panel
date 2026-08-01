# 服务器安装框架说明

这一步的目标是：服务器只执行一条安装命令，就把定制面板、Xray、Nginx 基础环境准备好。然后你再打开网页，在里面填写线路参数。

## 当前安装脚本

脚本位置：

```bash
scripts/install-server.sh
```

它会做这些事：

- 安装基础依赖：`curl`、`git`、`gcc/g++`、`make`、`unzip`、`nginx` 等。
- 检查并安装 Go。
- 检查并安装 Node 24。
- 从 Git 仓库拉取面板代码。
- 构建前端页面。
- 编译后端二进制。
- 创建 systemd 服务：`line-panel`。
- 创建运行目录：
  - 程序目录：`/usr/local/line-panel`
  - 数据目录：`/etc/line-panel`
  - 日志目录：`/var/log/line-panel`
  - Xray 目录：`/usr/local/line-panel/bin`
- 下载 Xray-core。
- 生成随机用户名、密码和访问路径。
- 把安装结果写入：`/etc/line-panel/install-result.env`

## 服务器执行方式

每个正式版本都会有一个 Git tag，例如 `v2.0.9`。首次安装必须指定这个版本：

```bash
VERSION=v2.0.9
PANEL_REPO_REF="$VERSION" bash <(curl -fsSL "https://raw.githubusercontent.com/cchu40558-collab/relay-panel/${VERSION}/scripts/install-server.sh")
```

升级时也必须明确写出目标版本：

```bash
relay-panel update v2.1.0
```

安装完成后查看账号和地址：

```bash
cat /etc/line-panel/install-result.env
```

查看服务状态：

```bash
systemctl status line-panel --no-pager
```

看实时日志：

```bash
journalctl -u line-panel -f
```

## 常用参数

可以在执行脚本时传：

```bash
PANEL_PORT=2053
PANEL_USERNAME=myuser
PANEL_PASSWORD=mypass
PANEL_WEB_BASE_PATH=/panel
PANEL_INSTALL_NGINX=true
PANEL_INSTALL_XRAY=true
```

例子：

```bash
VERSION=v2.0.9
PANEL_PORT=2053 PANEL_WEB_BASE_PATH=/panel PANEL_REPO_REF="$VERSION" bash <(curl -fsSL "https://raw.githubusercontent.com/cchu40558-collab/relay-panel/${VERSION}/scripts/install-server.sh")
```

## 现在能做到什么

安装好后，服务器上会有一个独立的定制面板服务。

然后你在网页里填 Cloudflare 主线路参数，点“保存并应用”：

- 面板会写入 Xray 入站。
- 面板会写入住宅出口出站。
- 面板会写入路由规则。
- 面板会通知 Xray 重载。
- 如果打开“写入 Nginx 并重载”，Linux 服务器上会写入 Nginx 配置并 reload。

## 还没完成

- Trojan 直连真实执行器，后续版本支持。
- Cloudflare API 自动改 DNS。
- 防火墙自动开放端口。

## 版本与回退

- `relay-panel update` 不带版本号时不会升级。
- `relay-panel update vX.Y.Z` 只会下载对应的正式版本。
- 每次升级前会备份程序、服务配置和面板数据。
- 升级失败会自动恢复刚才的版本。
- 成功后只保留最近两个旧版本备份；加上当前运行版本，服务器最多保留三个版本。
- `relay-panel rollback` 会恢复最近一个旧版本，并先备份当前版本。
