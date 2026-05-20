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
	defaultListenPort     = 443
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

	SyncInterval   int
	ReportInterval int
	ListenPort     int
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

	SyncIntervalExplicit   bool
	ReportIntervalExplicit bool
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

	syncInterval, syncExplicit, err := loadOptionalPositiveIntEnv("SYNC_INTERVAL", defaultSyncInterval)
	if err != nil {
		return nil, err
	}

	reportInterval, reportExplicit, err := loadOptionalPositiveIntEnv("REPORT_INTERVAL", defaultReportInterval)
	if err != nil {
		return nil, err
	}

	listenPort, err := loadOptionalPortEnv("LISTEN_PORT", defaultListenPort)
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

	return &Config{
		PanelHost:               panelHost,
		PanelToken:              panelToken,
		NodeID:                  nodeID,
		SyncInterval:            syncInterval,
		ReportInterval:          reportInterval,
		ListenPort:              listenPort,
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

func defaultTrafficStateFile() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "singbox-bridge", "pending-traffic.json")
	}

	return "/var/lib/singbox-bridge/pending-traffic.json"
}
