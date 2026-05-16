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

// UserSyncer 负责定时同步 Xboard 下发的用户和节点配置。
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
		s.logger.Warn("同步 Xboard 用户失败", zap.Error(err))
		return
	}

	newConfig, err := s.panelClient.GetNodeConfig()
	if err != nil {
		s.logger.Warn("刷新 Xboard 节点配置失败，将继续使用旧配置", zap.Error(err))
		newConfig = s.nodeConfig
	}
	if newConfig == nil {
		s.logger.Warn("节点配置为空，跳过本次热重载")
		return
	}

	newUserHash := hashUsers(newUsers)
	newConfigHash := hashConfig(newConfig)

	usersChanged := newUserHash != s.currentUserHash
	configChanged := newConfigHash != s.currentConfigHash

	if !usersChanged && !configChanged {
		s.logger.Debug("Xboard 下发状态未变化", zap.Int("user_count", len(newUsers)))
		return
	}

	if configChanged {
		s.logger.Info("检测到节点配置变更",
			zap.String("server_name", newConfig.TLSSettings.ServerName),
		)
	}
	if usersChanged {
		s.logger.Info("检测到用户列表变更", zap.Int("user_count", len(newUsers)))
	}

	if s.trafficReporter != nil {
		s.trafficReporter.Report(ctx)
	}

	if err := s.engine.ReloadUsers(newConfig, newUsers, s.listenPort, s.logLevel); err != nil {
		s.logger.Error("热重载 sing-box 失败", zap.Error(err))
		return
	}

	s.currentUserHash = newUserHash
	s.currentConfigHash = newConfigHash
	s.nodeConfig = newConfig

	s.logger.Info("热重载完成", zap.Int("user_count", len(newUsers)))
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
