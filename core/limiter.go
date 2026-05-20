package core

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/juju/ratelimit"
	"singbox-bridge/panel"
	"singbox-bridge/singbox"
)

const defaultLimiterWindow = time.Minute

type LimiterConfig struct {
	MaxConnPerUser          int
	MaxConnPerIP            int
	MaxNewConnPerUserPerMin int
	MaxNewConnPerIPPerMin   int
}

type Limiter struct {
	mu sync.Mutex

	userByName        map[string]panel.User
	oldUserOnline     map[string]map[string]struct{}
	remoteUserOnline  map[int]map[string]struct{}
	activeConnByUser  map[string]map[string]singbox.ConnMeta
	activeConnByIP    map[string]map[string]singbox.ConnMeta
	recentConnByUser  map[string][]time.Time
	recentConnByIP    map[string][]time.Time
	aliveList         map[int]int
	speedLimitByName  map[string]int
	speedBucketByName map[string]*ratelimit.Bucket
	window            time.Duration
	maxConnPerUser    int
	maxNewConnPerUser int
	maxConnPerIP      int
	maxNewConnPerIP   int
}

func NewLimiter() *Limiter {
	return NewLimiterWithConfig(LimiterConfig{
		MaxConnPerUser:          32,
		MaxConnPerIP:            20,
		MaxNewConnPerUserPerMin: 120,
		MaxNewConnPerIPPerMin:   60,
	})
}

func NewLimiterWithConfig(cfg LimiterConfig) *Limiter {
	if cfg.MaxConnPerUser <= 0 {
		cfg.MaxConnPerUser = 32
	}
	if cfg.MaxConnPerIP <= 0 {
		cfg.MaxConnPerIP = 20
	}
	if cfg.MaxNewConnPerUserPerMin <= 0 {
		cfg.MaxNewConnPerUserPerMin = 120
	}
	if cfg.MaxNewConnPerIPPerMin <= 0 {
		cfg.MaxNewConnPerIPPerMin = 60
	}

	return &Limiter{
		userByName:        make(map[string]panel.User),
		oldUserOnline:     make(map[string]map[string]struct{}),
		remoteUserOnline:  make(map[int]map[string]struct{}),
		activeConnByUser:  make(map[string]map[string]singbox.ConnMeta),
		activeConnByIP:    make(map[string]map[string]singbox.ConnMeta),
		recentConnByUser:  make(map[string][]time.Time),
		recentConnByIP:    make(map[string][]time.Time),
		aliveList:         make(map[int]int),
		speedLimitByName:  make(map[string]int),
		speedBucketByName: make(map[string]*ratelimit.Bucket),
		window:            defaultLimiterWindow,
		maxConnPerUser:    cfg.MaxConnPerUser,
		maxNewConnPerUser: cfg.MaxNewConnPerUserPerMin,
		maxConnPerIP:      cfg.MaxConnPerIP,
		maxNewConnPerIP:   cfg.MaxNewConnPerIPPerMin,
	}
}

func (l *Limiter) UpdateUsers(users []panel.User) {
	l.mu.Lock()
	defer l.mu.Unlock()

	next := make(map[string]panel.User, len(users))
	nextSpeedLimit := make(map[string]int, len(users))
	nextSpeedBuckets := make(map[string]*ratelimit.Bucket, len(users))
	for _, user := range users {
		name := limiterUserName(user.ID)
		next[name] = user
		nextSpeedLimit[name] = user.SpeedLimit
		if bucket, ok := l.speedBucketByName[name]; ok && l.speedLimitByName[name] == user.SpeedLimit {
			nextSpeedBuckets[name] = bucket
		}
	}
	l.userByName = next
	l.speedLimitByName = nextSpeedLimit
	l.speedBucketByName = nextSpeedBuckets
}


func (l *Limiter) UpdateAliveList(alive panel.AliveList) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(alive) == 0 {
		l.aliveList = make(map[int]int)
		l.remoteUserOnline = make(map[int]map[string]struct{})
		return
	}

	next := make(map[int]int, len(alive))
	nextRemote := make(map[int]map[string]struct{}, len(alive))
	for uid, ips := range alive {
		next[uid] = len(ips)
		nextRemote[uid] = stringSet(ips)
	}
	l.aliveList = next
	l.remoteUserOnline = nextRemote
}

