package core

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"singbox-bridge/config"
	"singbox-bridge/panel"
	"singbox-bridge/singbox"

	"go.uber.org/zap"
)

type DoctorResult struct {
	OK     bool          `json:"ok"`
	Checks []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func RunDoctor(ctx context.Context, cfg *config.Config, logger *zap.Logger) DoctorResult {
	result := DoctorResult{OK: true}
	add := func(name string, ok bool, message string) {
		result.Checks = append(result.Checks, DoctorCheck{Name: name, OK: ok, Message: message})
		if !ok {
			result.OK = false
		}
	}

	client := panel.NewClient(cfg.PanelHost, cfg.PanelToken, cfg.NodeID)

	nodeConfig, err := client.GetNodeConfig()
	if err != nil {
		add("panel_config", false, err.Error())
		return result
	}
	add("panel_config", true, fmt.Sprintf("protocol=%s network=%s server_name=%s", nodeConfig.Protocol, nodeConfig.Network, nodeConfig.TLSSettings.ServerName))

	users, err := client.GetUsers()
	if err != nil {
		add("panel_users", false, err.Error())
		return result
	}
	add("panel_users", true, fmt.Sprintf("users=%d", len(users)))

	if err := validateUsers(users); err != nil {
		add("users_valid", false, err.Error())
	} else {
		add("users_valid", true, "all users have ids and uuids")
	}

	if _, err := singbox.BuildConfig(nodeConfig, users, cfg.LogLevel, cfg.ClashAPIListenAddr, cfg.GoogleIPv6); err != nil {
		add("singbox_config", false, err.Error())
	} else {
		add("singbox_config", true, "config can be translated")
	}

	if err := checkListenPort(ctx, nodeConfig.ServerPort); err != nil {
		add("listen_port", false, err.Error())
	} else {
		add("listen_port", true, fmt.Sprintf(":%d is available", nodeConfig.ServerPort))
	}

	if _, err := client.GetUserAlive(); err != nil {
		add("alive_list", false, err.Error())
	} else {
		add("alive_list", true, "alive list endpoint is reachable")
	}

	if logger != nil {
		logger.Info("doctor completed", zap.Bool("ok", result.OK), zap.Int("checks", len(result.Checks)))
	}
	return result
}

func validateUsers(users []panel.User) error {
	for _, user := range users {
		if user.ID <= 0 {
			return fmt.Errorf("user has invalid id: %d", user.ID)
		}
		if strings.TrimSpace(user.UUID) == "" {
			return fmt.Errorf("user %d has empty uuid", user.ID)
		}
	}
	return nil
}

func checkListenPort(ctx context.Context, port int) error {
	addr := fmt.Sprintf(":%d", port)
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen port %d is not available: %w", port, err)
	}
	defer listener.Close()

	deadline, ok := ctx.Deadline()
	if ok && time.Until(deadline) <= 0 {
		return context.DeadlineExceeded
	}
	return nil
}
