package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"phantom-node/config"
	"phantom-node/panel"
	"phantom-node/singbox"

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
	statusServer    *http.Server
	startedAt       time.Time
	lastAliveAt     time.Time
	lastAliveOK     bool
	lastAliveError  string
	lastOnlineCount int
}

func NewNode(cfg *config.Config, logger *zap.Logger) *Node {
	limiter := NewLimiterWithConfig(LimiterConfig{
		MaxConnPerUser:          cfg.MaxConnPerUser,
		MaxConnPerIP:            cfg.MaxConnPerIP,
		MaxNewConnPerUserPerMin: cfg.MaxNewConnPerUserPerMin,
		MaxNewConnPerIPPerMin:   cfg.MaxNewConnPerIPPerMin,
	})
	return &Node{
		cfg:         cfg,
		engine:      singbox.NewEngine(cfg.StatsListenAddr, cfg.ClashAPIListenAddr, limiter, limiter, logger),
		panelClient: panel.NewClient(cfg.PanelHost, cfg.PanelToken, cfg.NodeID),
		logger:      logger,
		limiter:     limiter,
	}
}

func (n *Node) Start(ctx context.Context) error {
	n.logger.Info("starting xboard node",
		zap.Int("node_id", n.cfg.NodeID),
		zap.String("panel", n.cfg.PanelHost),
	)
	n.mu.Lock()
	n.startedAt = time.Now()
	n.mu.Unlock()

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
	if err := n.engine.Start(nodeConfig, users, n.cfg.ListenPort, n.cfg.LogLevel); err != nil {
		return err
	}
	n.logger.Info("sing-box started", zap.Int("listen_port", n.cfg.ListenPort))

	initialAlivePayload := map[int][]string{}
	if n.limiter != nil {
		initialAlivePayload = n.limiter.BuildAlivePayload()
	}
	if err := n.panelClient.SendAlive(initialAlivePayload); err != nil {
		n.markAlive(false, err.Error(), n.engine.GetOnlineCount(ctx))
		n.logger.Warn("initial online report failed", zap.Error(err))
	} else {
		n.markAlive(true, "", n.engine.GetOnlineCount(ctx))
	}

	n.trafficReporter = NewTrafficReporterWithLimit(n.engine, n.panelClient, n.logger, n.cfg.TrafficStateFile, n.cfg.TrafficPendingMaxUsers)
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
	n.userSyncer.SetLimiter(n.limiter)

	go n.runTickers(ctx)
	n.startStatusServer()

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
				n.markAlive(false, err.Error(), onlineCount)
				n.logger.Warn("online report failed", zap.Error(err))
			} else {
				n.markAlive(true, "", onlineCount)
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

	if n.statusServer != nil {
		n.logger.Info("stopping status server")
		if err := n.statusServer.Shutdown(ctx); err != nil {
			n.logger.Warn("failed to stop status server cleanly", zap.Error(err))
		}
	}

	n.logger.Info("xboard node stopped")
}

func (n *Node) startStatusServer() {
	if n.cfg.StatusListenAddr == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(n.Status())
	})

	server := &http.Server{
		Addr:              n.cfg.StatusListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	n.statusServer = server

	go func() {
		n.logger.Info("status server started", zap.String("listen_addr", n.cfg.StatusListenAddr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			n.logger.Warn("status server stopped unexpectedly", zap.Error(err))
		}
	}()
}

func (n *Node) Status() NodeStatus {
	n.mu.Lock()
	startedAt := n.startedAt
	lastAliveAt := n.lastAliveAt
	lastAliveOK := n.lastAliveOK
	lastAliveError := n.lastAliveError
	lastOnlineCount := n.lastOnlineCount
	n.mu.Unlock()

	status := NodeStatus{
		OK:               true,
		NodeID:           n.cfg.NodeID,
		StartedAt:        startedAt,
		UptimeSeconds:    int64(time.Since(startedAt).Seconds()),
		SyncInterval:     n.cfg.SyncInterval,
		ReportInterval:   n.cfg.ReportInterval,
		ListenPort:       n.cfg.ListenPort,
		StatsListenAddr:  n.cfg.StatsListenAddr,
		StatusListenAddr: n.cfg.StatusListenAddr,
		LastAliveAt:      lastAliveAt,
		LastAliveOK:      lastAliveOK,
		LastAliveError:   lastAliveError,
		OnlineUsers:      lastOnlineCount,
	}
	if n.userSyncer != nil {
		status.Sync = n.userSyncer.Snapshot()
	}
	if n.trafficReporter != nil {
		status.Traffic = n.trafficReporter.Snapshot()
	}
	if n.limiter != nil {
		status.Limiter = n.limiter.Snapshot()
	}
	return status
}

func (n *Node) markAlive(ok bool, err string, onlineCount int) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.lastAliveAt = time.Now()
	n.lastAliveOK = ok
	n.lastAliveError = err
	n.lastOnlineCount = onlineCount
}

type NodeStatus struct {
	OK               bool            `json:"ok"`
	NodeID           int             `json:"node_id"`
	StartedAt        time.Time       `json:"started_at"`
	UptimeSeconds    int64           `json:"uptime_seconds"`
	SyncInterval     int             `json:"sync_interval_seconds"`
	ReportInterval   int             `json:"report_interval_seconds"`
	ListenPort       int             `json:"listen_port"`
	StatsListenAddr  string          `json:"stats_listen_addr"`
	StatusListenAddr string          `json:"status_listen_addr"`
	LastAliveAt      time.Time       `json:"last_alive_at,omitempty"`
	LastAliveOK      bool            `json:"last_alive_ok"`
	LastAliveError   string          `json:"last_alive_error,omitempty"`
	OnlineUsers      int             `json:"online_users"`
	Sync             SyncSnapshot    `json:"sync"`
	Traffic          TrafficSnapshot `json:"traffic"`
	Limiter          LimiterSnapshot `json:"limiter"`
}
