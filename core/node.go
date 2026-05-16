package core

import (
	"context"
	"time"

	"phantom-node/cloudflare"
	"phantom-node/config"
	"phantom-node/panel"
	"phantom-node/singbox"

	"go.uber.org/zap"
)

// Node 是节点生命周期的主控器，编排启动、运行、关停三个阶段
type Node struct {
	cfg         *config.Config
	engine      *singbox.Engine
	panelClient *panel.Client
	dnsMgr      *cloudflare.DNSManager // 可能为 nil（CF 未启用时）
	logger      *zap.Logger

	trafficReporter *TrafficReporter
	userSyncer      *UserSyncer
}

// NewNode 创建节点主控器
func NewNode(cfg *config.Config, logger *zap.Logger) *Node {
	return &Node{
		cfg:         cfg,
		engine:      singbox.NewEngine(),
		panelClient: panel.NewClient(cfg.PanelHost, cfg.PanelToken, cfg.NodeID),
		logger:      logger,
	}
}

// Start 执行串行启动序列，任何一步失败则返回错误
func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("启动 phantom-node",
		zap.Int("node_id", n.cfg.NodeID),
		zap.String("panel", n.cfg.PanelHost),
	)

	// 1. 拉取节点配置
	n.logger.Info("正在拉取节点配置...")
	nodeConfig, err := n.panelClient.GetNodeConfig()
	if err != nil {
		return err
	}
	n.logger.Info("节点配置获取成功",
		zap.Int("端口", nodeConfig.ServerPort),
		zap.String("协议", nodeConfig.Protocol),
		zap.String("伪装域名", nodeConfig.TLSSettings.ServerName),
		zap.String("流控", nodeConfig.Flow),
	)

	// 使用面板返回的间隔值作为备选（仅当环境变量使用默认值时）
	if nodeConfig.BaseConfig.PullInterval > 0 && n.cfg.SyncInterval == 60 {
		n.cfg.SyncInterval = nodeConfig.BaseConfig.PullInterval
	}
	if nodeConfig.BaseConfig.PushInterval > 0 && n.cfg.ReportInterval == 60 {
		n.cfg.ReportInterval = nodeConfig.BaseConfig.PushInterval
	}

	// 2. 拉取用户列表
	n.logger.Info("正在拉取用户列表...")
	users, err := n.panelClient.GetUsers()
	if err != nil {
		return err
	}
	n.logger.Info("用户列表获取成功", zap.Int("用户数", len(users)))

	// 3. 启动 sing-box 内核
	n.logger.Info("正在启动 sing-box 内核...")
	if err := n.engine.Start(nodeConfig, users, n.cfg.ListenPort, n.cfg.LogLevel); err != nil {
		return err
	}
	n.logger.Info("sing-box 内核启动成功", zap.Int("监听端口", n.cfg.ListenPort))

	// 4. Cloudflare DNS 自注册
	if n.cfg.CFEnabled {
		n.logger.Info("正在注册 Cloudflare DNS...")
		publicIP, err := cloudflare.GetPublicIP()
		if err != nil {
			return err
		}

		n.dnsMgr = cloudflare.NewDNSManager(n.cfg.CFAPIToken, n.cfg.CFZoneID, n.cfg.CFRecordName)
		if err := n.dnsMgr.Register(publicIP); err != nil {
			return err
		}
		n.logger.Info("DNS 注册成功",
			zap.String("记录", n.cfg.CFRecordName),
			zap.String("IP", publicIP),
		)
	}

	// 5. 首次心跳
	if err := n.panelClient.SendAlive([]map[string]interface{}{}); err != nil {
		n.logger.Warn("首次心跳失败（非致命）", zap.Error(err))
	}

	// 6. 创建定时任务组件
	n.trafficReporter = NewTrafficReporter(n.engine, n.panelClient, n.logger)
	n.userSyncer = NewUserSyncer(
		n.engine, n.panelClient, nodeConfig,
		n.cfg.ListenPort, n.cfg.LogLevel, n.logger, n.trafficReporter,
	)
	n.userSyncer.SetInitialHash(users)

	// 7. 启动并行定时器
	go n.runTickers(ctx)

	n.logger.Info("phantom-node 启动完成，所有定时器已激活")
	return nil
}

// runTickers 运行所有并行定时器，直到 ctx 被取消
func (n *Node) runTickers(ctx context.Context) {
	syncTicker := time.NewTicker(time.Duration(n.cfg.SyncInterval) * time.Second)
	reportTicker := time.NewTicker(time.Duration(n.cfg.ReportInterval) * time.Second)
	aliveTicker := time.NewTicker(60 * time.Second)

	defer syncTicker.Stop()
	defer reportTicker.Stop()
	defer aliveTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-syncTicker.C:
			n.userSyncer.Sync(ctx)

		case <-reportTicker.C:
			n.trafficReporter.Report(ctx)

		case <-aliveTicker.C:
			onlineCount := n.engine.GetOnlineCount(ctx)
			
			// Xboard 依赖数组的长度来统计在线人数，因此构造相同长度的数组
			onlineUsers := make([]map[string]interface{}, onlineCount)
			for i := 0; i < onlineCount; i++ {
				onlineUsers[i] = map[string]interface{}{}
			}

			if err := n.panelClient.SendAlive(onlineUsers); err != nil {
				n.logger.Warn("心跳上报失败", zap.Error(err))
			} else {
				n.logger.Debug("心跳上报成功", zap.Int("在线数", onlineCount))
			}
		}
	}
}

// Shutdown 执行优雅关停序列
func (n *Node) Shutdown(ctx context.Context) {
	n.logger.Info("开始优雅关停...")

	// 1. 最后一次流量抢救上报
	if n.trafficReporter != nil {
		n.logger.Info("正在抢救最后一次流量数据...")
		n.trafficReporter.Report(ctx)
	}

	// 2. 摘除 Cloudflare DNS 记录
	if n.dnsMgr != nil {
		n.logger.Info("正在摘除 DNS 记录...")
		if err := n.dnsMgr.Deregister(); err != nil {
			n.logger.Error("DNS 记录摘除失败", zap.Error(err))
		} else {
			n.logger.Info("DNS 记录摘除成功")
		}
	}

	// 3. 关停 sing-box
	n.logger.Info("正在关停 sing-box...")
	if err := n.engine.Close(); err != nil {
		n.logger.Error("sing-box 关停异常", zap.Error(err))
	}

	n.logger.Info("phantom-node 已完全关停")
}
