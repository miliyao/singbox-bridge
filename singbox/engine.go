package singbox

import (
	"context"
	"fmt"
	"sync"

	"phantom-node/panel"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	boxService "github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	dnsTransport "github.com/sagernet/sing-box/dns/transport"
	"github.com/sagernet/sing-box/dns/transport/local"
	_ "github.com/sagernet/sing-box/experimental/v2rayapi"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/vless"
)

// Engine 管理内嵌 sing-box 实例的生命周期。
type Engine struct {
	mu            sync.Mutex
	instance      *box.Box
	users         []panel.User
	stats         *StatsClient
	currentConfig *panel.NodeConfig
	listenPort    int
	logLevel      string
}

func NewEngine() *Engine {
	return &Engine{}
}

func createContext() context.Context {
	ctx := context.Background()

	inboundRegistry := inbound.NewRegistry()
	vless.RegisterInbound(inboundRegistry)

	outboundRegistry := outbound.NewRegistry()
	direct.RegisterOutbound(outboundRegistry)

	endpointRegistry := endpoint.NewRegistry()

	dnsTransportRegistry := dns.NewTransportRegistry()
	dnsTransport.RegisterUDP(dnsTransportRegistry)
	dnsTransport.RegisterTCP(dnsTransportRegistry)
	local.RegisterTransport(dnsTransportRegistry)

	serviceRegistry := boxService.NewRegistry()

	return box.Context(ctx, inboundRegistry, outboundRegistry, endpointRegistry, dnsTransportRegistry, serviceRegistry)
}

func (e *Engine) Start(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) error {
	instance, err := e.createBox(nodeConfig, users, listenPort, logLevel)
	if err != nil {
		return err
	}

	if err := instance.Start(); err != nil {
		instance.Close()
		return fmt.Errorf("启动 sing-box 失败: %w", err)
	}

	statsClient, statsErr := connectStatsClient()
	if statsErr != nil {
		fmt.Printf("警告: 连接 sing-box Stats 接口失败: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = instance
	e.users = cloneUsers(users)
	e.stats = statsClient
	e.currentConfig = nodeConfig
	e.listenPort = listenPort
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func (e *Engine) ReloadUsers(nodeConfig *panel.NodeConfig, newUsers []panel.User, listenPort int, logLevel string) error {
	newInstance, err := e.createBox(nodeConfig, newUsers, listenPort, logLevel)
	if err != nil {
		return fmt.Errorf("构建新的 sing-box 实例失败: %w", err)
	}

	e.mu.Lock()
	oldInstance := e.instance
	oldStats := e.stats
	oldUsers := cloneUsers(e.users)
	oldConfig := e.currentConfig
	oldListenPort := e.listenPort
	oldLogLevel := e.logLevel
	e.instance = nil
	e.stats = nil
	e.mu.Unlock()

	if oldStats != nil {
		_ = oldStats.Close()
	}
	if oldInstance != nil {
		_ = oldInstance.Close()
	}

	if err := newInstance.Start(); err != nil {
		_ = newInstance.Close()

		rollbackErr := e.restorePreviousInstance(oldConfig, oldUsers, oldListenPort, oldLogLevel)
		if rollbackErr != nil {
			return fmt.Errorf("启动新的 sing-box 实例失败: %w；回滚也失败: %v", err, rollbackErr)
		}

		return fmt.Errorf("启动新的 sing-box 实例失败，已回滚到旧配置: %w", err)
	}

	newStats, statsErr := connectStatsClient()
	if statsErr != nil {
		fmt.Printf("警告: 热重载后连接 sing-box Stats 接口失败: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = newInstance
	e.users = cloneUsers(newUsers)
	e.stats = newStats
	e.currentConfig = nodeConfig
	e.listenPort = listenPort
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func (e *Engine) createBox(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) (*box.Box, error) {
	opts, err := BuildConfig(nodeConfig, users, listenPort, logLevel)
	if err != nil {
		return nil, fmt.Errorf("生成 sing-box 配置失败: %w", err)
	}

	ctx := createContext()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: opts,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 sing-box 实例失败: %w", err)
	}
	return instance, nil
}

func (e *Engine) CollectTraffic(ctx context.Context) ([]UserTraffic, error) {
	e.mu.Lock()
	stats := e.stats
	e.mu.Unlock()

	if stats == nil {
		return nil, nil
	}

	return stats.QueryUserTraffic(ctx)
}

func (e *Engine) GetOnlineCount(ctx context.Context) int {
	e.mu.Lock()
	stats := e.stats
	e.mu.Unlock()

	if stats == nil {
		return 0
	}

	count, err := stats.GetOnlineCount(ctx)
	if err != nil {
		return 0
	}
	return count
}

func (e *Engine) GetUsers() []panel.User {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneUsers(e.users)
}

func (e *Engine) Close() error {
	e.mu.Lock()
	instance := e.instance
	stats := e.stats
	e.instance = nil
	e.stats = nil
	e.users = nil
	e.currentConfig = nil
	e.listenPort = 0
	e.logLevel = ""
	e.mu.Unlock()

	if stats != nil {
		_ = stats.Close()
	}
	if instance != nil {
		return instance.Close()
	}
	return nil
}

func (e *Engine) restorePreviousInstance(nodeConfig *panel.NodeConfig, users []panel.User, listenPort int, logLevel string) error {
	if nodeConfig == nil {
		return fmt.Errorf("旧实例配置不存在，无法回滚")
	}

	instance, err := e.createBox(nodeConfig, users, listenPort, logLevel)
	if err != nil {
		return fmt.Errorf("重建旧的 sing-box 实例失败: %w", err)
	}

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return fmt.Errorf("重新启动旧的 sing-box 实例失败: %w", err)
	}

	statsClient, statsErr := connectStatsClient()
	if statsErr != nil {
		fmt.Printf("警告: 回滚后连接 sing-box Stats 接口失败: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = instance
	e.users = cloneUsers(users)
	e.stats = statsClient
	e.currentConfig = nodeConfig
	e.listenPort = listenPort
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func connectStatsClient() (*StatsClient, error) {
	return NewStatsClient(StatsListenAddr)
}

func cloneUsers(users []panel.User) []panel.User {
	if len(users) == 0 {
		return nil
	}

	cloned := make([]panel.User, len(users))
	copy(cloned, users)
	return cloned
}
