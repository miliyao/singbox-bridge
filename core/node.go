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

const aliveReportInterval = 60 * time.Second

// Node orchestrates the lifecycle of a single managed proxy node.
type Node struct {
	cfg         *config.Config
	engine      *singbox.Engine
	panelClient *panel.Client
	dnsMgr      *cloudflare.DNSManager
	logger      *zap.Logger

	trafficReporter *TrafficReporter
	userSyncer      *UserSyncer
}

func NewNode(cfg *config.Config, logger *zap.Logger) *Node {
	return &Node{
		cfg:         cfg,
		engine:      singbox.NewEngine(),
		panelClient: panel.NewClient(cfg.PanelHost, cfg.PanelToken, cfg.NodeID),
		logger:      logger,
	}
}

func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("starting phantom-node",
		zap.Int("node_id", n.cfg.NodeID),
		zap.String("panel", n.cfg.PanelHost),
	)

	n.logger.Info("fetching node configuration")
	nodeConfig, err := n.panelClient.GetNodeConfig()
	if err != nil {
		return err
	}

	n.logger.Info("fetched node configuration",
		zap.Int("server_port", nodeConfig.ServerPort),
		zap.String("protocol", nodeConfig.Protocol),
		zap.String("server_name", nodeConfig.TLSSettings.ServerName),
		zap.String("flow", nodeConfig.Flow),
	)

	if nodeConfig.BaseConfig.PullInterval > 0 && n.cfg.SyncInterval == 60 {
		n.cfg.SyncInterval = nodeConfig.BaseConfig.PullInterval
	}
	if nodeConfig.BaseConfig.PushInterval > 0 && n.cfg.ReportInterval == 60 {
		n.cfg.ReportInterval = nodeConfig.BaseConfig.PushInterval
	}

	n.logger.Info("fetching users")
	users, err := n.panelClient.GetUsers()
	if err != nil {
		return err
	}
	n.logger.Info("fetched users", zap.Int("user_count", len(users)))

	n.logger.Info("starting sing-box")
	if err := n.engine.Start(nodeConfig, users, n.cfg.ListenPort, n.cfg.LogLevel); err != nil {
		return err
	}
	n.logger.Info("sing-box started", zap.Int("listen_port", n.cfg.ListenPort))

	if n.cfg.CFEnabled {
		n.logger.Info("registering Cloudflare DNS record")
		publicIP, err := cloudflare.GetPublicIP()
		if err != nil {
			return err
		}

		n.dnsMgr = cloudflare.NewDNSManager(n.cfg.CFAPIToken, n.cfg.CFZoneID, n.cfg.CFRecordName)
		if err := n.dnsMgr.Register(publicIP); err != nil {
			return err
		}

		n.logger.Info("Cloudflare DNS record is active",
			zap.String("record", n.cfg.CFRecordName),
			zap.String("public_ip", publicIP),
		)
	}

	if err := n.panelClient.SendAlive([]map[string]interface{}{}); err != nil {
		n.logger.Warn("initial heartbeat failed", zap.Error(err))
	}

	n.trafficReporter = NewTrafficReporter(n.engine, n.panelClient, n.logger)
	n.userSyncer = NewUserSyncer(
		n.engine,
		n.panelClient,
		nodeConfig,
		n.cfg.ListenPort,
		n.cfg.LogLevel,
		n.logger,
		n.trafficReporter,
	)
	n.userSyncer.SetInitialHash(users)

	go n.runTickers(ctx)

	n.logger.Info("phantom-node started",
		zap.Int("sync_interval_seconds", n.cfg.SyncInterval),
		zap.Int("report_interval_seconds", n.cfg.ReportInterval),
	)
	return nil
}

func (n *Node) runTickers(ctx context.Context) {
	syncTicker := time.NewTicker(time.Duration(n.cfg.SyncInterval) * time.Second)
	reportTicker := time.NewTicker(time.Duration(n.cfg.ReportInterval) * time.Second)
	aliveTicker := time.NewTicker(aliveReportInterval)

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
			if err := n.panelClient.SendAlive(buildOnlineUsers(onlineCount)); err != nil {
				n.logger.Warn("heartbeat push failed", zap.Error(err))
			} else {
				n.logger.Debug("heartbeat pushed", zap.Int("online_users", onlineCount))
			}
		}
	}
}

func (n *Node) Shutdown(ctx context.Context) {
	n.logger.Info("shutting down phantom-node")

	if n.trafficReporter != nil {
		n.logger.Info("flushing final traffic report")
		n.trafficReporter.Report(ctx)
	}

	if n.dnsMgr != nil {
		n.logger.Info("removing Cloudflare DNS record")
		if err := n.dnsMgr.Deregister(); err != nil {
			n.logger.Error("failed to remove Cloudflare DNS record", zap.Error(err))
		} else {
			n.logger.Info("Cloudflare DNS record removed")
		}
	}

	n.logger.Info("stopping sing-box")
	if err := n.engine.Close(); err != nil {
		n.logger.Error("sing-box closed with error", zap.Error(err))
	}

	n.logger.Info("phantom-node shutdown complete")
}

func buildOnlineUsers(count int) []map[string]interface{} {
	onlineUsers := make([]map[string]interface{}, count)
	for i := 0; i < count; i++ {
		onlineUsers[i] = map[string]interface{}{}
	}
	return onlineUsers
}
