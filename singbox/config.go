package singbox

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"singbox-bridge/panel"
)

const (
	defaultRealityDestPort      uint16 = 443
	defaultVLESSFlow                   = "xtls-rprx-vision"
	inboundTag                         = "vless-in"
	directOutboundTag                  = "direct"
	localDNSOutboundTag                = "local-dns"
	defaultSniffTimeout                = time.Second
	defaultTCPKeepAlive                = 5 * time.Minute
	defaultTCPKeepAliveInterval        = 75 * time.Second
)

// BuildConfig translates the Xboard node payload into a sing-box runtime config.
func BuildConfig(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel, statsListenAddr, clashAPIListenAddr string) (option.Options, error) {
	if nodeConfig == nil {
		return option.Options{}, fmt.Errorf("xboard node config must not be nil")
	}
	if strings.TrimSpace(nodeConfig.Protocol) != "" && !strings.EqualFold(strings.TrimSpace(nodeConfig.Protocol), "vless") {
		return option.Options{}, fmt.Errorf("unsupported protocol %q, only vless is supported", nodeConfig.Protocol)
	}
	if err := validateSupportedNetwork(nodeConfig.Network); err != nil {
		return option.Options{}, err
	}
	if strings.TrimSpace(nodeConfig.TLSSettings.ServerName) == "" {
		return option.Options{}, fmt.Errorf("missing tls_settings.server_name")
	}
	if strings.TrimSpace(nodeConfig.TLSSettings.PrivateKey) == "" {
		return option.Options{}, fmt.Errorf("missing tls_settings.private_key")
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

	routes, err := parseRouteOptions(nodeConfig.Routes)
	if err != nil {
		return option.Options{}, err
	}
	routes = mergeDefaultRouteOptions(routes)

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
				Tag:  inboundTag,
				Options: &option.VLESSInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:               (*badoption.Addr)(&listenAddr),
						ListenPort:           uint16(listenPort),
						ReuseAddr:            true,
						TCPFastOpen:          true,
						TCPKeepAlive:         badoption.Duration(defaultTCPKeepAlive),
						TCPKeepAliveInterval: badoption.Duration(defaultTCPKeepAliveInterval),
						InboundOptions: option.InboundOptions{
							SniffEnabled:             true,
							SniffOverrideDestination: false,
							SniffTimeout:             badoption.Duration(defaultSniffTimeout),
						},
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
								ShortID:    badoption.Listable[string](nodeConfig.TLSSettings.ShortIDList()),
							},
						},
					},
				},
			},
		},
		Outbounds: []option.Outbound{
			{
				Type: "direct",
				Tag:  directOutboundTag,
			},
		},
		Route: routes,
		DNS:   buildDefaultDNSOptions(),
		Experimental: &option.ExperimentalOptions{
			CacheFile: &option.CacheFileOptions{
				Enabled: true,
			},
			V2RayAPI: &option.V2RayAPIOptions{
				Listen: statsListenAddr,
				Stats: &option.V2RayStatsServiceOptions{
					Enabled:  true,
					Inbounds: []string{inboundTag},
					Users:    userNames,
				},
			},
		},
	}
	if strings.TrimSpace(clashAPIListenAddr) != "" {
		opts.Experimental.ClashAPI = &option.ClashAPIOptions{
			ExternalController: strings.TrimSpace(clashAPIListenAddr),
		}
	}

	return opts, nil
}

func validateSupportedNetwork(raw string) error {
	network := strings.ToLower(strings.TrimSpace(raw))
	switch network {
	case "", "tcp":
		return nil
	default:
		return fmt.Errorf("unsupported network %q, only tcp is currently supported", raw)
	}
}

func parseRouteOptions(raw json.RawMessage) (*option.RouteOptions, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}

	var route option.RouteOptions
	if err := json.Unmarshal(raw, &route); err == nil {
		return &route, nil
	}

	var rules []option.Rule
	if err := json.Unmarshal(raw, &rules); err == nil {
		return &option.RouteOptions{Rules: rules}, nil
	}

	var legacyRules []legacyRouteRule
	if err := json.Unmarshal(raw, &legacyRules); err == nil {
		if route := convertLegacyRouteRules(legacyRules); route != nil {
			return route, nil
		}
	}

	return nil, fmt.Errorf("failed to decode xboard routes into sing-box route options")
}

func mergeDefaultRouteOptions(route *option.RouteOptions) *option.RouteOptions {
	if route == nil {
		route = &option.RouteOptions{}
	}

	route.AutoDetectInterface = true

	mergedRules := make([]option.Rule, 0, len(route.Rules)+1)
	mergedRules = append(mergedRules, defaultSafetyRules()...)
	mergedRules = append(mergedRules, route.Rules...)
	route.Rules = mergedRules

	if route.Final == "" {
		route.Final = directOutboundTag
	}

	return route
}

