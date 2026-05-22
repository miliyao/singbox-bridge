package core

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"

	"singbox-bridge/panel"

	"go.uber.org/zap"
)

type syncEngine interface {
	ReloadUsers(nodeConfig *panel.NodeConfig, users []panel.User, logLevel string) error
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
	logLevel string,
	logger *zap.Logger,
	trafficReporter *TrafficReporter,
) *UserSyncer {
	return &UserSyncer{
		engine:          engine,
		panelClient:     panelClient,
		nodeConfig:      nodeConfig,
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

	if err := s.engine.ReloadUsers(newConfig, newUsers, s.logLevel); err != nil {
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
	if len(users) == 0 {
		return "empty"
	}

	// 浅拷贝 users 的底层元素（只复制结构体，不复制深层数据），用于在排序时不干扰原有切片
	sortedUsers := make([]panel.User, len(users))
	copy(sortedUsers, users)

	// 按用户 ID 进行数字排序，确保哈希结果的确定性
	sort.Slice(sortedUsers, func(i, j int) bool {
		return sortedUsers[i].ID < sortedUsers[j].ID
	})

	// 流式哈希写入，杜绝字符串拼接和大对象强制转换带来的堆分配与 GC 开销
	h := sha256.New()
	buf := make([]byte, 8)

	for _, user := range sortedUsers {
		// 写入用户 ID
		binary.BigEndian.PutUint64(buf, uint64(user.ID))
		_, _ = h.Write(buf)

		// 写入限速值
		binary.BigEndian.PutUint64(buf, uint64(user.SpeedLimit))
		_, _ = h.Write(buf)

		// 写入 UUID 字节流
		_, _ = h.Write([]byte(user.UUID))

		// 写入分隔符以确保哈希值跟原有分隔保持逻辑的一致性
		_, _ = h.Write([]byte("|"))
	}

	hash := h.Sum(nil)
	return fmt.Sprintf("%x", hash[:8])
}

func hashConfig(config *panel.NodeConfig) string {
	if config == nil {
		return "nil"
	}

	h := sha256.New()

	// 依次写入各个标量字段，确保确定性顺序，使用 \x00 分隔符防止字段边界混淆引发碰撞
	_, _ = h.Write([]byte(config.Protocol))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(config.ListenIP))
	_, _ = h.Write([]byte{0})

	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(config.ServerPort))
	_, _ = h.Write(buf)

	_, _ = h.Write([]byte(config.Network))
	_, _ = h.Write([]byte{0})

	binary.BigEndian.PutUint64(buf, uint64(config.TLS))
	_, _ = h.Write(buf)

	_, _ = h.Write([]byte(config.Flow))
	_, _ = h.Write([]byte{0})

	// TLSSettings
	allowInsecureVal := uint64(0)
	if config.TLSSettings.AllowInsecure {
		allowInsecureVal = 1
	}
	binary.BigEndian.PutUint64(buf, allowInsecureVal)
	_, _ = h.Write(buf)
	_, _ = h.Write([]byte(config.TLSSettings.ServerPort))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(config.TLSSettings.ServerName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(config.TLSSettings.PublicKey))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(config.TLSSettings.PrivateKey))
	_, _ = h.Write([]byte{0})

	// 确定性地对 shortIDs 进行排序再哈希
	shortIDs := config.TLSSettings.ShortIDList()
	if len(shortIDs) > 0 {
		sortedShortIDs := make([]string, len(shortIDs))
		copy(sortedShortIDs, shortIDs)
		sort.Strings(sortedShortIDs)
		for _, sid := range sortedShortIDs {
			_, _ = h.Write([]byte(sid))
		}
	} else {
		_, _ = h.Write([]byte(config.TLSSettings.ShortID))
	}

	// BaseConfig
	binary.BigEndian.PutUint64(buf, uint64(config.BaseConfig.PushInterval))
	_, _ = h.Write(buf)
	binary.BigEndian.PutUint64(buf, uint64(config.BaseConfig.PullInterval))
	_, _ = h.Write(buf)

	// Routes: 解析出 dynamic JSON 并进行排序哈希
	if len(config.Routes) > 0 && string(config.Routes) != "null" {
		var parsedRoutes any
		if err := json.Unmarshal(config.Routes, &parsedRoutes); err == nil {
			hashGenericValue(h, parsedRoutes)
		} else {
			_, _ = h.Write(config.Routes)
		}
	}

	hash := h.Sum(nil)
	return fmt.Sprintf("%x", hash[:8])
}

// hashGenericValue 递归地将任意 JSON 解构的数据以确定性的 Key 排序顺序写入哈希中
func hashGenericValue(h io.Writer, val any) {
	if val == nil {
		_, _ = h.Write([]byte("null"))
		return
	}
	switch v := val.(type) {
	case bool:
		if v {
			_, _ = h.Write([]byte("true"))
		} else {
			_, _ = h.Write([]byte("false"))
		}
	case float64:
		// 使用 IEEE 754 位模式写入，保证全精度且无字符串格式歧义
		var fbuf [8]byte
		binary.BigEndian.PutUint64(fbuf[:], math.Float64bits(v))
		_, _ = h.Write(fbuf[:])
	case int:
		_, _ = h.Write([]byte(fmt.Sprintf("%d", v)))
	case int64:
		_, _ = h.Write([]byte(fmt.Sprintf("%d", v)))
	case string:
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	case []any:
		_, _ = h.Write([]byte("["))
		for _, item := range v {
			hashGenericValue(h, item)
			_, _ = h.Write([]byte(","))
		}
		_, _ = h.Write([]byte("]"))
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		_, _ = h.Write([]byte("{"))
		for _, k := range keys {
			_, _ = h.Write([]byte(k))
			_, _ = h.Write([]byte{0}) // key 边界分隔符
			_, _ = h.Write([]byte(":"))
			hashGenericValue(h, v[k])
			_, _ = h.Write([]byte(","))
		}
		_, _ = h.Write([]byte("}"))
	default:
		_, _ = h.Write([]byte(fmt.Sprintf("%v", v)))
	}
}
