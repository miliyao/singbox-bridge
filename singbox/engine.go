package singbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"singbox-bridge/panel"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
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
	"github.com/sagernet/sing/service"
	"go.uber.org/zap"
)

// Engine manages the embedded sing-box instance lifecycle.
type Engine struct {
	mu                  sync.Mutex
	instance            *box.Box
	users               []panel.User
	stats               *StatsClient
	currentConfig       *panel.NodeConfig
	logLevel            string
	statsListenAddr     string
	clashAPIListenAddr string
	googleIPv6          bool
	limiter             ConnectionLimiter
	rates               UserRateProvider
	logger              *zap.Logger
	cachePath           string
}

func NewEngine(statsListenAddr, clashAPIListenAddr string, googleIPv6 bool, limiter ConnectionLimiter, rates UserRateProvider, logger *zap.Logger, cachePath string) *Engine {
	return &Engine{
		statsListenAddr:    statsListenAddr,
		clashAPIListenAddr: clashAPIListenAddr,
		googleIPv6:          googleIPv6,
		limiter:            limiter,
		rates:              rates,
		logger:             logger,
		cachePath:          cachePath,
	}
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

func (e *Engine) Start(nodeConfig *panel.NodeConfig, users []panel.User, logLevel string) error {
	instance, err := e.createBox(nodeConfig, users, logLevel)
	if err != nil {
		return err
	}

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	statsClient, statsErr := connectStatsClient(e.statsListenAddr)
	if statsErr != nil {
		fmt.Printf("warning: failed to connect to sing-box stats API: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = instance
	e.users = cloneUsers(users)
	e.stats = statsClient
	e.currentConfig = nodeConfig
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func (e *Engine) ReloadUsers(nodeConfig *panel.NodeConfig, newUsers []panel.User, logLevel string) error {
	newInstance, err := e.createBox(nodeConfig, newUsers, logLevel)
	if err != nil {
		return fmt.Errorf("failed to build new sing-box instance: %w", err)
	}

	e.mu.Lock()
	oldInstance := e.instance
	oldStats := e.stats
	oldUsers := cloneUsers(e.users)
	oldConfig := e.currentConfig
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

		rollbackErr := e.restorePreviousInstance(oldConfig, oldUsers, oldLogLevel)
		if rollbackErr != nil {
			return fmt.Errorf("failed to start new sing-box instance: %w; rollback also failed: %v", err, rollbackErr)
		}

		return fmt.Errorf("failed to start new sing-box instance, rolled back to the previous config: %w", err)
	}

	newStats, statsErr := connectStatsClient(e.statsListenAddr)
	if statsErr != nil {
		fmt.Printf("warning: failed to reconnect to sing-box stats API after reload: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = newInstance
	e.users = cloneUsers(newUsers)
	e.stats = newStats
	e.currentConfig = nodeConfig
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func (e *Engine) createBox(nodeConfig *panel.NodeConfig, users []panel.User, logLevel string) (*box.Box, error) {
	opts, err := BuildConfig(nodeConfig, users, logLevel, e.statsListenAddr, e.clashAPIListenAddr, e.googleIPv6, e.cachePath)
	if err != nil {
		return nil, fmt.Errorf("failed to generate sing-box config: %w", err)
	}

	if e.cachePath != "" {
		if err := os.MkdirAll(filepath.Dir(e.cachePath), 0755); err != nil {
			e.logger.Warn("failed to create cache directory", zap.String("path", e.cachePath), zap.Error(err))
		}
	}

	ctx := createContext()
	instance, err := box.New(box.Options{
		Context: ctx,
		Options: opts,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create sing-box instance: %w", err)
	}

	router := service.FromContext[adapter.Router](ctx)
	if router != nil && e.limiter != nil {
		router.AppendTracker(newLimiterTracker(e.limiter, e.rates, e.logger))
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

func (e *Engine) Close() error {
	e.mu.Lock()
	instance := e.instance
	stats := e.stats
	e.instance = nil
	e.stats = nil
	e.users = nil
	e.currentConfig = nil
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

func (e *Engine) restorePreviousInstance(nodeConfig *panel.NodeConfig, users []panel.User, logLevel string) error {
	if nodeConfig == nil {
		return fmt.Errorf("previous config is missing, cannot roll back")
	}

	instance, err := e.createBox(nodeConfig, users, logLevel)
	if err != nil {
		return fmt.Errorf("failed to rebuild previous sing-box instance: %w", err)
	}

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return fmt.Errorf("failed to restart previous sing-box instance: %w", err)
	}

	statsClient, statsErr := connectStatsClient(e.statsListenAddr)
	if statsErr != nil {
		fmt.Printf("warning: failed to reconnect to sing-box stats API after rollback: %v\n", statsErr)
	}

	e.mu.Lock()
	e.instance = instance
	e.users = cloneUsers(users)
	e.stats = statsClient
	e.currentConfig = nodeConfig
	e.logLevel = logLevel
	e.mu.Unlock()

	return nil
}

func connectStatsClient(listenAddr string) (*StatsClient, error) {
	return NewStatsClient(listenAddr)
}

func cloneUsers(users []panel.User) []panel.User {
	if len(users) == 0 {
		return nil
	}

	cloned := make([]panel.User, len(users))
	copy(cloned, users)
	return cloned
}
