# singbox-bridge

[![Go Test](https://github.com/miliyao/singbox-bridge/workflows/Go/badge.svg)](https://github.com/miliyao/singbox-bridge/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/miliyao/singbox-bridge)](https://golang.org)
[![License](https://img.shields.io/github/license/miliyao/singbox-bridge)](LICENSE)

`singbox-bridge` 是一个面向 [Xboard](https://github.com/cedar2025/Xboard)（UniProxy API）的轻量级节点后端，将 sing-box 内核直接嵌入单一进程运行，针对 **1C1G VPS** 环境进行了深度优化，重点覆盖热路径并发性能、内存可控性与连接安全管理。

---

## 特性概览

- **嵌入式 sing-box 内核**：无需外部进程，通过 Go API 直接驱动，减少 IPC 开销与进程间同步延迟
- **无锁流量统计热路径**：`sync.Map` + `atomic` 组合，`AddTraffic` 完全免除互斥锁争用
- **分段 CPS 限速器**：$O(1)$ 新建连接速率判定，支持按用户与按 IP 双维度控制
- **双层 IP 引用计数**：设备数统计从 $O(\text{连接数})$ 降为 $O(\text{活跃IP数})$
- **确定性零分配哈希**：流式 SHA-256 二进制写入，配置变更检测幂等且无碰撞
- **离线流量持久化**：面板宕机期间流量缓冲落盘，恢复后自动重试合并上报
- **单进程多节点**：一个进程可同时运行多个 `NODE_ID`，共享内存与运行时资源
- **运行时内存保护**：自动设置 `GOMEMLIMIT = 750MiB`（未显式配置时），引导 GC 主动介入，降低 OOM 风险

---

## 快速部署

### 一键脚本安装（推荐，Systemd）

```bash
# 从 main 分支拉取源码编译安装（适用于需要最新优化的场景）
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=your_token \
  --google-ipv6 \
  --source
```

多节点示例（单进程同时运行节点 5、6、7）：

```bash
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- \
  --node-id=5,6,7 \
  --panel=https://panel.example.com \
  --token=your_token \
  --source
```

### 手动编译

```bash
git clone https://github.com/miliyao/singbox-bridge.git
cd singbox-bridge
go build -o singbox-bridge .
```

---

## 配置项

配置通过环境变量注入，也可写入 `/etc/singbox-bridge.env` 文件（`KEY=VALUE` 格式，支持 `#` 注释行）。

### 必填项

| 变量名 | 说明 |
|--------|------|
| `PANEL_HOST` | Xboard 面板的 HTTP 接口地址（如 `https://panel.example.com`） |
| `PANEL_TOKEN` | 面板 UniProxy 通信密钥 |
| `NODE_ID` | 节点 ID，支持单节点（`5`）或逗号分隔的多节点列表（`5,6,7`） |

### 可选项

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `SYNC_INTERVAL` | `60` | 从面板拉取用户与节点配置的间隔（秒），优先采用面板下发的 `pull_interval` |
| `REPORT_INTERVAL` | `60` | 上报流量数据的间隔（秒），优先采用面板下发的 `push_interval` |
| `LOG_LEVEL` | `info` | 日志级别（`debug` / `info` / `warn` / `error`） |
| `GOOGLE_IPV6` | `false` | 是否启用 Google 域名 IPv6 直连分流 |
| `CLASH_API_LISTEN_ADDR` | *(禁用)* | 启用 Clash 元数据 API 并绑定的监听地址（如 `127.0.0.1:9090`） |
| `TRAFFIC_STATE_FILE` | `/var/lib/singbox-bridge/pending-traffic.json` | 离线暂存流量的落盘路径；多节点模式下自动按节点 ID 隔离 |
| `MAX_CONN_PER_USER` | `128` | 每用户允许的最大并发 TCP 连接数 |
| `MAX_CONN_PER_IP` | `64` | 单 IP 允许的最大并发 TCP 连接数 |
| `MAX_NEW_CONN_PER_USER_PER_MIN` | `600` | 每用户每分钟最大新建连接数（CPS 限速） |
| `MAX_NEW_CONN_PER_IP_PER_MIN` | `300` | 单 IP 每分钟最大新建连接数（CPS 限速） |
| `TRAFFIC_PENDING_MAX_USERS` | `10000` | 离线流量缓冲的最大用户条数，超限时按 UID 升序丢弃最旧条目 |

---

## 运维手册

### 常用命令

```bash
# 查看服务状态
systemctl status singbox-bridge

# 实时查看运行日志
journalctl -u singbox-bridge -f

# 查看最近 100 条日志
journalctl -u singbox-bridge -n 100 --no-pager

# 重启服务（配置变更后）
systemctl restart singbox-bridge
```

### 健康自检

内置 `doctor` 子命令，可对当前运行环境进行系统级检查，包括：
- TCP BBR 拥塞控制状态
- 文件描述符软限制（`nofile`）
- 面板连通性与 API 鉴权
- 节点端口可绑定性

```bash
env $(cat /etc/singbox-bridge.env | xargs) /usr/local/bin/singbox-bridge doctor
```

> [!NOTE]
> 后台服务运行时执行 `doctor`，`listen_port` 检查项会提示 `bind: address already in use`，这是正常现象，不影响其他检查项的输出。

---

## 架构设计

```
┌─────────────────────────────────────────────────────────┐
│                      main / Node                        │
│  （启动协调、Ticker 调度：sync / report / alive）        │
└──────────┬──────────────────┬───────────────────────────┘
           │                  │
     ┌─────▼──────┐    ┌──────▼──────┐
     │ UserSyncer │    │TrafficReport│
     │ （配置同步） │    │ （流量上报） │
     └─────┬──────┘    └──────┬──────┘
           │                  │
     ┌─────▼──────────────────▼──────┐
     │         singbox.Engine        │
     │  （内嵌 sing-box 内核实例）     │
     └──────┬──────────────┬─────────┘
            │              │
     ┌──────▼──────┐  ┌────▼──────────┐
     │   Limiter   │  │  StatsTracker │
     │ （连接管控） │  │ （流量统计）   │
     └─────────────┘  └───────────────┘
```

### 关键设计决策

#### 无锁流量统计（`singbox/stats.go`）
`AddTraffic` 是每个数据包都会触发的热路径。采用 `sync.Map`（`Load` / `LoadOrStore`）管理用户记录，`atomic.AddInt64` 原子累加流量，`atomic.SwapInt64` 原子采集清零，整个路径完全无 Mutex 争用。

#### 双锁 Limiter（`core/limiter.go`）
将全局大锁拆分为：
- `configMu sync.RWMutex`：保护用户表、限速配置等静态配置（读多写少）
- `stateMu sync.Mutex`：保护活跃连接、CPS 计数、设备在线状态等动态状态

两锁同时获取时严格遵守 `configMu` → `stateMu` 的加锁顺序，避免死锁。

#### 确定性配置哈希（`core/sync.go`）
配置变更检测基于 SHA-256 流式哈希：
- 标量字段：`binary.BigEndian` 定长写入，字符串字段之间插入 `\x00` 分隔符
- 浮点数：写入 IEEE 754 位模式（`math.Float64bits`），全精度无格式歧义
- 动态 JSON（`Routes`）：递归解析后按键名排序写入，数组保留顺序敏感性

---

## 测试

```bash
go test -v ./...
```

各子包均具备独立单元测试，所有测试不依赖真实 sing-box 内核或外部网络。

---

## 许可证

[MIT License](LICENSE)
