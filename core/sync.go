package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"singbox-bridge/panel"

	"go.uber.org/zap"
)

type syncEngine interface {
	ReloadUsers(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) error
}

type syncPanelClient interface {
	GetNodeConfig() (*panel.NodeConfig, error)
	GetUsers() ([]panel.User, error)
	GetUserAlive() (panel.AliveList, error)
}

// UserSyncer periodically refreshes Xboard users and node config.
type UserSyncer struct {
	mu sync.Mutex

	engine      syncEngine
	panelClient syncPanelClient
	nodeConfig  *panel.NodeConfig
	listenPort  int
	logLevel    string
	logger      *zap.Logger

	currentUserHash   string
	currentConfigHash string

	trafficReporter *TrafficReporter
	limiter         *Limiter

	lastSyncAt      time.Time
	lastSyncOK      bool
	lastSyncError   string
	lastReloadAt    time.Time
	lastReloadOK    bool
	lastReloadError string
	lastUserCount   int
	lastConfigHash  string
}

func NewUserSyncer(
	engine syncEngine,
	panelClient syncPanelClient,
	nodeConfig *panel.NodeConfig,
	listenPort int,
	logLevel string,
	logger *zap.Logger,
	trafficReporter *TrafficReporter,
) *UserSyncer {
	return &UserSyncer{
		engine:          engine,
		panelClient:     panelClient,
		nodeConfig:      nodeConfig,
		listenPort:      listenPort,
		logLevel:        logLevel,
		logger:          logger,
		trafficReporter: trafficReporter,
	}
}

func (s *UserSyncer) SetInitialHash(users []panel.User) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentUserHash = hashUsers(users)
	s.currentConfigHash = hashConfig(s.nodeConfig)
	s.lastUserCount = len(users)
	s.lastConfigHash = s.currentConfigHash
}

func (s *UserSyncer) SetLimiter(limiter *Limiter) {
	s.limiter = limiter
}

func (s *UserSyncer) Sync(ctx context.Context) {
	s.mu.Lock()
	currentUserHash := s.currentUserHash
	currentConfigHash := s.currentConfigHash
	currentNodeConfig := s.nodeConfig
	s.mu.Unlock()

	newUsers, err := s.panelClient.GetUsers()
	if err != nil {
		s.markSync(false, err.Error(), -1, "")
		s.logger.Warn("failed to sync xboard users", zap.Error(err))
		return
	}
	if s.limiter != nil {
		s.limiter.UpdateUsers(newUsers)
	}

	newConfig, err := s.panelClient.GetNodeConfig()
	if err != nil {
		s.logger.Warn("failed to refresh xboard node config, keeping the previous config", zap.Error(err))
		newConfig = currentNodeConfig
	}
	if newConfig == nil {
		s.markSync(false, "node config is nil", len(newUsers), "")
		s.logger.Warn("node config is nil, skipping reload")
		return
	}

	newUserHash := hashUsers(newUsers)
	newConfigHash := hashConfig(newConfig)

	usersChanged := newUserHash != currentUserHash
	configChanged := newConfigHash != currentConfigHash

	if !usersChanged && !configChanged {
		s.markSync(true, "", len(newUsers), newConfigHash)
		s.logger.Debug("xboard state unchanged", zap.Int("user_count", len(newUsers)))
		return
	}

	if configChanged {
		s.logger.Info("detected node config change",
			zap.String("server_name", newConfig.TLSSettings.ServerName),
			zap.String("network", newConfig.Network),
			zap.String("flow", newConfig.Flow),
			zap.Bool("has_routes", len(newConfig.Routes) > 0),
		)
	}
	if usersChanged {
		s.logger.Info("detected user list change", zap.Int("user_count", len(newUsers)))
	}

	if s.trafficReporter != nil {
		s.trafficReporter.Report(ctx)
	}

	refreshAliveCounts(s.panelClient, s.limiter, s.logger, "sync")

	if err := s.engine.ReloadUsers(newConfig, newUsers, s.listenPort, s.logLevel); err != nil {
		s.markSync(false, err.Error(), len(newUsers), newConfigHash)
		s.markReload(false, err.Error())
		s.logger.Error("failed to reload sing-box",
			zap.Error(err),
			zap.Bool("users_changed", usersChanged),
			zap.Bool("config_changed", configChanged),
		)
		return
	}

	s.mu.Lock()
	s.currentUserHash = newUserHash
	s.currentConfigHash = newConfigHash
	s.nodeConfig = newConfig
	s.mu.Unlock()
	s.markSync(true, "", len(newUsers), newConfigHash)
	s.markReload(true, "")

	s.logger.Info("sing-box reload completed",
		zap.Int("user_count", len(newUsers)),
		zap.String("config_hash", newConfigHash),
	)
}

func (s *UserSyncer) markSync(ok bool, err string, userCount int, configHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastSyncAt = time.Now()
	s.lastSyncOK = ok
	s.lastSyncError = err
	if userCount >= 0 {
		s.lastUserCount = userCount
	}
	if configHash != "" {
		s.lastConfigHash = configHash
	}
}

func (s *UserSyncer) markReload(ok bool, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastReloadAt = time.Now()
	s.lastReloadOK = ok
	s.lastReloadError = err
}

func (s *UserSyncer) Snapshot() SyncSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return SyncSnapshot{
		LastSyncAt:      s.lastSyncAt,
		LastSyncOK:      s.lastSyncOK,
		LastSyncError:   s.lastSyncError,
		LastReloadAt:    s.lastReloadAt,
		LastReloadOK:    s.lastReloadOK,
		LastReloadError: s.lastReloadError,
		UserCount:       s.lastUserCount,
		ConfigHash:      s.lastConfigHash,
	}
}

type SyncSnapshot struct {
	LastSyncAt      time.Time `json:"last_sync_at,omitempty"`
	LastSyncOK      bool      `json:"last_sync_ok"`
	LastSyncError   string    `json:"last_sync_error,omitempty"`
	LastReloadAt    time.Time `json:"last_reload_at,omitempty"`
	LastReloadOK    bool      `json:"last_reload_ok"`
	LastReloadError string    `json:"last_reload_error,omitempty"`
	UserCount       int       `json:"user_count"`
	ConfigHash      string    `json:"config_hash"`
}

func hashUsers(users []panel.User) string {
	identities := make([]string, len(users))
	for i, user := range users {
		identities[i] = fmt.Sprintf("%d:%s:%d", user.ID, user.UUID, user.SpeedLimit)
	}
	sort.Strings(identities)

	var builder strings.Builder
	for _, identity := range identities {
		builder.WriteString(identity)
		builder.WriteByte('|')
	}

	hash := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", hash[:8])
}

func hashConfig(config *panel.NodeConfig) string {
	if config == nil {
		return "nil"
	}

	data, err := json.Marshal(config)
	if err != nil {
		return "marshal-error"
	}

	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8])
}
