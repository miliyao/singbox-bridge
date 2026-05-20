package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	defaultSyncInterval   = 60
	defaultReportInterval = 60
	defaultLogLevel       = "info"
	defaultStatsListen    = "127.0.0.1:10085"
	defaultClashAPIListen = ""
	defaultStatusListen   = "127.0.0.1:10087"

	defaultMaxConnPerUser          = 32
	defaultMaxConnPerIP            = 20
	defaultMaxNewConnPerUserPerMin = 120
	defaultMaxNewConnPerIPPerMin   = 60
	defaultTrafficPendingMaxUsers  = 10000
)

// Config stores the runtime configuration loaded from the environment.
type Config struct {
	PanelHost  string
	PanelToken string
	NodeID     int
	NodeIDs    []int

	SyncInterval   int
	ReportInterval int
	LogLevel       string

	StatsListenAddr    string
	StatusListenAddr   string
	ClashAPIListenAddr string
	TrafficStateFile   string

	MaxConnPerUser          int
	MaxConnPerIP            int
	MaxNewConnPerUserPerMin int
	MaxNewConnPerIPPerMin   int
	TrafficPendingMaxUsers  int

	GoogleIPv6 bool

	SyncIntervalExplicit   bool
	ReportIntervalExplicit bool
}

func Load() (*Config, error) {
	_ = tryLoadEnvFile("/etc/singbox-bridge.env")

	panelHost, err := requireEnv("PANEL_HOST")
	if err != nil {
		return nil, err
	}

	panelToken, err := requireEnv("PANEL_TOKEN")
	if err != nil {
		return nil, err
	}

	nodeIDsRaw, err := requireEnv("NODE_ID")
	if err != nil {
		return nil, err
	}

	var nodeIDs []int
	for _, part := range strings.Split(nodeIDsRaw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("NODE_ID must be a comma-separated list of integers: %w", err)
		}
		if id <= 0 {
			return nil, fmt.Errorf("NODE_ID values must be greater than 0")
		}
		nodeIDs = append(nodeIDs, id)
	}
	if len(nodeIDs) == 0 {
		return nil, fmt.Errorf("NODE_ID must contain at least one valid ID")
	}

	syncInterval, syncExplicit, err := loadOptionalPositiveIntEnv("SYNC_INTERVAL", defaultSyncInterval)
	if err != nil {
		return nil, err
	}

	reportInterval, reportExplicit, err := loadOptionalPositiveIntEnv("REPORT_INTERVAL", defaultReportInterval)
	if err != nil {
		return nil, err
	}
	logLevel, err := loadOptionalLogLevelEnv("LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return nil, err
	}

	statsListenAddr := loadOptionalStringEnv("STATS_LISTEN_ADDR", defaultStatsListen)
	statusListenAddr := loadOptionalStringEnv("STATUS_LISTEN_ADDR", defaultStatusListen)
	clashAPIListenAddr := loadOptionalStringEnv("CLASH_API_LISTEN_ADDR", defaultClashAPIListen)
	trafficStateFile := loadOptionalStringEnv("TRAFFIC_STATE_FILE", defaultTrafficStateFile())

	maxConnPerUser, err := loadOptionalPositiveIntEnvValue("MAX_CONN_PER_USER", defaultMaxConnPerUser)
	if err != nil {
		return nil, err
	}
	maxConnPerIP, err := loadOptionalPositiveIntEnvValue("MAX_CONN_PER_IP", defaultMaxConnPerIP)
	if err != nil {
		return nil, err
	}
	maxNewConnPerUser, err := loadOptionalPositiveIntEnvValue("MAX_NEW_CONN_PER_USER_PER_MIN", defaultMaxNewConnPerUserPerMin)
	if err != nil {
		return nil, err
	}
	maxNewConnPerIP, err := loadOptionalPositiveIntEnvValue("MAX_NEW_CONN_PER_IP_PER_MIN", defaultMaxNewConnPerIPPerMin)
	if err != nil {
		return nil, err
	}
	trafficPendingMaxUsers, err := loadOptionalPositiveIntEnvValue("TRAFFIC_PENDING_MAX_USERS", defaultTrafficPendingMaxUsers)
	if err != nil {
		return nil, err
	}

	googleIPv6 := loadOptionalBoolEnv("GOOGLE_IPV6")

	return &Config{
		PanelHost:               panelHost,
		PanelToken:              panelToken,
		NodeID:                  nodeIDs[0],
		NodeIDs:                 nodeIDs,
		SyncInterval:            syncInterval,
		ReportInterval:          reportInterval,
		LogLevel:                logLevel,
		StatsListenAddr:         statsListenAddr,
		StatusListenAddr:        statusListenAddr,
		ClashAPIListenAddr:      clashAPIListenAddr,
		TrafficStateFile:        trafficStateFile,
		MaxConnPerUser:          maxConnPerUser,
		MaxConnPerIP:            maxConnPerIP,
		MaxNewConnPerUserPerMin: maxNewConnPerUser,
		MaxNewConnPerIPPerMin:   maxNewConnPerIP,
		TrafficPendingMaxUsers:  trafficPendingMaxUsers,
		GoogleIPv6:              googleIPv6,
		SyncIntervalExplicit:    syncExplicit,
		ReportIntervalExplicit:  reportExplicit,
	}, nil
}

func requireEnv(key string) (string, error) {
	val, ok := lookupTrimmedEnv(key)
	if !ok {
		return "", fmt.Errorf("environment variable %s is required", key)
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
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", key)
	}
	return value, nil
}

func loadOptionalPositiveIntEnv(key string, defaultVal int) (int, bool, error) {
	raw, ok := lookupTrimmedEnv(key)
	if !ok {
		return defaultVal, false, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if value <= 0 {
		return 0, true, fmt.Errorf("%s must be greater than 0", key)
	}
	return value, true, nil
}

func loadOptionalPositiveIntEnvValue(key string, defaultVal int) (int, error) {
	value, _, err := loadOptionalPositiveIntEnv(key, defaultVal)
	return value, err
}

func loadOptionalPortEnv(key string, defaultVal int) (int, error) {
	raw, ok := lookupTrimmedEnv(key)
	if !ok {
		return defaultVal, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("%s must be between 1 and 65535", key)
	}
	return port, nil
}

func loadOptionalLogLevelEnv(key, defaultVal string) (string, error) {
	raw, ok := lookupTrimmedEnv(key)
	if !ok {
		return defaultVal, nil
	}

	switch strings.ToLower(raw) {
	case "debug", "info", "warn", "error":
		return strings.ToLower(raw), nil
	default:
		return "", fmt.Errorf("%s must be one of debug, info, warn, error", key)
	}
}

func loadOptionalStringEnv(key, defaultVal string) string {
	raw, ok := lookupTrimmedEnv(key)
	if !ok {
		return defaultVal
	}
	return raw
}

func lookupTrimmedEnv(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

// loadOptionalBoolEnv 读取布尔环境变量，支持 "true"/"1"/"yes" 为真。
func loadOptionalBoolEnv(key string) bool {
	raw, ok := lookupTrimmedEnv(key)
	if !ok {
		return false
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func defaultTrafficStateFile() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "singbox-bridge", "pending-traffic.json")
	}

	return "/var/lib/singbox-bridge/pending-traffic.json"
}

func tryLoadEnvFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) ||
			(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			if len(val) >= 2 {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return nil
}
