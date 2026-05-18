# phantom-node

`phantom-node` 是一个面向 `Xboard UniProxy` 的单节点守护进程。

它负责从面板拉取节点配置和用户列表，启动内嵌 `sing-box`，持续完成热重载、流量上报、在线 IP 上报，以及单节点范围内的基础连接限制。

项目当前的目标很明确：

- 只做 `Xboard UniProxy`
- 只做单节点
- 只做 `VLESS + REALITY + TCP`
- 优先把部署、稳定性和运维体验做好

## 当前支持范围

当前版本只支持这一条运行链路：

- `VLESS`
- `REALITY`
- `XTLS Vision`
- `TCP`

如果你的节点模型就是这条链路，这个项目是为它写的。
如果你需要 `ws`、多协议矩阵、多后端兼容层，当前仓库并不打算覆盖那类场景。

## 主要能力

- 从 `Xboard UniProxy` 拉取节点配置和用户列表
- 根据面板数据生成并启动内嵌 `sing-box`
- 定时同步用户和节点配置，配置变化时热重载
- 热重载失败时回滚到上一份可用实例
- 通过 `sing-box Stats API` 采集用户流量并定时上报
- 流量上报失败时缓存在本地文件，进程重启后自动恢复重试
- 定时向面板上报在线用户 IP 列表
- 默认注入本地 `bittorrent` 拒绝规则
- 内置单节点 limiter，用于控制设备数、并发连接数和新建连接速率

## 当前 limiter 行为

当前 limiter 是单节点视角，核心行为如下：

- 基于连接级 tracker 统计活动连接和来源 IP
- 支持 `device_limit`
- 支持单用户并发连接数限制
- 支持单 IP 并发连接数限制
- 支持单用户每分钟新建连接速率限制
- 支持单 IP 每分钟新建连接速率限制
- 支持面板用户 `speed_limit`
- 启动和定时同步时会从面板拉取 `alivelist`，用于补齐单节点 alive 视角

当前 limiter 仍有边界：

- 只按单节点视角生效
- 不做跨节点在线设备聚合
- `speed_limit` 以单用户共享速率桶实现，不做跨节点聚合

## 运行时默认值

程序默认启用以下安全和网络行为：

- 启用协议嗅探
- 拒绝 `bittorrent`
- 启用 `reuse_addr`
- 启用 `tcp_fast_open`
- 设置 TCP keepalive 和 keepalive interval

默认配置值：

- `LISTEN_PORT=443`
- `SYNC_INTERVAL=60`
- `REPORT_INTERVAL=60`
- `LOG_LEVEL=info`
- `STATS_LISTEN_ADDR=127.0.0.1:10085`
- `CLASH_API_LISTEN_ADDR` 默认关闭
- `STATUS_LISTEN_ADDR=127.0.0.1:10087`
- `TRAFFIC_PENDING_MAX_USERS=10000`

`TRAFFIC_STATE_FILE` 默认值：

- Linux: `/var/lib/phantom-node/pending-traffic.json`
- Windows: `%TEMP%\\phantom-node\\pending-traffic.json`

## 环境变量

必填环境变量：

- `PANEL_HOST`：Xboard 面板地址，包含 `http://` 或 `https://`
- `PANEL_TOKEN`：节点通信 token
- `NODE_ID`：Xboard 节点 ID

可选环境变量：

- `SYNC_INTERVAL`：用户和配置同步周期，单位秒
- `REPORT_INTERVAL`：流量上报周期，单位秒
- `LISTEN_PORT`：节点监听端口
- `LOG_LEVEL`：`debug`、`info`、`warn`、`error`
- `STATS_LISTEN_ADDR`：`sing-box` Stats gRPC 监听地址
- `STATUS_LISTEN_ADDR`：本地状态 HTTP 监听地址
- `CLASH_API_LISTEN_ADDR`：`sing-box` Clash API 监听地址，留空则不启用
- `TRAFFIC_STATE_FILE`：流量失败缓冲文件路径
- `TRAFFIC_PENDING_MAX_USERS`：本地流量缓冲最多保留的用户数
- `MAX_CONN_PER_USER`：单用户最大并发连接数
- `MAX_CONN_PER_IP`：单 IP 最大并发连接数
- `MAX_NEW_CONN_PER_USER_PER_MIN`：单用户每分钟最大新建连接数
- `MAX_NEW_CONN_PER_IP_PER_MIN`：单 IP 每分钟最大新建连接数

