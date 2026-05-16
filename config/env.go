package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	defaultSyncInterval   = 60
	defaultReportInterval = 60
	defaultListenPort     = 443
	defaultLogLevel       = "info"
)

// Config 保存节点运行所需的全部环境变量配置。
type Config struct {
	PanelHost  string
	PanelToken string
	NodeID     int

	SyncInterval   int
	ReportInterval int
	ListenPort     int
	LogLevel       string

	CFEnabled    bool
	CFAPIToken   string
	CFZoneID     string
	CFRecordName string
}

func Load() (*Config, error) {
	panelHost, err := requireEnv("PANEL_HOST")
	if err != nil {
		return nil, err
	}

	panelToken, err := requireEnv("PANEL_TOKEN")
	if err != nil {
		return nil, err
	}

	nodeID, err := requirePositiveIntEnv("NODE_ID")
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		PanelHost:      panelHost,
		PanelToken:     panelToken,
		NodeID:         nodeID,
		SyncInterval:   normalizePositiveInt(getEnvInt("SYNC_INTERVAL", defaultSyncInterval), defaultSyncInterval),
		ReportInterval: normalizePositiveInt(getEnvInt("REPORT_INTERVAL", defaultReportInterval), defaultReportInterval),
		ListenPort:     normalizePort(getEnvInt("LISTEN_PORT", defaultListenPort)),
		LogLevel:       normalizeLogLevel(getEnvString("LOG_LEVEL", defaultLogLevel)),
		CFEnabled:      getEnvBool("CF_ENABLED", false),
	}

	if cfg.CFEnabled {
		cfg.CFAPIToken, err = requireEnv("CF_API_TOKEN")
		if err != nil {
			return nil, fmt.Errorf("启用 Cloudflare DNS 时必须设置 CF_API_TOKEN: %w", err)
		}

		cfg.CFZoneID, err = requireEnv("CF_ZONE_ID")
		if err != nil {
			return nil, fmt.Errorf("启用 Cloudflare DNS 时必须设置 CF_ZONE_ID: %w", err)
		}

		cfg.CFRecordName, err = requireEnv("CF_RECORD_NAME")
		if err != nil {
			return nil, fmt.Errorf("启用 Cloudflare DNS 时必须设置 CF_RECORD_NAME: %w", err)
		}
	}

	return cfg, nil
}

func requireEnv(key string) (string, error) {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return "", fmt.Errorf("环境变量 %s 未设置", key)
	}
	return val, nil
}

func requirePositiveIntEnv(key string) (int, error) {
	raw, err := requireEnv(key)
	if err != nil {
		return 0, err
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s 必须大于 0", key)
	}
	return value, nil
}

func getEnvInt(key string, defaultVal int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}

	parsed, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}

func getEnvString(key, defaultVal string) string {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}
	return val
}

func getEnvBool(key string, defaultVal bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultVal
	}

	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return parsed
}

func normalizePositiveInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func normalizePort(port int) int {
	if port < 1 || port > 65535 {
		return defaultListenPort
	}
	return port
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return "debug"
	case "info":
		return "info"
	case "warn":
		return "warn"
	case "error":
		return "error"
	default:
		return defaultLogLevel
	}
}
