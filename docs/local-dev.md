# 本地开发启动说明

这个项目现在先按“借用 3x-ui，逐步改成线路部署面板”的方式开发。这里的脚本只负责本机开发，不会真的修改服务器线路。

## 目录

- 项目代码：`D:\tizi\一键部署\一键部署面板`
- 开发工具：`D:\dm`
- 本地开发数据：`D:\tizi\一键部署\一键部署面板\.dev\x-ui`

## 第一次准备

如果刚打开新的 PowerShell，直接进入项目：

```powershell
cd D:\tizi\一键部署\一键部署面板
```

如果前端依赖没装过：

```powershell
cd D:\tizi\一键部署\一键部署面板\frontend
npm.cmd ci
```

## 启动后端面板

```powershell
cd D:\tizi\一键部署\一键部署面板
.\scripts\dev-panel.ps1
```

默认访问：

```text
http://127.0.0.1:2053/panel/
```

如果 `2053` 被占用：

```powershell
.\scripts\dev-panel.ps1 -Port 2054
```

新开发数据库的默认账号一般是：

```text
admin / admin
```

## 启动前端开发服务

后端面板使用的是构建后的前端文件。平时改前端页面时，可以另开一个 PowerShell 跑 Vite：

```powershell
cd D:\tizi\一键部署\一键部署面板
.\scripts\dev-frontend.ps1
```

默认访问：

```text
http://127.0.0.1:5173/
```

如果要让后端面板直接看到最新前端页面，先构建前端：

```powershell
cd D:\tizi\一键部署\一键部署面板\frontend
npm.cmd run build
```

然后重新启动后端面板。

## 检查代码

```powershell
cd D:\tizi\一键部署\一键部署面板
.\scripts\check-dev.ps1
```

这个检查会跑：

- 前端 TypeScript 类型检查
- 当前新增后端模块的 Go 测试

## 现在做到哪一步

当前阶段已经有了线路面板的外壳：

- 左侧菜单：线路部署、线路列表、诊断日志、系统设置
- 第一版线路部署入口：Cloudflare 主线路、Reality 直连；Trojan 后续支持
- 后端已提供线路数据、预检、保存并应用、检测、分享 API
- Cloudflare 和 Reality 会写入 3x-ui 管理的 Xray 入站、出站和路由草案
- Cloudflare 在 Linux 上可选择写入 Nginx 配置，并在 `nginx -t` 或 reload 失败时恢复旧配置
- 本地开发环境缺少 Xray binary 时，Xray 实际重载失败属于预期；可继续验证表单校验、配置生成、状态和日志

下一步是在 VPS 上完成烟雾测试：验证真实 Xray 重载、Nginx 写入和回滚，以及入口和住宅出口连通性。
