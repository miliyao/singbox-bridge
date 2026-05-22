# singbox-bridge

[![Go Test](https://github.com/miliyao/singbox-bridge/workflows/Go/badge.svg)](https://github.com/miliyao/singbox-bridge/actions)
[![Go Version](https://img.shields.io/github/go-mod/go-version/miliyao/singbox-bridge)](https://golang.org)
[![License](https://img.shields.io/github/license/miliyao/singbox-bridge)](LICENSE)

`singbox-bridge` 是一个面向 Xboard（UniProxy API）的轻量级 VLESS-Reality 节点后端，基于 Go 直接嵌入 sing-box 内核，减少传统多进程代理架构中的额外开销，重点优化 1C1G VPS 下的 CPU、内存与连接管理表现。

当前版本已实现原子化流量累加、分段计数限速、活跃 IP 引用计数、低分配用户哈希生成和运行时软内存保护。性能收益数据将以标准化压测报告为准，随版本持续更新。

---

## 优化设计与核心实现

为了保证在 1C1G 等低资源宿主机上长期稳定运行，本项目对关键路径进行了针对性重构：

### 1. 热路径流量统计优化
在 TCP 和 UDP 的数据收发热路径中，使用连接级局部 `atomic` 计数缓冲。当单连接累积流量达到 **1MB** 时，通过 `atomic.SwapInt64` 交换数据并刷入全局 `StatsTracker`，从而降低全局互斥锁的竞争频率；连接关闭时执行强制清算，保证计量精度不受影响。

### 2. $O(1)$ 分段计数 CPS 限制器
每分钟新建连接数（CPS）限制器由时间戳切片滑动窗口算法重构为分段计数结构。新建连接时的判定复杂度降为 $O(1)$，消除了频繁的切片复制与遍历开销。在更新用户配置时自动执行冷数据清理，防止缓存表膨胀。

### 3. 双层活跃 IP 引用计数
在连接注册与注销阶段实时更新双层 IP 引用计数 Map。获取用户当前活跃 IP 列表的复杂度从 $O(\text{当前连接数})$ 降为 $O(\text{当前活跃IP数})$，避免了每次判定设备限制时全量遍历连接和对 IP 字符串进行重复转换的开销。

### 4. 零拷贝流式哈希计算
在定时同步用户配置时，直接对排序后的用户列表执行流式二进制哈希计算。通过 `binary.Write` 将 ID、SpeedLimit 与 UUID 的原始字节写入 `sha256.New()`，避免了生成大量临时 `"ID:UUID:Limit"` 字符串与 `strings.Builder` 合并强转带来的堆内存分配与 GC 回收压力。

### 5. 原子自增连接追踪 ID
移除了对第三方 UUID 生成库的依赖，在建立连接时采用全局原子递增的 `uint64` 短序列（转化为 36 进制字符串）作为内存追踪键值，消除了生成随机 UUID 时的全局锁竞争和系统调用。

### 6. 自适应内存软限制保护
在未配置 `GOMEMLIMIT` 环境变量的低配环境中，程序启动时默认设置 `debug.SetMemoryLimit(750MiB)`。在突发大流量或配置重载引起瞬时内存上涨时，引导 Go 运行时执行更积极的垃圾回收，降低被内核 OOM Killer 终止服务的风险。

### 7. 宿主机内核与系统参数探测
程序启动时在 Linux 平台自动检测 TCP BBR 拥塞控制算法状态以及当前进程的最大文件描述符软限制（`nofile`）。若检测到配置未达到优化推荐值，会在日志中输出相应的优化指引。

---

## 环境变量配置项

| 变量名 | 默认值 | 说明 |
|--------|--------|------|
| `PANEL_HOST` | *(必填)* | Xboard 面板的 HTTP 接口地址 |
| `PANEL_TOKEN` | *(必填)* | 面板 UniProxy 通信密钥 |
| `NODE_ID` | *(必填)* | 节点 ID（支持单节点如 `5`，或多节点列表如 `5,6,7` 以启用单进程多实例模式） |
| `LISTEN_PORT` | `443` | 节点本地监听端口（多实例模式下监听各自面板指定的端口） |
| `LOG_LEVEL` | `info` | 日志级别 (`debug`, `info`, `warn`, `error`) |
| `GOOGLE_IPV6` | `false` | 是否开启 Google 域名 IPv6 直连分流 |
| `TRAFFIC_STATE_FILE` | *(自适应)* | 离线暂存流量落盘文件。Linux 默认路径为 `/var/lib/singbox-bridge/pending-traffic.json`（多节点模式自动特化隔离） |
| `MAX_CONN_PER_USER` | `128` | 每用户允许的最大 TCP 并发连接数 |
| `MAX_CONN_PER_IP` | `64` | 单个 IP 允许的最大并发连接数 |
| `MAX_NEW_CONN_PER_USER_PER_MIN` | `600` | 每用户每分钟最大新连接数 |
| `MAX_NEW_CONN_PER_IP_PER_MIN` | `300` | 单 IP 每分钟最大新连接数 |

---

## 部署与运维

### 1. 使用一键脚本安装 (Systemd)

对于需要启用最新优化版本的环境，推荐使用 `--source` 参数，脚本将自动配置 Go 环境并从 GitHub `main` 分支拉取源码在本地编译安装：

```bash
# 源码编译安装示例（需根据实际情况替换 panel 与 token 占位符）
curl -fsSL https://raw.githubusercontent.com/miliyao/singbox-bridge/main/install.sh | bash -s -- --node-id=5,6 --panel=https://panel.example.com --token=secret_token --google-ipv6 --source
```

### 2. 常用管理命令

```bash
# 查看服务状态及运行日志
systemctl status singbox-bridge
journalctl -u singbox-bridge -n 100 --no-pager

# 运行一键健康自检（支持多实例循环排查）
env $(cat /etc/singbox-bridge.env | xargs) /usr/local/bin/singbox-bridge doctor
```

> [!NOTE]
> 运行自检工具时，因后台常驻服务正在占用监听端口，导致 `listen_port` 检查项提示 `bind: address already in use` 属于正常状态。
