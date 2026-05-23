package main

import (
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/juju/ratelimit"
)

// ConnMeta 定义活跃连接的基础属性，供限制器审计
type ConnMeta struct {
	ConnID    string
	User      string
	SourceIP  netip.Addr
	StartedAt time.Time
}

// LimitDecision 定义审计决策结果
type LimitDecision struct {
	Allow  bool
	Reason string
}

const defaultLimiterWindow = time.Minute

type rateLimiter struct {
	lastReset time.Time
	count     int
}

// Limiter 提供全局连接管理器，支持单用户的并发数控制、IP数控制、连接速率 CPS 控制以及总带宽限速
type Limiter struct {
	configMu sync.RWMutex
	stateMu  sync.Mutex

	// 静态属性：由 configMu 保护
	userByName        map[string]User
	speedLimitByName  map[string]int
	speedBucketByName map[string]*ratelimit.Bucket

	// 动态运行时属性：由 stateMu 独占保护
	activeConnByUser  map[string]map[string]ConnMeta
	activeConnByIP    map[string]map[string]ConnMeta
	activeIPsCount    map[string]map[string]int
	recentConnByUser  map[string]*rateLimiter // CPS 按用户
	recentConnByIP    map[string]*rateLimiter // CPS 按源 IP
	window            time.Duration
	maxConnPerUser    int
	maxNewConnPerUser int
	maxConnPerIP      int
	maxNewConnPerIP   int
}

// NewLimiter 基于配置创建本地连接限制器
func NewLimiter(cfg *Config) *Limiter {
	l := &Limiter{
		userByName:        make(map[string]User),
		activeConnByUser:  make(map[string]map[string]ConnMeta),
		activeConnByIP:    make(map[string]map[string]ConnMeta),
		activeIPsCount:    make(map[string]map[string]int),
		recentConnByUser:  make(map[string]*rateLimiter),
		recentConnByIP:    make(map[string]*rateLimiter),
		speedLimitByName:  make(map[string]int),
		speedBucketByName: make(map[string]*ratelimit.Bucket),
		window:            defaultLimiterWindow,
		maxConnPerUser:    cfg.Limits.MaxConnPerUser,
		maxNewConnPerUser: cfg.Limits.MaxNewConnPerUserPerMin,
		maxConnPerIP:      cfg.Limits.MaxConnPerIP,
		maxNewConnPerIP:   cfg.Limits.MaxNewConnPerIPPerMin,
	}
	l.UpdateUsers(cfg.Users)
	return l
}

// UpdateUsers 更新受控用户列表，并在必要时重建限速令牌桶
func (l *Limiter) UpdateUsers(users []User) {
	l.configMu.Lock()
	defer l.configMu.Unlock()
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	now := time.Now()
	// 定期清理 CPS 追踪缓存以释放空闲用户的内存占用
	for ip, rl := range l.recentConnByIP {
		if now.Sub(rl.lastReset) > l.window*2 {
			delete(l.recentConnByIP, ip)
		}
	}
	for name, rl := range l.recentConnByUser {
		if now.Sub(rl.lastReset) > l.window*2 {
			delete(l.recentConnByUser, name)
		}
	}

	nextUsers := make(map[string]User, len(users))
	nextSpeedLimit := make(map[string]int, len(users))
	nextSpeedBuckets := make(map[string]*ratelimit.Bucket, len(users))

	for _, u := range users {
		name := u.Name
		nextUsers[name] = u
		nextSpeedLimit[name] = u.SpeedLimit
		if bucket, ok := l.speedBucketByName[name]; ok && l.speedLimitByName[name] == u.SpeedLimit {
			nextSpeedBuckets[name] = bucket
		}
	}

	l.userByName = nextUsers
	l.speedLimitByName = nextSpeedLimit
	l.speedBucketByName = nextSpeedBuckets
}

// UserSpeedBucket 满足 singbox 流量限制对令牌桶提供者的要求
func (l *Limiter) UserSpeedBucket(userName string) *ratelimit.Bucket {
	name := strings.TrimSpace(userName)

	l.configMu.RLock()
	speedLimit := l.speedLimitByName[name]
	bucket, ok := l.speedBucketByName[name]
	l.configMu.RUnlock()

	if speedLimit <= 0 {
		return nil
	}
	if ok {
		return bucket
	}

	l.configMu.Lock()
	defer l.configMu.Unlock()

	// 双重锁检查，防止并发重复创建
	if bucket, ok = l.speedBucketByName[name]; ok {
		return bucket
	}

	bytesPerSecond := speedLimitBytesPerSecond(speedLimit)
	if bytesPerSecond <= 0 {
		return nil
	}

	bucket = ratelimit.NewBucketWithRate(bytesPerSecond, int64(bytesPerSecond))
	l.speedBucketByName[name] = bucket
	return bucket
}

