package config

import "testing"

func TestLoadMarksExplicitIntervalsAndOptionalFields(t *testing.T) {
	t.Setenv("PANEL_HOST", "https://panel.example.com")
	t.Setenv("PANEL_TOKEN", "secret")
	t.Setenv("NODE_ID", "7")
	t.Setenv("SYNC_INTERVAL", "60")
	t.Setenv("REPORT_INTERVAL", "120")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("STATS_LISTEN_ADDR", "127.0.0.1:20001")
	t.Setenv("STATUS_LISTEN_ADDR", "127.0.0.1:20003")
	t.Setenv("CLASH_API_LISTEN_ADDR", "127.0.0.1:20002")
	t.Setenv("TRAFFIC_STATE_FILE", "/tmp/pending.json")
	t.Setenv("MAX_CONN_PER_USER", "11")
	t.Setenv("MAX_CONN_PER_IP", "12")
	t.Setenv("MAX_NEW_CONN_PER_USER_PER_MIN", "13")
	t.Setenv("MAX_NEW_CONN_PER_IP_PER_MIN", "14")
	t.Setenv("TRAFFIC_PENDING_MAX_USERS", "15")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.SyncIntervalExplicit || !cfg.ReportIntervalExplicit {
		t.Fatal("expected sync and report intervals to be marked as explicit")
	}
	if cfg.SyncInterval != 60 || cfg.ReportInterval != 120 {
		t.Fatalf("unexpected intervals: sync=%d report=%d", cfg.SyncInterval, cfg.ReportInterval)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.StatsListenAddr != "127.0.0.1:20001" {
		t.Fatalf("StatsListenAddr = %q, want 127.0.0.1:20001", cfg.StatsListenAddr)
	}
	if cfg.StatusListenAddr != "127.0.0.1:20003" {
		t.Fatalf("StatusListenAddr = %q, want 127.0.0.1:20003", cfg.StatusListenAddr)
	}
	if cfg.ClashAPIListenAddr != "127.0.0.1:20002" {
		t.Fatalf("ClashAPIListenAddr = %q, want 127.0.0.1:20002", cfg.ClashAPIListenAddr)
	}
	if cfg.TrafficStateFile != "/tmp/pending.json" {
		t.Fatalf("TrafficStateFile = %q, want /tmp/pending.json", cfg.TrafficStateFile)
	}
	if cfg.MaxConnPerUser != 11 || cfg.MaxConnPerIP != 12 || cfg.MaxNewConnPerUserPerMin != 13 || cfg.MaxNewConnPerIPPerMin != 14 {
		t.Fatalf("unexpected limiter config: %#v", cfg)
	}
	if cfg.TrafficPendingMaxUsers != 15 {
		t.Fatalf("TrafficPendingMaxUsers = %d, want 15", cfg.TrafficPendingMaxUsers)
	}
}

func TestLoadUsesDefaultsWhenOptionalValuesAreMissing(t *testing.T) {
	t.Setenv("PANEL_HOST", "https://panel.example.com")
	t.Setenv("PANEL_TOKEN", "secret")
	t.Setenv("NODE_ID", "7")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.SyncInterval != defaultSyncInterval || cfg.ReportInterval != defaultReportInterval {
		t.Fatalf("unexpected defaults: sync=%d report=%d", cfg.SyncInterval, cfg.ReportInterval)
	}
	if cfg.SyncIntervalExplicit || cfg.ReportIntervalExplicit {
		t.Fatal("expected default intervals to be non-explicit")
	}
	if cfg.StatsListenAddr == "" {
		t.Fatal("expected default stats listen address to be set")
	}
	if cfg.StatusListenAddr == "" {
		t.Fatal("expected default status listen address to be set")
	}
	if cfg.TrafficStateFile == "" {
		t.Fatal("expected default traffic state file to be set")
	}
	if cfg.ClashAPIListenAddr != "" {
		t.Fatalf("expected clash api to be disabled by default, got %q", cfg.ClashAPIListenAddr)
	}
	if cfg.MaxConnPerUser != defaultMaxConnPerUser || cfg.MaxConnPerIP != defaultMaxConnPerIP {
		t.Fatalf("unexpected default limiter config: %#v", cfg)
	}
}

func TestLoadRejectsInvalidOptionalValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid sync interval", key: "SYNC_INTERVAL", value: "0"},
		{name: "invalid report interval", key: "REPORT_INTERVAL", value: "abc"},
		{name: "invalid log level", key: "LOG_LEVEL", value: "trace"},
		{name: "invalid max conn per user", key: "MAX_CONN_PER_USER", value: "0"},
		{name: "invalid max conn per ip", key: "MAX_CONN_PER_IP", value: "-1"},
		{name: "invalid max user conn rate", key: "MAX_NEW_CONN_PER_USER_PER_MIN", value: "abc"},
		{name: "invalid max pending traffic", key: "TRAFFIC_PENDING_MAX_USERS", value: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PANEL_HOST", "https://panel.example.com")
			t.Setenv("PANEL_TOKEN", "secret")
			t.Setenv("NODE_ID", "7")
			t.Setenv(tc.key, tc.value)

			if _, err := Load(); err == nil {
				t.Fatalf("expected Load to reject %s=%q", tc.key, tc.value)
			}
		})
	}
}

func TestLoadParsesMultiNodeIDs(t *testing.T) {
	t.Setenv("PANEL_HOST", "https://panel.example.com")
	t.Setenv("PANEL_TOKEN", "secret")
	t.Setenv("NODE_ID", "5, 6,7 ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.NodeID != 5 {
		t.Fatalf("expected NodeID to be 5, got %d", cfg.NodeID)
	}

	if len(cfg.NodeIDs) != 3 || cfg.NodeIDs[0] != 5 || cfg.NodeIDs[1] != 6 || cfg.NodeIDs[2] != 7 {
		t.Fatalf("unexpected NodeIDs slice: %#v", cfg.NodeIDs)
	}

	// Test invalid NODE_ID list
	t.Setenv("NODE_ID", "5, abc, 7")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail with invalid list item")
	}

	t.Setenv("NODE_ID", "5, -1, 7")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail with negative list item")
	}

	t.Setenv("NODE_ID", ", ,")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to fail with empty list")
	}
}
