package singbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// UserTraffic stores the upload/download metrics.
type UserTraffic struct {
	UserID   int
	Upload   int64
	Download int64
}

type userTrafficRecord struct {
	upload   int64 // accessed atomically
	download int64 // accessed atomically
}

// StatsTracker implements the traffic collection and online count tracking in memory.
type StatsTracker struct {
	userTraffic   sync.Map // string -> *userTrafficRecord
	activeUsersMu sync.Mutex
	activeUsers   map[string]map[string]struct{} // userName -> map[connID]struct{}
}

func NewStatsTracker() *StatsTracker {
	return &StatsTracker{
		activeUsers: make(map[string]map[string]struct{}),
	}
}

func (s *StatsTracker) AddTraffic(userName string, upload, download int64) {
	if userName == "" {
		return
	}

	var r *userTrafficRecord
	if val, ok := s.userTraffic.Load(userName); ok {
		r = val.(*userTrafficRecord)
	} else {
		newRecord := &userTrafficRecord{}
		val, loaded := s.userTraffic.LoadOrStore(userName, newRecord)
		if loaded {
			r = val.(*userTrafficRecord)
		} else {
			r = newRecord
		}
	}

	if upload > 0 {
		atomic.AddInt64(&r.upload, upload)
	}
	if download > 0 {
		atomic.AddInt64(&r.download, download)
	}
}

func (s *StatsTracker) RegisterConn(userName string, connID string) {
	if userName == "" || connID == "" {
		return
	}
	s.activeUsersMu.Lock()
	defer s.activeUsersMu.Unlock()
	conns, ok := s.activeUsers[userName]
	if !ok {
		conns = make(map[string]struct{})
		s.activeUsers[userName] = conns
	}
	conns[connID] = struct{}{}
}

func (s *StatsTracker) UnregisterConn(userName string, connID string) {
	if userName == "" || connID == "" {
		return
	}
	s.activeUsersMu.Lock()
	defer s.activeUsersMu.Unlock()
	conns, ok := s.activeUsers[userName]
	if ok {
		delete(conns, connID)
		if len(conns) == 0 {
			delete(s.activeUsers, userName)
		}
	}
}

func (s *StatsTracker) CollectTraffic(ctx context.Context) ([]UserTraffic, error) {
	result := make([]UserTraffic, 0)
	s.userTraffic.Range(func(key, value any) bool {
		name := key.(string)
		record := value.(*userTrafficRecord)

		userID, err := parseUserID(name)
		if err != nil {
			return true
		}

		upload := atomic.SwapInt64(&record.upload, 0)
		download := atomic.SwapInt64(&record.download, 0)

		if upload == 0 && download == 0 {
			return true
		}

		result = append(result, UserTraffic{
			UserID:   userID,
			Upload:   upload,
			Download: download,
		})
		return true
	})
	return result, nil
}

func (s *StatsTracker) GetOnlineCount(ctx context.Context) (int, error) {
	s.activeUsersMu.Lock()
	defer s.activeUsersMu.Unlock()
	return len(s.activeUsers), nil
}

func parseUserID(name string) (int, error) {
	const userNamePrefix = "user-"
	if !strings.HasPrefix(name, userNamePrefix) {
		return 0, fmt.Errorf("用户标识格式不正确: %s", name)
	}
	id, err := strconv.Atoi(strings.TrimPrefix(name, userNamePrefix))
	if err != nil {
		return 0, fmt.Errorf("用户标识中的数字部分无效: %s", name)
	}
	if id <= 0 {
		return 0, fmt.Errorf("用户 ID 必须大于 0: %d", id)
	}
	return id, nil
}
