# phantom-node

`phantom-node` 是一个专注于 `Xboard` 的 Go 守护进程，用来运行单个
`sing-box` 节点。它负责从 Xboard 拉取节点配置和用户列表，启动本地
`sing-box`，并持续完成用户同步、流量上报与在线人数上报。

当前仓库的定位刻意收窄，不做多面板抽象，优先把 `Xboard` 场景做好。
整体设计取向参考了 [V2bX](https://github.com/wyx2685/V2bX) 和
[XrayR](https://github.com/XrayR-project/XrayR) 在面板对接、中文运维体验、
热重载和定时上报上的思路，但实现上保持为更轻量的单节点版本。

## 项目定位

- 只专注 `Xboard UniProxy` 接口
- 程序运行日志、安装脚本和文档统一使用中文
- 维持“单节点、单进程、内嵌 sing-box”的简单结构
- 优先保证热重载、流量上报和退出清理的可靠性

## 当前支持范围

当前运行配置固定为以下协议组合：

- `VLESS`
- `REALITY`
- `XTLS Vision`

这意味着项目已经明确偏向某一类 Xboard 节点，不追求像 V2bX、XrayR 那样的
多协议、多后端覆盖能力。

## 主要功能

- 启动时从 Xboard 拉取节点配置和用户列表
- 根据 Xboard 配置生成并启动内嵌 `sing-box`
- 定时同步用户和节点配置，发现变化时执行热重载
- 定时向 Xboard 上报流量和在线人数
- 可选注册 Cloudflare DNS A 记录
- 热重载失败时尝试自动回滚到旧实例
- 流量上报失败时先缓存到内存，等待下次重试

## 必填环境变量

- `PANEL_HOST`：Xboard 面板地址，需包含 `https://`
- `PANEL_TOKEN`：节点通信令牌
- `NODE_ID`：Xboard 节点 ID

## 可选环境变量

- `SYNC_INTERVAL`：同步周期，单位秒，默认 `60`
- `REPORT_INTERVAL`：流量上报周期，单位秒，默认 `60`
- `LISTEN_PORT`：节点监听端口，默认 `443`
- `LOG_LEVEL`：日志级别，可选 `debug`、`info`、`warn`、`error`
- `CF_ENABLED`：是否启用 Cloudflare DNS 自动注册
- `CF_API_TOKEN`：启用 Cloudflare 时必填
- `CF_ZONE_ID`：启用 Cloudflare 时必填
- `CF_RECORD_NAME`：启用 Cloudflare 时必填

## 本地开发

运行测试：

```bash
go test ./...
```

本地构建：

```bash
go build ./...
```

## 部署方式

仓库自带 Linux 安装脚本：

```bash
bash deploy/install.sh \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret
```

如需启用 Cloudflare DNS，可额外追加：

```bash
--cf-enabled
--cf-token=...
--cf-zone=...
--cf-record=node.example.com
```

## 可靠性说明

- 流量上报失败时，不会立刻丢弃，会先缓存在内存中
- 下次上报成功时，会把缓存流量和新流量一并提交
- `sing-box` 热重载失败时，会尽量回滚到旧实例
- Cloudflare DNS 注册会优先更新已有记录，而不是重复创建

## 当前已知限制

- 缓存流量只保存在内存里，进程异常退出时仍可能丢失
- Stats gRPC 监听地址固定为 `127.0.0.1:10085`
- 当前实现是轻量单节点架构，不支持多节点复用
- 目前没有实现像 V2bX、XrayR 那样更完整的协议矩阵和面板生态兼容层
