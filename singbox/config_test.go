package singbox

import (
	"encoding/json"
	"testing"
	"time"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"singbox-bridge/panel"
)

func TestBuildConfigUsesDefaultsAndPrependsSafetyRules(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		ListenIP: "",
		Network:  "tcp",
		Flow:     "",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
			ServerPort: "",
			ShortID:    "abcd",
		},
		Routes: json.RawMessage(`[{"domain":["example.com"],"outbound":"direct"}]`),
	}
	users := []panel.User{{ID: 1, UUID: "uuid-1"}}

	opts, err := BuildConfig(nodeConfig, users, 443, "info", "127.0.0.1:10085", "", false)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}

	vlessOptions, ok := opts.Inbounds[0].Options.(*option.VLESSInboundOptions)
	if !ok {
		t.Fatalf("unexpected inbound options type: %T", opts.Inbounds[0].Options)
	}
	if len(vlessOptions.Users) != 1 || vlessOptions.Users[0].Flow != defaultVLESSFlow {
		t.Fatalf("unexpected inbound users: %#v", vlessOptions.Users)
	}
	if !vlessOptions.ListenOptions.ReuseAddr || !vlessOptions.ListenOptions.TCPFastOpen {
		t.Fatalf("expected reuse_addr and tcp_fast_open to be enabled: %#v", vlessOptions.ListenOptions)
	}
	if time.Duration(vlessOptions.ListenOptions.TCPKeepAlive) != defaultTCPKeepAlive {
		t.Fatalf("unexpected tcp_keep_alive: %v", time.Duration(vlessOptions.ListenOptions.TCPKeepAlive))
	}
	if time.Duration(vlessOptions.ListenOptions.TCPKeepAliveInterval) != defaultTCPKeepAliveInterval {
		t.Fatalf("unexpected tcp_keep_alive_interval: %v", time.Duration(vlessOptions.ListenOptions.TCPKeepAliveInterval))
	}
	if !vlessOptions.ListenOptions.SniffEnabled {
		t.Fatal("expected sniff to be enabled")
	}

	if opts.Route == nil || len(opts.Route.Rules) != 7 {
		t.Fatalf("expected 7 route rules, got %#v", opts.Route)
	}
	if !opts.Route.AutoDetectInterface {
		t.Fatal("expected auto_detect_interface to be enabled")
	}
	if opts.Route.Final != directOutboundTag {
		t.Fatalf("expected final route to be %q, got %q", directOutboundTag, opts.Route.Final)
	}
	if opts.Route.Rules[0].DefaultOptions.Protocol[0] != C.ProtocolBitTorrent {
		t.Fatalf("expected first route to block bittorrent, got %#v", opts.Route.Rules[0])
	}
	if opts.Route.Rules[0].DefaultOptions.RuleAction.Action != C.RuleActionTypeReject {
		t.Fatalf("expected first route action reject, got %#v", opts.Route.Rules[0].DefaultOptions.RuleAction)
	}
	if !opts.Route.Rules[1].DefaultOptions.IPIsPrivate {
		t.Fatalf("expected second route to direct private IPs, got %#v", opts.Route.Rules[1])
	}
	// 验证新增的高危端口和国内流量拦截
	if opts.Route.Rules[2].DefaultOptions.Port[0] != 25 || opts.Route.Rules[2].DefaultOptions.RuleAction.Action != C.RuleActionTypeReject {
		t.Fatalf("expected third route to reject SMTP port 25, got %#v", opts.Route.Rules[2])
	}
	if opts.Route.Rules[3].DefaultOptions.Port[0] != 445 || opts.Route.Rules[3].DefaultOptions.PortRange[0] != "135:139" || opts.Route.Rules[3].DefaultOptions.RuleAction.Action != C.RuleActionTypeReject {
		t.Fatalf("expected fourth route to reject high-risk ports, got %#v", opts.Route.Rules[3])
	}
	if opts.Route.Rules[4].DefaultOptions.GeoIP[0] != "cn" || opts.Route.Rules[4].DefaultOptions.RuleAction.Action != C.RuleActionTypeReject {
		t.Fatalf("expected fifth route to reject GeoIP cn, got %#v", opts.Route.Rules[4])
	}
	if opts.Route.Rules[5].DefaultOptions.Geosite[0] != "cn" || opts.Route.Rules[5].DefaultOptions.RuleAction.Action != C.RuleActionTypeReject {
		t.Fatalf("expected sixth route to reject Geosite cn, got %#v", opts.Route.Rules[5])
	}
	if opts.DNS == nil || len(opts.DNS.Rules) != 1 {
		t.Fatalf("expected 1 dns reject rule, got %#v", opts.DNS)
	}
	if len(opts.DNS.Rules[0].DefaultOptions.DomainKeyword) == 0 {
		t.Fatalf("expected dns rule to contain domain keywords, got %#v", opts.DNS.Rules[0])
	}
	if opts.Experimental == nil || opts.Experimental.V2RayAPI == nil || opts.Experimental.V2RayAPI.Listen != "127.0.0.1:10085" {
		t.Fatalf("unexpected stats listen addr: %#v", opts.Experimental)
	}
	if opts.Experimental.ClashAPI != nil {
		t.Fatalf("expected clash api to be disabled by default, got %#v", opts.Experimental.ClashAPI)
	}
}

