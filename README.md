# singbox-bridge

[![Go Test](https://github.com/miliyao/singbox-bridge/workflows/Go/badge.svg)](https://github.com/miliyao/singbox-bridge/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/miliyao/singbox-bridge)](https://golang.org)
[![License](https://img.shields.io/github/license/miliyao/singbox-bridge)](LICENSE)

`singbox-bridge` 是一个为 **Xboard (UniProxy API)** 面板定制的、极其轻量且高性能的 **VLESS-Reality** 节点后端程序。

它通过嵌入式方式直接集成 [sing-box](https://github.com/SagerNet/sing-box) 内核，摒弃了传统后端多核心抽象的冗余与子进程调用的开销，以 **Unix 哲学（只做一件事并做到极致）** 为设计核心，提供了无与伦比的运行效率与内存控制。

---

## 核心特性

- ⚡ **极致轻量**: 核心源码仅约 2,800 行。内存占用极低，无任何运行期子进程开销。
- 👥 **单进程多实例**: 支持配置逗号分隔的多个节点 ID（如 `NODE_ID="5,6"`），单个进程内自动并发运行多个独立的节点实例，共享内存开销，运维极简。
- 🔀 **端口与状态避让**: 在多实例模式下，每个实例的监控端口、自检端点、流量快照和 SQLite 缓存数据库均自动计算端口偏移及文件名特化（如递增统计端口，保存为 `cache-node5.db`、`cache-node6.db`），彻底防范端口冲突与文件锁竞争。
- 🔒 **VLESS-Reality-Vision**: 原生且专注于当前最健壮的防封锁协议组合。
- 📦 **零配置文件**: 纯环境变量驱动，完美适配 Docker、Kubernetes 和 systemd。
- 💾 **流量持久化缓冲**: 遭遇网络波动或面板宕机时，流量统计自动落盘缓冲，网络恢复后补报，绝不漏记。
- 🔄 **原子级热重载与安全回滚**: 配置变更时瞬间无缝重载。若新配置启动失败，自动回滚至上一稳定运行版本。
- 🚦 **高级速率与连接控制**:
  - 精确到用户的并发 TCP 连接数限制。
  - 支持 per-user 和 per-IP 的每分钟新连接建立速率（CPS）限制。
  - 自带 BitTorrent 等 P2P 流量拦截规则。
- 🌐 **Google IPv6 分流直连**: 开启后，自动通过 IPv6 直连所有 Google 相关域名，有效节约 IPv4 流量并防范谷歌验证码。
- 🛠️ **一键诊断工具**: 内置 `singbox-bridge doctor` 命令，一键检查面板连通性、核心配置、本地端口冲突等。
- 📊 **状态端点**: 内置 `/status` 和 `/health` JSON API 状态暴露。

---

## 环境变量配置项

通过设置以下环境变量，即可完全掌控 `singbox-bridge` 的运行行为：

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `PANEL_HOST` | *(必填)* | Xboard 面板的 HTTP 接口地址 |
| `PANEL_TOKEN` | *(必填)* | 面板 UniProxy 通信密钥 |
| `NODE_ID` | *(必填)* | 节点 ID（支持单节点如 `5`，或多节点列表如 `5,6,7` 以启用单进程多实例模式） |
| `LISTEN_PORT` | `443` | 节点本地监听端口（由 Xboard 控制，多实例时各自监听面板指定的端口） |
| `LOG_LEVEL` | `info` | 日志级别 (`debug`, `info`, `warn`, `error`) |
| `GOOGLE_IPV6` | `false` | 是否开启 Google 域名 IPv6 直连分流 |
| `TRAFFIC_STATE_FILE` | `/var/lib/singbox-bridge/pending-traffic.json` | 待上报流量落盘缓冲路径（多实例模式下自动特化为 `*-node{id}.json`） |
| `MAX_CONN_PER_USER` | `32` | 每用户允许的最大 TCP 并发连接数 |
| `MAX_CONN_PER_IP` | `20` | 单个 IP 允许的最大并发连接数 |
| `MAX_NEW_CONN_PER_USER_PER_MIN` | `120` | 每用户每分钟最大新连接数 |
| `MAX_NEW_CONN_PER_IP_PER_MIN` | `60` | 单 IP 每分钟最大新连接数 |
| `STATS_LISTEN_ADDR` | `127.0.0.1:10085` | 内部统计数据监听地址（多实例时以其为基准递增偏移：`10085`, `10086`...） |

---

## 快速部署

### 1. 一键脚本安装 (Systemd)

使用官方维护的一键安装脚本，自动安装最新 Release 的二进制文件或本地编译，并注册为 Systemd 服务：

```bash
# 单节点部署
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- --node-id=5 --panel=https://panel.example.com --token=secret_token

# 多节点单进程并发部署 (如 5 和 6 节点)
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- --node-id=5,6 --panel=https://panel.example.com --token=secret_token

# 开启 Google IPv6 直连分流
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- --node-id=5,6 --panel=https://panel.example.com --token=secret_token --google-ipv6
```

### 2. 状态查询与运维

```bash
# 查看服务运行状态及并发实例日志
systemctl status singbox-bridge
journalctl -u singbox-bridge -n 100 --no-pager

# 运行一键健康自检（支持多实例循环排查）
env $(cat /etc/singbox-bridge.env | xargs) /usr/local/bin/singbox-bridge doctor
```

> [!NOTE]
> 当 `singbox-bridge` 后台服务正在运行时，执行 `doctor` 诊断命令中的 `listen_port` 检查项报告 `bind: address already in use` 属于正常现象，这说明后台服务已成功占用了该端口提供服务。

