# singbox-bridge

[![Go Test](https://github.com/miliyao/singbox-bridge/workflows/Go/badge.svg)](https://github.com/miliyao/singbox-bridge/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/miliyao/singbox-bridge)](https://golang.org)
[![License](https://img.shields.io/github/license/miliyao/singbox-bridge)](LICENSE)

`singbox-bridge` 是一个专为 **Xboard (UniProxy API)** 面板深度定制、极致轻量且高性能的 **VLESS-Reality** 嵌入式节点后端程序。

它通过 **Go 语言级嵌入 API** 直接集成 [sing-box](https://github.com/SagerNet/sing-box) 内核，彻底告别了传统代理后端复杂的子进程调用和多核心冗余抽象。项目基于 **Unix 哲学** 设计，采用**全内存无盘化缓存**与**零端口暴露流量跟踪**技术，提供无与伦比的运行效率、内存控制和数据安全保密性。

---

## 核心特性

- ⚡ **嵌入式集成**: 摒弃多进程架构，内核与主控运行在同一内存空间，核心源码仅约 2,800 行，无任何运行时子进程 IPC 开销。
- 🛡️ **零端口暴露流量监控**: 彻底废弃实验性 gRPC/TCP 本地 API 监听，通过内核路由层 `limiterTracker` 实施纯内存级流量与连接状态劫持，规避本地端口被恶意探测的风险。
- 👥 **单进程多实例**: 支持以逗号分隔的节点 ID 环境变量（如 `NODE_ID="5,6"`），单个进程内自动并发运行多个独立的节点实例，共享内存与线程池，运维极简。
- 💾 **无盘化缓存与流量缓冲**: 移除了本地 SQLite 缓存文件，全面启用内存会话存储。若遭遇网络故障或面板宕机，待上报流量自动落盘暂存（文件名根据节点 ID 自动特化隔离），恢复后补报，绝不漏记。
- 🔒 **Reality 原生支持**: 专注于当前最健壮的 VLESS-Reality-Vision 防封锁协议组合。
- 🔄 **原子级热重载与安全回滚**: 监测到配置/用户变更时瞬间无缝重载。若新配置启动失败，自动回滚至上一稳定运行版本，保障服务持续可用。
- 🚦 **双维连接与频率控制**:
  - 精确到用户的并发 TCP 连接数限制。
  - 支持 per-user 和 per-IP 的每分钟新连接建立速率（CPS）限制。
  - 自动同步面板的设备数限制（`DeviceLimit`），并支持集群节点间全局设备数协同限制。
  - 自带 BitTorrent 等 P2P 流量拦截与高危端口封禁规则。
- 🌐 **Google IPv6 分流直连**: 开启后自动通过 IPv6 直连所有 Google 相关域名，有效节约 IPv4 流量并防范谷歌验证码。
- 🛠️ **一键诊断工具**: 内置 `singbox-bridge doctor` 诊断模式，一键排查面板连通性、证书配置及本地冲突。

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
| `MAX_CONN_PER_USER` | `128` | 每用户允许的最大 TCP 并发连接数 |
| `MAX_CONN_PER_IP` | `64` | 单个 IP 允许的最大并发连接数 |
| `MAX_NEW_CONN_PER_USER_PER_MIN` | `600` | 每用户每分钟最大新连接数 |
| `MAX_NEW_CONN_PER_IP_PER_MIN` | `300` | 单 IP 每分钟最大新连接数 |

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

