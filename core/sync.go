package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"phantom-node/panel"

	"go.uber.org/zap"
)

type syncEngine interface {
	ReloadUsers(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) error
}

type syncPanelClient interface {
	GetNodeConfig() (*panel.NodeConfig, error)
	GetUsers() ([]panel.User, error)
}

// UserSyncer keeps node config and user state in sync with the panel.
type UserSyncer struct {
	engine      syncEngine
	panelClient syncPanelClient
	nodeConfig  *panel.NodeConfig
	listenPort  int
	logLevel    string
	logger      *zap.Logger

	currentUserHash   string
	currentConfigHash string

	trafficReporter *TrafficReporter
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
	s.currentUserHash = hashUsers(users)
	s.currentConfigHash = hashConfig(s.nodeConfig)
}

func (s *UserSyncer) Sync(ctx context.Context) {
	newUsers, err := s.panelClient.GetUsers()
	if err != nil {
		s.logger.Warn("failed to sync users", zap.Error(err))
		return
	}

	newConfig, err := s.panelClient.GetNodeConfig()
	if err != nil {
		s.logger.Warn("failed to refresh node config; using previous config", zap.Error(err))
		newConfig = s.nodeConfig
	}
	if newConfig == nil {
		s.logger.Warn("node config is unavailable; skipping reload")
		return
	}

	newUserHash := hashUsers(newUsers)
	newConfigHash := hashConfig(newConfig)

	usersChanged := newUserHash != s.currentUserHash
	configChanged := newConfigHash != s.currentConfigHash

	if !usersChanged && !configChanged {
		s.logger.Debug("panel state unchanged", zap.Int("user_count", len(newUsers)))
		return
	}

	if configChanged {
		s.logger.Info("detected node config change",
			zap.String("server_name", newConfig.TLSSettings.ServerName),
		)
	}
	if usersChanged {
		s.logger.Info("detected user list change", zap.Int("user_count", len(newUsers)))
	}

	if s.trafficReporter != nil {
		s.trafficReporter.Report(ctx)
	}

	if err := s.engine.ReloadUsers(newConfig, newUsers, s.listenPort, s.logLevel); err != nil {
		s.logger.Error("failed to reload sing-box", zap.Error(err))
		return
	}

	s.currentUserHash = newUserHash
	s.currentConfigHash = newConfigHash
	s.nodeConfig = newConfig

	s.logger.Info("reload complete", zap.Int("user_count", len(newUsers)))
}

func hashUsers(users []panel.User) string {
	identities := make([]string, len(users))
	for i, user := range users {
		identities[i] = fmt.Sprintf("%d:%s", user.ID, user.UUID)
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