// Check 对传入的新连接进行规则合规性审计
func (l *Limiter) Check(meta ConnMeta, now time.Time) LimitDecision {
	userName := strings.TrimSpace(meta.User)
	if userName == "" {
		return LimitDecision{Allow: false, Reason: "missing user"}
	}

	l.configMu.RLock()
	user, ok := l.userByName[userName]
	l.configMu.RUnlock()

	if !ok {
		return LimitDecision{Allow: false, Reason: "unknown user"}
	}

	if meta.StartedAt.IsZero() {
		meta.StartedAt = now
	}

	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	// 1. 每用户 CPS 限制
	if l.maxNewConnPerUser > 0 {
		if !l.checkCPS(l.recentConnByUser, userName, l.maxNewConnPerUser, now) {
			return LimitDecision{Allow: false, Reason: "new connections per user limit exceeded"}
		}
	}

	ipKey := ""
	if meta.SourceIP.IsValid() {
		ipKey = meta.SourceIP.String()
		// 2. 每 IP CPS 限制
		if l.maxNewConnPerIP > 0 {
			if !l.checkCPS(l.recentConnByIP, ipKey, l.maxNewConnPerIP, now) {
				return LimitDecision{Allow: false, Reason: "new connections per ip limit exceeded"}
			}
		}
	}

	// 3. 每用户最大并发连接数限制
	if l.maxConnPerUser > 0 && len(l.ensureConnMap(l.activeConnByUser, userName)) >= l.maxConnPerUser {
		return LimitDecision{Allow: false, Reason: "active connections per user limit exceeded"}
	}

	// 4. 每 IP 最大并发连接数限制
	if ipKey != "" && l.maxConnPerIP > 0 && len(l.ensureConnMap(l.activeConnByIP, ipKey)) >= l.maxConnPerIP {
		return LimitDecision{Allow: false, Reason: "active connections per ip limit exceeded"}
	}

	// 5. 设备数（活跃 IP 数量）限制
	if user.DeviceLimit > 0 && ipKey != "" {
		currentIPs := l.currentUserIPs(userName)
		_, ipKnown := currentIPs[ipKey]
		if !ipKnown {
			// 如果是个新的 IP，检查总 IP 数是否超出额定限制
			if len(currentIPs) >= user.DeviceLimit {
				return LimitDecision{Allow: false, Reason: "device limit exceeded"}
			}
		}
	}

	return LimitDecision{Allow: true}
}

// Register 注册新的活跃连接状态
func (l *Limiter) Register(meta ConnMeta) {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	userName := strings.TrimSpace(meta.User)
	if userName == "" {
		return
	}

	l.ensureConnMap(l.activeConnByUser, userName)[meta.ConnID] = meta
	if meta.SourceIP.IsValid() {
		ipKey := meta.SourceIP.String()
		l.ensureConnMap(l.activeConnByIP, ipKey)[meta.ConnID] = meta

		if l.activeIPsCount[userName] == nil {
			l.activeIPsCount[userName] = make(map[string]int)
		}
		l.activeIPsCount[userName][ipKey]++
	}
}

// Unregister 解除活跃连接状态
func (l *Limiter) Unregister(meta ConnMeta) {
	l.stateMu.Lock()
	defer l.stateMu.Unlock()

	userName := strings.TrimSpace(meta.User)
	if userName != "" {
		if conns, ok := l.activeConnByUser[userName]; ok {
			delete(conns, meta.ConnID)
			if len(conns) == 0 {
				delete(l.activeConnByUser, userName)
			}
		}
	}
	if meta.SourceIP.IsValid() {
		ipKey := meta.SourceIP.String()
		if conns, ok := l.activeConnByIP[ipKey]; ok {
			delete(conns, meta.ConnID)
			if len(conns) == 0 {
				delete(l.activeConnByIP, ipKey)
			}
		}

		if userName != "" {
			if counts, ok := l.activeIPsCount[userName]; ok {
				counts[ipKey]--
				if counts[ipKey] <= 0 {
					delete(counts, ipKey)
				}
				if len(counts) == 0 {
					delete(l.activeIPsCount, userName)
				}
			}
		}
	}
}

func (l *Limiter) checkCPS(index map[string]*rateLimiter, key string, limit int, now time.Time) bool {
	rl, ok := index[key]
	if !ok {
		index[key] = &rateLimiter{
			lastReset: now,
			count:     1,
		}
		return true
	}
	if now.Sub(rl.lastReset) >= l.window {
		rl.lastReset = now
		rl.count = 1
		return true
	}
	if rl.count >= limit {
		return false
	}
	rl.count++
	return true
}

func (l *Limiter) ensureConnMap(index map[string]map[string]ConnMeta, key string) map[string]ConnMeta {
	conns := index[key]
	if conns == nil {
		conns = make(map[string]ConnMeta)
		index[key] = conns
	}
	return conns
}

func (l *Limiter) currentUserIPs(userName string) map[string]struct{} {
	counts := l.activeIPsCount[userName]
	if len(counts) == 0 {
		return nil
	}
	ips := make(map[string]struct{}, len(counts))
	for ip := range counts {
		ips[ip] = struct{}{}
	}
	return ips
}

func speedLimitBytesPerSecond(mbps int) float64 {
	if mbps <= 0 {
		return 0
	}
	return float64(mbps) * 1024 * 1024 / 8
}
