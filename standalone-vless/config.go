package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config 定义了独立版 VLESS 节点的完整配置结构
type Config struct {
	ListenIP           string      `json:"listen_ip"`
	ServerPort         int         `json:"server_port"`
	Flow               string      `json:"flow"`
	LogLevel           string      `json:"log_level"`
	ClashAPIListenAddr string      `json:"clash_api_listen_addr"`
	GoogleIPv6         bool        `json:"google_ipv6"`
	TLSSettings        TLSSettings `json:"tls_settings"`
	Limits             Limits      `json:"limits"`
	Users              []User      `json:"users"`
}

// TLSSettings 包含 Reality 握手目标及 TLS 密钥配置
type TLSSettings struct {
	ServerName string   `json:"server_name"`
	ServerPort string   `json:"server_port"`
	PrivateKey string   `json:"private_key"`
	ShortID    []string `json:"short_id"`
}

// Limits 包含全局的用户连接限制
type Limits struct {
	MaxConnPerUser          int `json:"max_conn_per_user"`
	MaxConnPerIP            int `json:"max_conn_per_ip"`
	MaxNewConnPerUserPerMin int `json:"max_new_conn_per_user_per_min"`
	MaxNewConnPerIPPerMin   int `json:"max_new_conn_per_ip_per_min"`
}

// User 定义了 VLESS 用户凭据及该用户的单独限速、限设备配置
type User struct {
	Name        string `json:"name"`
	UUID        string `json:"uuid"`
	SpeedLimit  int    `json:"speed_limit"`  // 单位 Mbps，0 表示不限制
	DeviceLimit int    `json:"device_limit"` // 最大活跃 IP 数，0 表示不限制
}

// LoadConfig 从指定的文件路径中读取并解析 JSON 配置文件
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 基础参数合法性检验
	if cfg.ServerPort <= 0 || cfg.ServerPort > 65535 {
		return nil, fmt.Errorf("无效的 server_port: %d", cfg.ServerPort)
	}
	if strings.TrimSpace(cfg.TLSSettings.ServerName) == "" {
		return nil, fmt.Errorf("tls_settings.server_name 不能为空")
	}
	if strings.TrimSpace(cfg.TLSSettings.PrivateKey) == "" {
		return nil, fmt.Errorf("tls_settings.private_key 不能为空")
	}
	if len(cfg.Users) == 0 {
		return nil, fmt.Errorf("用户列表（users）不能为空，至少需要配置一个用户")
	}

	// 用户信息简单去重和合法性校验
	seenNames := make(map[string]bool)
	seenUUIDs := make(map[string]bool)
	for i, u := range cfg.Users {
		name := strings.TrimSpace(u.Name)
		uuid := strings.TrimSpace(u.UUID)
		if name == "" {
			return nil, fmt.Errorf("第 %d 个用户的用户名（name）不能为空", i+1)
		}
		if uuid == "" {
			return nil, fmt.Errorf("用户 %s 的 UUID 不能为空", name)
		}
		if seenNames[name] {
			return nil, fmt.Errorf("重复的用户名: %s", name)
		}
		if seenUUIDs[uuid] {
			return nil, fmt.Errorf("重复的 UUID: %s", uuid)
		}
		seenNames[name] = true
		seenUUIDs[uuid] = true
	}

	return &cfg, nil
}