func TestBuildConfigEnablesClashAPIWhenConfigured(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
	}

	opts, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	if opts.Experimental.ClashAPI == nil || opts.Experimental.ClashAPI.ExternalController != "127.0.0.1:10086" {
		t.Fatalf("expected configured clash api, got %#v", opts.Experimental.ClashAPI)
	}
}

func TestBuildConfigRejectsUnsupportedNetwork(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "ws",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
	}

	if _, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false); err == nil {
		t.Fatal("expected unsupported network error")
	}
}

func TestBuildConfigAcceptsSpeedLimit(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
	}

	_, err := BuildConfig(nodeConfig, []panel.User{{ID: 1, UUID: "uuid-1", SpeedLimit: 10}}, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
}

func TestBuildConfigAcceptsRouteObject(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
		Routes: json.RawMessage(`{"rules":[{"domain_suffix":["example.com"],"outbound":"direct"}],"final":"direct"}`),
	}

	opts, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	if opts.Route == nil || opts.Route.Final != "direct" || len(opts.Route.Rules) != 7 {
		t.Fatalf("unexpected route options: %#v", opts.Route)
	}
}

func TestBuildConfigRejectsInvalidRoutePayload(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
		Routes: json.RawMessage(`{"rules":[{"type":"unknown"}]}`),
	}

	if _, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false); err == nil {
		t.Fatal("expected route decode error")
	}
}

func TestBuildConfigConvertsLegacyRouteRules(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
		Routes: json.RawMessage(`[
			{"type":"field","domain":["example.com"],"outbound":"direct"},
			{"type":"reject","protocol":["bitTorrent"]}
		]`),
	}

	opts, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", false)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}
	if opts.Route == nil || len(opts.Route.Rules) != 8 {
		t.Fatalf("unexpected legacy route conversion: %#v", opts.Route)
	}
	if opts.Route.Final != "direct" {
		t.Fatalf("unexpected legacy route final: %q", opts.Route.Final)
	}
}

func TestBuildConfigEnablesGoogleIPv6(t *testing.T) {
	nodeConfig := &panel.NodeConfig{
		Protocol: "vless",
		Network:  "tcp",
		TLSSettings: panel.TLSSettings{
			ServerName: "example.com",
			PrivateKey: "private-key",
		},
	}

	opts, err := BuildConfig(nodeConfig, nil, 443, "info", "127.0.0.1:10085", "127.0.0.1:10086", true)
	if err != nil {
		t.Fatalf("BuildConfig returned error: %v", err)
	}

	// 检查是否有 direct-v6 outbound
	var foundOutbound bool
	for _, outbound := range opts.Outbounds {
		if outbound.Tag == "direct-v6" && outbound.Type == "direct" {
			foundOutbound = true
			if directOpts, ok := outbound.Options.(*option.DirectOutboundOptions); !ok || uint8(directOpts.DomainStrategy) != 2 {
				t.Fatalf("unexpected direct-v6 outbound options: %#v", outbound.Options)
			}
		}
	}
	if !foundOutbound {
		t.Fatal("expected direct-v6 outbound not found")
	}

	// 检查是否有 google 路由规则
	var foundRule bool
	for _, rule := range opts.Route.Rules {
		if len(rule.DefaultOptions.Geosite) == 1 && rule.DefaultOptions.Geosite[0] == "google" {
			foundRule = true
			if rule.DefaultOptions.RouteOptions.Outbound != "direct-v6" {
				t.Fatalf("unexpected outbound for google rule: %q", rule.DefaultOptions.RouteOptions.Outbound)
			}
		}
	}
	if !foundRule {
		t.Fatal("expected google route rule not found")
	}
}

