package singbox

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"phantom-node/panel"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

const (
	StatsListenAddr               = "127.0.0.1:10085"
	defaultRealityDestPort uint16 = 443
	defaultVLESSFlow              = "xtls-rprx-vision"
)

// BuildConfig converts panel state into a sing-box runtime configuration.
// The current implementation targets a VLESS + REALITY deployment.
func BuildConfig(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) (option.Options, error) {
	if nodeConfig == nil {
		return option.Options{}, fmt.Errorf("node config is required")
	}
	if strings.TrimSpace(nodeConfig.TLSSettings.ServerName) == "" {
		return option.Options{}, fmt.Errorf("tls_settings.server_name is required")
	}
	if strings.TrimSpace(nodeConfig.TLSSettings.PrivateKey) == "" {
		return option.Options{}, fmt.Errorf("tls_settings.private_key is required")
	}

	flow := strings.TrimSpace(nodeConfig.Flow)
	if flow == "" {
		flow = defaultVLESSFlow
	}

	destPort, err := parseRealityDestPort(nodeConfig.TLSSettings.ServerPort)
	if err != nil {
		return option.Options{}, err
	}

	listenAddr, err := resolveListenAddr(nodeConfig.ListenIP)
	if err != nil {
		return option.Options{}, err
	}

	sbUsers := make([]option.VLESSUser, 0, len(users))
	userNames := make([]string, 0, len(users))
	for _, user := range users {
		name := fmt.Sprintf("user-%d", user.ID)
		userNames = append(userNames, name)
		sbUsers = append(sbUsers, option.VLESSUser{
			Name: name,
			UUID: user.UUID,
			Flow: flow,
		})
	}

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
							ServerName: nodeConfig.TLSSettings.ServerName,
							Reality: &option.InboundRealityOptions{
								Enabled: true,
								Handshake: option.InboundRealityHandshakeOptions{
									ServerOptions: option.ServerOptions{
										Server:     nodeConfig.TLSSettings.ServerName,
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
			V2RayAPI: &option.V2RayAPIOptions{
				Listen: StatsListenAddr,
				Stats: &option.V2RayStatsServiceOptions{
					Enabled:  true,
					Inbounds: []string{"vless-in"},
					Users:    userNames,
				},
			},
		},
	}

	return opts, nil
}

func parseRealityDestPort(raw string) (uint16, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultRealityDestPort, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid tls_settings.server_port %q: %w", raw, err)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("tls_settings.server_port out of range: %d", port)
	}
	return uint16(port), nil
}

func resolveListenAddr(raw string) (netip.Addr, error) {
	if strings.TrimSpace(raw) == "" {
		return netip.IPv6Unspecified(), nil
	}

	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid listen_ip %q: %w", raw, err)
	}
	return addr, nil
}
