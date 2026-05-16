package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config 保存所有从环境变量读取的配置项
type Config struct {
	// 必填项
	PanelHost  string // Xboard 面板地址（含 https://）
	PanelToken string // 节点通信 Token
	NodeID     int    // 节点 ID

	// 可选项（带默认值）
	SyncInterval   int    // 用户同步周期（秒），默认 60
	ReportInterval int    // 流量上报周期（秒），默认 60
	ListenPort     int    // sing-box 监听端口，默认 443
	LogLevel       string // 日志级别，默认 info

	// Cloudflare DNS 自注册（参数控制是否启用）
	CFEnabled    bool   // 是否启用 CF DNS 自注册，默认 false
	CFAPIToken   string // CF API Token
	CFZoneID     string // CF Zone ID
	CFRecordName string // DNS 记录名（如 us-pool.xxx.com）
}

// Load 从环境变量加载并校验配置
func Load() (*Config, error) {
	cfg := &Config{}

	// 必填项校验
	cfg.PanelHost = os.Getenv("PANEL_HOST")
	if cfg.PanelHost == "" {
		return nil, fmt.Errorf("环境变量 PANEL_HOST 未设置")
	}

	cfg.PanelToken = os.Getenv("PANEL_TOKEN")
	if cfg.PanelToken == "" {
		return nil, fmt.Errorf("环境变量 PANEL_TOKEN 未设置")
	}

	nodeIDStr := os.Getenv("NODE_ID")
	if nodeIDStr == "" {
		return nil, fmt.Errorf("环境变量 NODE_ID 未设置")
	}
	nodeID, err := strconv.Atoi(nodeIDStr)
	if err != nil {
		return nil, fmt.Errorf("NODE_ID 必须为整数: %w", err)
	}
	cfg.NodeID = nodeID

	// 可选项，带默认值
	cfg.SyncInterval = getEnvInt("SYNC_INTERVAL", 60)
	cfg.ReportInterval = getEnvInt("REPORT_INTERVAL", 60)
	cfg.ListenPort = getEnvInt("LISTEN_PORT", 443)
	cfg.LogLevel = getEnvString("LOG_LEVEL", "info")

	// Cloudflare 相关
	cfg.CFEnabled = getEnvBool("CF_ENABLED", false)
	if cfg.CFEnabled {
		cfg.CFAPIToken = os.Getenv("CF_API_TOKEN")
		if cfg.CFAPIToken == "" {
			return nil, fmt.Errorf("CF_ENABLED=true 时 CF_API_TOKEN 必须设置")
		}
		cfg.CFZoneID = os.Getenv("CF_ZONE_ID")
		if cfg.CFZoneID == "" {
			return nil, fmt.Errorf("CF_ENABLED=true 时 CF_ZONE_ID 必须设置")
		}
		cfg.CFRecordName = os.Getenv("CF_RECORD_NAME")
		if cfg.CFRecordName == "" {
			return nil, fmt.Errorf("CF_ENABLED=true 时 CF_RECORD_NAME 必须设置")
		}
	}

	return cfg, nil
}

// getEnvInt 读取整数型环境变量，不存在或解析失败则返回默认值
func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}

// getEnvString 读取字符串型环境变量，不存在则返回默认值
func getEnvString(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

// getEnvBool 读取布尔型环境变量，不存在则返回默认值
func getEnvBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}
