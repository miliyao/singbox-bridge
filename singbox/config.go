package singbox

import (
	"fmt"
	"net/netip"
	"phantom-node/panel"
	"strconv"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

// BuildConfig 根据面板配置和用户列表构建 sing-box 配置
// 固定协议：VLESS + REALITY + XTLS-Vision
func BuildConfig(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) (option.Options, error) {
	// 构建用户列表
	sbUsers := make([]option.VLESSUser, 0, len(users))
	userNames := make([]string, 0, len(users))
	for _, u := range users {
		name := fmt.Sprintf("user-%d", u.ID)
		userNames = append(userNames, name)
		sbUsers = append(sbUsers, option.VLESSUser{
			Name: name,
			UUID: u.UUID,
			Flow: "xtls-rprx-vision",
		})
	}

	// 解析回落目标端口（Xboard 返回的是字符串 "443"）
	destPort := uint16(443)
	if nodeConfig.TLSSettings.ServerPort != "" {
		if p, err := strconv.Atoi(nodeConfig.TLSSettings.ServerPort); err == nil {
			destPort = uint16(p)
		}
	}

	// 伪装域名
	serverName := nodeConfig.TLSSettings.ServerName

	// 监听地址
	listenAddr := netip.IPv6Unspecified() // ::

	// V2Ray Stats 配置
	statsEnabled := true
	v2rayAPI := &option.V2RayAPIOptions{
		Listen: "127.0.0.1:10085",
		Stats: &option.V2RayStatsServiceOptions{
			Enabled:  statsEnabled,
			Inbounds: []string{"vless-in"},
			Users:    userNames,
		},
	}

	// 构建完整配置
	opts := option.Options{
		Log: &option.LogOptions{
			Level:    logLevel,
			Disabled: false,
		},
		Inbounds: []option.Inbound{
			{
				Type: "vless",
				Tag:  "vless-in",
				Options: &option.VLESSInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     (*badoption.Addr)(&listenAddr),
						ListenPort: uint16(listenPort),
					},
					Users: sbUsers,
					InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
						TLS: &option.InboundTLSOptions{
							Enabled:    true,
							ServerName: serverName,
							Reality: &option.InboundRealityOptions{
								Enabled: true,
								Handshake: option.InboundRealityHandshakeOptions{
									ServerOptions: option.ServerOptions{
										Server:     serverName,
										ServerPort: destPort,
									},
								},
								PrivateKey: nodeConfig.TLSSettings.PrivateKey,
								ShortID:    badoption.Listable[string]{nodeConfig.TLSSettings.ShortID},
							},
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: "direct",
				Tag:  "direct",
			},
		},
		Experimental: &option.ExperimentalOptions{
			V2RayAPI: v2rayAPI,
		},
	}

	return opts, nil
}