func defaultSafetyRules() []option.Rule {
	return []option.Rule{
		rejectRule(option.RawDefaultRule{
			Protocol: badoption.Listable[string]{C.ProtocolBitTorrent},
		}),
		routeRule(option.RawDefaultRule{
			IPIsPrivate: true,
		}),
	}
}

func routeRule(raw option.RawDefaultRule) option.Rule {
	return option.Rule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: raw,
			RuleAction: option.RuleAction{
				Action: C.RuleActionTypeRoute,
				RouteOptions: option.RouteActionOptions{
					Outbound: directOutboundTag,
				},
			},
		},
	}
}

func buildDefaultDNSOptions() *option.DNSOptions {
	return &option.DNSOptions{
		RawDNSOptions: option.RawDNSOptions{
			Servers: []option.DNSServerOptions{
				{
					Type: "local",
					Tag:  localDNSOutboundTag,
					Options: &option.LocalDNSServerOptions{
						PreferGo: true,
					},
				},
			},
			Rules: []option.DNSRule{
				dnsRuleRejectDomain([]string{"ads", "tracker"}),
			},
			Final: localDNSOutboundTag,
		},
	}
}

func dnsRuleRejectDomain(keywords []string) option.DNSRule {
	return option.DNSRule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultDNSRule{
			RawDefaultDNSRule: option.RawDefaultDNSRule{
				DomainKeyword: toList(keywords),
			},
			DNSRuleAction: option.DNSRuleAction{
				Action: C.RuleActionTypeReject,
			},
		},
	}
}

func rejectRule(raw option.RawDefaultRule) option.Rule {
	return option.Rule{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: raw,
			RuleAction: option.RuleAction{
				Action: C.RuleActionTypeReject,
			},
		},
	}
}

func parseRealityDestPort(raw string) (uint16, error) {
	if strings.TrimSpace(raw) == "" {
		return defaultRealityDestPort, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid tls_settings.server_port %q", raw)
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
		return netip.Addr{}, fmt.Errorf("invalid listen_ip %q", raw)
	}
	return addr, nil
}

type legacyRouteRule struct {
	Type          string   `json:"type"`
	Protocol      []string `json:"protocol"`
	Domain        []string `json:"domain"`
	DomainSuffix  []string `json:"domain_suffix"`
	DomainKeyword []string `json:"domain_keyword"`
	DomainRegex   []string `json:"domain_regex"`
	IPCIDR        []string `json:"ip_cidr"`
	GeoIP         []string `json:"geoip"`
	Network       []string `json:"network"`
	Port          []uint16 `json:"port"`
	Outbound      string   `json:"outbound"`
	Final         string   `json:"final"`
}

func convertLegacyRouteRules(rules []legacyRouteRule) *option.RouteOptions {
	if len(rules) == 0 {
		return nil
	}

	route := &option.RouteOptions{}
	for _, item := range rules {
		if route.Final == "" && strings.TrimSpace(item.Final) != "" {
			route.Final = strings.TrimSpace(item.Final)
		}

		rule := option.Rule{}
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "", "field":
			rule.Type = C.RuleTypeDefault
			rule.DefaultOptions = option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Protocol:      toList(item.Protocol),
					Domain:        toList(item.Domain),
					DomainSuffix:  toList(item.DomainSuffix),
					DomainKeyword: toList(item.DomainKeyword),
					DomainRegex:   toList(item.DomainRegex),
					IPCIDR:        toList(item.IPCIDR),
					GeoIP:         toList(item.GeoIP),
					Network:       toList(item.Network),
					Port:          toList(item.Port),
				},
			}
		case "reject":
			rule.Type = C.RuleTypeDefault
			rule.DefaultOptions = option.DefaultRule{
				RawDefaultRule: option.RawDefaultRule{
					Protocol: toList(item.Protocol),
					Domain:   toList(item.Domain),
				},
				RuleAction: option.RuleAction{Action: C.RuleActionTypeReject},
			}
		default:
			continue
		}

		if strings.TrimSpace(item.Outbound) != "" {
			if route.Final == "" {
				route.Final = strings.TrimSpace(item.Outbound)
			}
		}
		route.Rules = append(route.Rules, rule)
	}

	if len(route.Rules) == 0 && route.Final == "" {
		return nil
	}
	if route.Final == "" {
		route.Final = directOutboundTag
	}
	return route
}

func toList[T any](values []T) badoption.Listable[T] {
	if len(values) == 0 {
		return nil
	}
	out := make(badoption.Listable[T], len(values))
	copy(out, values)
	return out
}