func (l *Limiter) UserSpeedBucket(userName string) *ratelimit.Bucket {
	l.mu.Lock()
	defer l.mu.Unlock()

	name := strings.TrimSpace(userName)
	speedLimit := l.speedLimitByName[name]
	if speedLimit <= 0 {
		return nil
	}

	if bucket, ok := l.speedBucketByName[name]; ok {
		return bucket
	}

	bytesPerSecond := speedLimitBytesPerSecond(speedLimit)
	if bytesPerSecond <= 0 {
		return nil
	}

	bucket := ratelimit.NewBucketWithRate(bytesPerSecond, int64(bytesPerSecond))
	l.speedBucketByName[name] = bucket
	return bucket
}

func (l *Limiter) Check(meta singbox.ConnMeta, now time.Time) singbox.LimitDecision {
	l.mu.Lock()
	defer l.mu.Unlock()

	userName := strings.TrimSpace(meta.User)
	if userName == "" {
		return singbox.LimitDecision{Allow: false, Reason: "missing user"}
	}

	user, ok := l.userByName[userName]
	if !ok {
		return singbox.LimitDecision{Allow: false, Reason: "unknown user"}
	}

	if meta.StartedAt.IsZero() {
		meta.StartedAt = now
	}

	l.recentConnByUser[userName] = append(l.trimEvents(l.recentConnByUser[userName], now), meta.StartedAt)
	if len(l.recentConnByUser[userName]) > l.maxNewConnPerUser {
		return singbox.LimitDecision{Allow: false, Reason: "new connections per user limit exceeded"}
	}

	ipKey := ""
	if meta.SourceIP.IsValid() {
		ipKey = meta.SourceIP.String()
		l.recentConnByIP[ipKey] = append(l.trimEvents(l.recentConnByIP[ipKey], now), meta.StartedAt)
		if len(l.recentConnByIP[ipKey]) > l.maxNewConnPerIP {
			return singbox.LimitDecision{Allow: false, Reason: "new connections per ip limit exceeded"}
		}
	}

	if len(l.ensureConnMap(l.activeConnByUser, userName)) >= l.maxConnPerUser {
		return singbox.LimitDecision{Allow: false, Reason: "active connections per user limit exceeded"}
	}
	if ipKey != "" && len(l.ensureConnMap(l.activeConnByIP, ipKey)) >= l.maxConnPerIP {
		return singbox.LimitDecision{Allow: false, Reason: "active connections per ip limit exceeded"}
	}

	if user.DeviceLimit > 0 && ipKey != "" {
		currentIPs := l.currentUserIPs(userName)
		remoteIPs := l.remoteUserOnline[user.ID]
		known := l.containsIP(currentIPs, ipKey) || l.containsIP(l.oldUserOnline[userName], ipKey)
		known = known || l.containsIP(remoteIPs, ipKey)
		if !known {
			current := l.currentDeviceCount(userName, currentIPs)
			if current >= user.DeviceLimit {
				return singbox.LimitDecision{Allow: false, Reason: "device limit exceeded"}
			}
		}
	}

	return singbox.LimitDecision{Allow: true}
}

func (l *Limiter) Register(meta singbox.ConnMeta) {
	l.mu.Lock()
	defer l.mu.Unlock()

	userName := strings.TrimSpace(meta.User)
	if userName == "" {
		return
	}

	l.ensureConnMap(l.activeConnByUser, userName)[meta.ConnID] = meta
	if meta.SourceIP.IsValid() {
		ipKey := meta.SourceIP.String()
		l.ensureConnMap(l.activeConnByIP, ipKey)[meta.ConnID] = meta
	}
}

func (l *Limiter) Unregister(meta singbox.ConnMeta) {
	l.mu.Lock()
	defer l.mu.Unlock()

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
	}
}

