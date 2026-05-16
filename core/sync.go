package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"phantom-node/panel"
	"phantom-node/singbox"

	"go.uber.org/zap"
)

// UserSyncer 负责定时从面板同步用户列表和节点配置，在变更时触发热重载
type UserSyncer struct {
	engine      *singbox.Engine
	panelClient *panel.Client
	nodeConfig  *panel.NodeConfig
	listenPort  int
	logLevel    string
	logger      *zap.Logger

	// 当前用户列表的哈希值，用于 diff 检测
	currentUserHash string
	// 当前节点配置的哈希值，用于 diff 检测
	currentConfigHash string

	// 流量上报器，重载前需要"抢救"旧实例的流量
	trafficReporter *TrafficReporter
}

// NewUserSyncer 创建用户同步器
func NewUserSyncer(
	engine *singbox.Engine,
	panelClient *panel.Client,
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

// SetInitialHash 设置初始哈希值（启动时调用）
func (s *UserSyncer) SetInitialHash(users []panel.User) {
	s.currentUserHash = hashUsers(users)
	s.currentConfigHash = hashConfig(s.nodeConfig)
}

// Sync 执行一次同步，同时检测用户列表和节点配置的变更
func (s *UserSyncer) Sync(ctx context.Context) {
	// 从面板拉取最新用户列表
	newUsers, err := s.panelClient.GetUsers()
	if err != nil {
		s.logger.Warn("用户同步失败", zap.Error(err))
		return
	}

	// 从面板拉取最新节点配置
	newConfig, err := s.panelClient.GetNodeConfig()
	if err != nil {
		s.logger.Warn("节点配置同步失败", zap.Error(err))
		// 配置拉取失败不阻断用户同步，继续用旧配置
		newConfig = s.nodeConfig
	}

	// 计算哈希
	newUserHash := hashUsers(newUsers)
	newConfigHash := hashConfig(newConfig)

	usersChanged := newUserHash != s.currentUserHash
	configChanged := newConfigHash != s.currentConfigHash

	// 无变更则跳过
	if !usersChanged && !configChanged {
		s.logger.Debug("用户列表和节点配置均无变更", zap.Int("用户数", len(newUsers)))
		return
	}

	// 记录变更内容
	if configChanged {
		s.logger.Info("检测到节点配置变更，触发热重载",
			zap.String("新伪装域名", newConfig.TLSSettings.ServerName),
		)
	}
	if usersChanged {
		s.logger.Info("检测到用户列表变更，触发热重载",
			zap.Int("新用户数", len(newUsers)),
		)
	}

	// 重载前"抢救"旧实例的流量数据
	s.trafficReporter.Report(ctx)

	// 执行热重载（使用最新的节点配置和用户列表）
	if err := s.engine.ReloadUsers(newConfig, newUsers, s.listenPort, s.logLevel); err != nil {
		s.logger.Error("热重载失败", zap.Error(err))
		return
	}

	// 更新哈希和配置引用
	s.currentUserHash = newUserHash
	s.currentConfigHash = newConfigHash
	s.nodeConfig = newConfig
	s.logger.Info("热重载完成", zap.Int("当前用户数", len(newUsers)))
}

// hashUsers 计算用户列表的哈希值（基于排序后的 UUID 集合）
func hashUsers(users []panel.User) string {
	// 提取并排序 UUID
	uuids := make([]string, len(users))
	for i, u := range users {
		uuids[i] = fmt.Sprintf("%d:%s", u.ID, u.UUID)
	}
	sort.Strings(uuids)

	// 拼接后计算 SHA256
	combined := ""
	for _, u := range uuids {
		combined += u + "|"
	}

	hash := sha256.Sum256([]byte(combined))
	return fmt.Sprintf("%x", hash[:8]) // 取前 8 字节，够用了
}

// hashConfig 计算节点配置的哈希值
func hashConfig(config *panel.NodeConfig) string {
	// 序列化关键字段后计算哈希
	data, _ := json.Marshal(config)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8])
}
