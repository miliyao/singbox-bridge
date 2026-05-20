package core

import (
	"context"
	"sync"
	"time"

	"singbox-bridge/config"
	"singbox-bridge/panel"
	"singbox-bridge/singbox"

	"go.uber.org/zap"
)

const aliveReportInterval = 60 * time.Second

// Node coordinates the full lifecycle of one Xboard-backed sing-box node.
type Node struct {
	mu sync.Mutex

	cfg         *config.Config
	engine      *singbox.Engine
	panelClient *panel.Client
	logger      *zap.Logger

	trafficReporter *TrafficReporter
	userSyncer      *UserSyncer
	limiter         *Limiter
}

func NewNode(cfg *config.Config, logger *zap.Logger) *Node {
	nodeLogger := logger.With(zap.Int("node_id", cfg.NodeID))
	limiter := NewLimiterWithConfig(LimiterConfig{
		MaxConnPerUser:          cfg.MaxConnPerUser,
		MaxConnPerIP:            cfg.MaxConnPerIP,
		MaxNewConnPerUserPerMin: cfg.MaxNewConnPerUserPerMin,
		MaxNewConnPerIPPerMin:   cfg.MaxNewConnPerIPPerMin,
	})
	return &Node{
		cfg:         cfg,
		engine:      singbox.NewEngine(cfg.StatsListenAddr, cfg.ClashAPIListenAddr, cfg.GoogleIPv6, limiter, limiter, nodeLogger),
		panelClient: panel.NewClient(cfg.PanelHost, cfg.PanelToken, cfg.NodeID),
		logger:      nodeLogger,
		limiter:     limiter,
	}
}

func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("starting xboard node",
		zap.Int("node_id", n.cfg.NodeID),
		zap.String("panel", n.cfg.PanelHost),
	)

	n.logger.Info("fetching node config from xboard")
	nodeConfig, err := n.panelClient.GetNodeConfig()
	if err != nil {
		return err
	}

	n.logger.Info("node config fetched",
		zap.Int("server_port", nodeConfig.ServerPort),
		zap.String("protocol", nodeConfig.Protocol),
		zap.String("network", nodeConfig.Network),
		zap.String("server_name", nodeConfig.TLSSettings.ServerName),
		zap.String("flow", nodeConfig.Flow),
	)

	if nodeConfig.BaseConfig.PullInterval > 0 && !n.cfg.SyncIntervalExplicit {
		n.cfg.SyncInterval = int(nodeConfig.BaseConfig.PullInterval)
	}
	if nodeConfig.BaseConfig.PushInterval > 0 && !n.cfg.ReportIntervalExplicit {
		n.cfg.ReportInterval = int(nodeConfig.BaseConfig.PushInterval)
	}

	n.logger.Info("fetching users from xboard")
	users, err := n.panelClient.GetUsers()
	if err != nil {
		return err
	}
	n.logger.Info("users fetched", zap.Int("user_count", len(users)))
	if n.limiter != nil {
		n.limiter.UpdateUsers(users)
	}
	refreshAliveCounts(n.panelClient, n.limiter, n.logger, "start")

	n.logger.Info("starting sing-box",
		zap.String("stats_listen_addr", n.cfg.StatsListenAddr),
	)
	if err := n.engine.Start(nodeConfig, users, n.cfg.LogLevel); err != nil {
		return err
	}
	n.logger.Info("sing-box started", zap.Int("listen_port", nodeConfig.ServerPort))

	initialAlivePayload := map[int][]string{}
	if n.limiter != nil {
		initialAlivePayload = n.limiter.BuildAlivePayload()
	}
	if err := n.panelClient.SendAlive(initialAlivePayload); err != nil {
		n.logger.Warn("initial online report failed", zap.Error(err))
	}

	n.trafficReporter = NewTrafficReporterWithLimit(n.engine, n.panelClient, n.logger, n.cfg.TrafficStateFile, n.cfg.TrafficPendingMaxUsers)
	n.userSyncer = NewUserSyncer(
		n.engine,
		n.panelClient,
		nodeConfig,
		n.cfg.LogLevel,
		n.logger,
		n.trafficReporter,
	)
	n.userSyncer.SetInitialHash(users)
	n.userSyncer.SetLimiter(n.limiter)

	go n.runTickers(ctx)

	n.logger.Info("xboard node started",
		zap.Int("sync_interval_seconds", n.cfg.SyncInterval),
		zap.Int("report_interval_seconds", n.cfg.ReportInterval),
		zap.String("traffic_state_file", n.cfg.TrafficStateFile),
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
			alivePayload := map[int][]string{}
			if n.limiter != nil {
				alivePayload = n.limiter.BuildAlivePayload()
			}
			onlineCount := n.engine.GetOnlineCount(ctx)
			if err := n.panelClient.SendAlive(alivePayload); err != nil {
				n.logger.Warn("online report failed", zap.Error(err))
			} else {
				n.logger.Debug("online report succeeded", zap.Int("online_users", onlineCount))
			}
		}
	}
}

func (n *Node) Shutdown(ctx context.Context) {
	n.logger.Info("shutting down xboard node")

	if n.trafficReporter != nil {
		n.logger.Info("flushing final traffic snapshot")
		n.trafficReporter.Report(ctx)
	}

	n.logger.Info("stopping sing-box")
	if err := n.engine.Close(); err != nil {
		n.logger.Error("failed to stop sing-box cleanly", zap.Error(err))
	}

	n.logger.Info("xboard node stopped")
}