说明：

- 如果面板下发了 `base_config.pull_interval` / `base_config.push_interval`，并且你没有显式设置 `SYNC_INTERVAL` / `REPORT_INTERVAL`，程序会优先采用面板下发值。

## Linux 一键安装

仓库根目录自带 `install.sh`，默认行为：

- 检查 `systemd`
- 自动放行 TCP 监听端口
- 优先下载 GitHub Release 预编译二进制
- Release 下载失败时自动回退到源码编译
- 需要源码编译时自动安装 Go
- 写入 `/etc/phantom-node.env`
- 写入 systemd 服务
- 自动启用并启动服务
- 自动尝试启用 BBR 和常用 sysctl 调优

最简单的安装方式：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/miliyao/phantom-node/main/install.sh) \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret
```

如果你要指定 Release 版本：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/miliyao/phantom-node/main/install.sh) \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret \
  --version=v0.1.0
```

如果你要强制源码编译：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/miliyao/phantom-node/main/install.sh) \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret \
  --source
```

如果你已经有自己的二进制下载地址：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/miliyao/phantom-node/main/install.sh) \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret \
  --download-url=https://example.com/phantom-node
```

安装脚本当前支持的参数：

- `--node-id`
- `--panel`
- `--token`
- `--port`
- `--version`
- `--ref`
- `--source`
- `--download-url`

## 安装后文件

安装脚本默认会写入：

- 二进制：`/usr/local/bin/phantom-node`
- 环境文件：`/etc/phantom-node.env`
- systemd 服务：`/etc/systemd/system/phantom-node.service`

环境文件默认内容类似：

```env
PANEL_HOST=https://panel.example.com
PANEL_TOKEN=secret
NODE_ID=5
LISTEN_PORT=443
```

## 本地运行

直接运行：

```bash
PANEL_HOST=https://panel.example.com \
PANEL_TOKEN=secret \
NODE_ID=5 \
go run -tags with_utls .
```

本地构建：

```bash
go build -tags with_utls .
```

运行测试：

```bash
go test -tags with_utls ./...
```

启动前自检：

```bash
PANEL_HOST=https://panel.example.com \
PANEL_TOKEN=secret \
NODE_ID=5 \
go run -tags with_utls . doctor
```

运行后状态接口：

```bash
curl http://127.0.0.1:10087/health
curl http://127.0.0.1:10087/status
```

## 发布

仓库包含 CI 配置 [.github/workflows/ci.yml](D:/Users/Aaron/Desktop/30/phantom-node/.github/workflows/ci.yml)，当前会在 `push` 到 `main` 和 `pull_request` 时执行：

- `go test -tags with_utls ./...`
- `go build -tags with_utls ./...`

仓库也保留了 Linux 预编译二进制发布路径，安装脚本默认会尝试下载：

- `phantom-node-linux-amd64`
- `phantom-node-linux-arm64`

## 稳定性设计

当前版本已经实现的稳定性行为：

- 热重载失败自动回滚
- 流量上报失败先缓存，再在后续周期重试
- 流量缓冲持久化到磁盘
- 进程重启后恢复未成功上报的流量
- 流量缓冲文件损坏时自动移到 `.corrupt`
- 流量缓冲支持最大用户数保护
- 提供本地 `/health` 和 `/status` 状态接口
- 提供 `doctor` 启动前诊断命令
- 使用面板 alive 列表和本地连接视角共同参与 `device_limit` 判断

## 已知限制

- 当前只支持 `VLESS + REALITY + XTLS Vision + TCP`
- 不支持 `ws`、`grpc` 等其它传输层
- `speed_limit` 是单节点、单用户级别限制
- limiter 只做单节点限制，不做跨节点聚合
- 不依赖远程 rule-set，启动不再需要外网拉取规则文件
- 目前仍是轻量单节点实现，不支持多节点复用
- 不追求实现 `V2bX` / `XrayR` 那样的大协议矩阵

## 适合什么场景

适合：

- 你的面板就是 `Xboard UniProxy`
- 你只需要单节点
- 你只跑 `VLESS + REALITY + TCP`
- 你希望部署和排错链路尽量简单

不适合：

- 你需要多协议、多传输层
- 你需要跨节点设备限制
- 你需要完整的多面板兼容层
