package singbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// UserTraffic stores the upload/download metrics.
type UserTraffic struct {
	UserID   int
	Upload   int64
	Download int64
}

type userTrafficRecord struct {
	upload   int64
	download int64
}

// StatsTracker implements the traffic collection and online count tracking in memory.
type StatsTracker struct {
	mu            sync.Mutex
	userTraffic   map[string]*userTrafficRecord
	activeUsers   map[string]map[string]struct{} // userName -> map[connID]struct{}
}

func NewStatsTracker() *StatsTracker {
	return &StatsTracker{
		userTraffic: make(map[string]*userTrafficRecord),
		activeUsers: make(map[string]map[string]struct{}),
	}
}

func (s *StatsTracker) AddTraffic(userName string, upload, download int64) {
	if userName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.userTraffic[userName]
	if !ok {
		r = &userTrafficRecord{}
		s.userTraffic[userName] = r
	}
	r.upload += upload
	r.download += download
}

func (s *StatsTracker) RegisterConn(userName string, connID string) {
	if userName == "" || connID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	conns, ok := s.activeUsers[userName]
	if ok {
		delete(conns, connID)
		if len(conns) == 0 {
			delete(s.activeUsers, userName)
		}
	}
}

func (s *StatsTracker) CollectTraffic(ctx context.Context) ([]UserTraffic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]UserTraffic, 0, len(s.userTraffic))
	for name, record := range s.userTraffic {
		userID, err := parseUserID(name)
		if err != nil {
			continue
		}
		if record.upload == 0 && record.download == 0 {
			continue
		}
		result = append(result, UserTraffic{
			UserID:   userID,
			Upload:   record.upload,
			Download: record.download,
		})
		// Reset accumulated traffic metrics after collection
		record.upload = 0
		record.download = 0
	}
	return result, nil
}

func (s *StatsTracker) GetOnlineCount(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