func (l *Limiter) BuildAlivePayload() map[int][]string {
	l.mu.Lock()
	defer l.mu.Unlock()

	currentOnline := l.currentOnlineDeviceSets()
	payload := make(map[int][]string, len(currentOnline))
	nextOld := make(map[string]map[string]struct{}, len(currentOnline))
	for userName, ips := range currentOnline {
		user, ok := l.userByName[userName]
		if !ok {
			continue
		}

		ipList := make([]string, 0, len(ips))
		copied := make(map[string]struct{}, len(ips))
		for ip := range ips {
			ipList = append(ipList, ip)
			copied[ip] = struct{}{}
		}
		sort.Strings(ipList)
		payload[user.ID] = ipList
		nextOld[userName] = copied
	}

	l.oldUserOnline = nextOld
	return payload
}

func limiterUserName(userID int) string {
	return "user-" + strconv.Itoa(userID)
}

func (l *Limiter) trimEvents(events []time.Time, now time.Time) []time.Time {
	if len(events) == 0 {
		return nil
	}
	cutoff := now.Add(-l.window)
	firstValid := 0
	for firstValid < len(events) && events[firstValid].Before(cutoff) {
		firstValid++
	}
	if firstValid == 0 {
		return events
	}
	if firstValid >= len(events) {
		return nil
	}
	trimmed := make([]time.Time, len(events)-firstValid)
	copy(trimmed, events[firstValid:])
	return trimmed
}

func (l *Limiter) ensureConnMap(index map[string]map[string]singbox.ConnMeta, key string) map[string]singbox.ConnMeta {
	conns := index[key]
	if conns == nil {
		conns = make(map[string]singbox.ConnMeta)
		index[key] = conns
	}
	return conns
}

func (l *Limiter) currentUserIPs(userName string) map[string]struct{} {
	userConns := l.activeConnByUser[userName]
	if len(userConns) == 0 {
		return nil
	}

	ips := make(map[string]struct{}, len(userConns))
	for _, meta := range userConns {
		if !meta.SourceIP.IsValid() {
			continue
		}
		ips[meta.SourceIP.String()] = struct{}{}
	}
	if len(ips) == 0 {
		return nil
	}
	return ips
}

func (l *Limiter) currentOnlineDeviceSets() map[string]map[string]struct{} {
	current := make(map[string]map[string]struct{}, len(l.activeConnByUser))
	for userName := range l.activeConnByUser {
		ips := l.currentUserIPs(userName)
		if len(ips) == 0 {
			continue
		}
		current[userName] = ips
	}
	return current
}

func (l *Limiter) currentDeviceCount(userName string, currentIPs map[string]struct{}) int {
	count := len(currentIPs)
	if user, ok := l.userByName[userName]; ok {
		if remote := len(l.remoteUserOnline[user.ID]); remote > count {
			count = remote
		}
		if alive := l.aliveList[user.ID]; alive > count {
			count = alive
		}
	}
	if old := l.oldUserOnline[userName]; len(old) > count {
		count = len(old)
	}
	return count
}

func speedLimitBytesPerSecond(mbps int) float64 {
	if mbps <= 0 {
		return 0
	}
	return float64(mbps) * 1024 * 1024 / 8
}

func (l *Limiter) containsIP(ips map[string]struct{}, ip string) bool {
	if ips == nil {
		return false
	}
	_, ok := ips[ip]
	return ok
}


func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}


func (l *Limiter) Snapshot() LimiterSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()

	activeConnections := 0
	for _, conns := range l.activeConnByUser {
		activeConnections += len(conns)
	}

	return LimiterSnapshot{
		Users:             len(l.userByName),
		ActiveUsers:       len(l.activeConnByUser),
		ActiveIPs:         len(l.activeConnByIP),
		ActiveConnections: activeConnections,
		MaxConnPerUser:    l.maxConnPerUser,
		MaxConnPerIP:      l.maxConnPerIP,
		MaxNewConnPerUser: l.maxNewConnPerUser,
		MaxNewConnPerIP:   l.maxNewConnPerIP,
	}
}

type LimiterSnapshot struct {
	Users             int `json:"users"`
	ActiveUsers       int `json:"active_users"`
	ActiveIPs         int `json:"active_ips"`
	ActiveConnections int `json:"active_connections"`
	MaxConnPerUser    int `json:"max_conn_per_user"`
	MaxConnPerIP      int `json:"max_conn_per_ip"`
	MaxNewConnPerUser int `json:"max_new_conn_per_user_per_min"`
	MaxNewConnPerIP   int `json:"max_new_conn_per_ip_per_min"`
}
