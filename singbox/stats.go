package singbox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	v2rayapi "github.com/sagernet/sing-box/experimental/v2rayapi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	statsConnectDelay = 500 * time.Millisecond
	statsQueryRPCPath = "/v2ray.core.app.stats.command.StatsService/QueryStats"
	userNamePrefix    = "user-"
)

// StatsClient wraps the sing-box V2Ray stats gRPC endpoint.
type StatsClient struct {
	conn *grpc.ClientConn
}

type UserTraffic struct {
	UserID   int
	Upload   int64
	Download int64
}

func NewStatsClient(listenAddr string) (*StatsClient, error) {
	time.Sleep(statsConnectDelay)

	conn, err := grpc.NewClient(
		listenAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect V2Ray stats API: %w", err)
	}

	return &StatsClient{conn: conn}, nil
}

func (s *StatsClient) QueryUserTraffic(ctx context.Context) ([]UserTraffic, error) {
	resp, err := s.queryStats(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("failed to query traffic stats: %w", err)
	}

	trafficMap := make(map[int]*UserTraffic)
	for _, stat := range resp.GetStat() {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) < 4 {
			continue
		}

		userID, err := parseUserID(parts[1])
		if err != nil {
			continue
		}

		entry := trafficMap[userID]
		if entry == nil {
			entry = &UserTraffic{UserID: userID}
			trafficMap[userID] = entry
		}

		switch parts[3] {
		case "uplink":
			entry.Upload = stat.Value
		case "downlink":
			entry.Download = stat.Value
		}
	}

	result := make([]UserTraffic, 0, len(trafficMap))
	for _, traffic := range trafficMap {
		if traffic.Upload == 0 && traffic.Download == 0 {
			continue
		}
		result = append(result, *traffic)
	}

	return result, nil
}

func (s *StatsClient) GetOnlineCount(ctx context.Context) (int, error) {
	resp, err := s.queryStats(ctx, false)
	if err != nil {
		return 0, err
	}

	activeUsers := make(map[string]struct{})
	for _, stat := range resp.GetStat() {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) >= 2 && stat.Value > 0 {
			activeUsers[parts[1]] = struct{}{}
		}
	}

	return len(activeUsers), nil
}

func (s *StatsClient) queryStats(ctx context.Context, reset bool) (*v2rayapi.QueryStatsResponse, error) {
	req := &v2rayapi.QueryStatsRequest{
		Reset_: reset,
	}
	resp := new(v2rayapi.QueryStatsResponse)

	if err := s.conn.Invoke(ctx, statsQueryRPCPath, req, resp); err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *StatsClient) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func parseUserID(name string) (int, error) {
	if !strings.HasPrefix(name, userNamePrefix) {
		return 0, fmt.Errorf("invalid user name format: %s", name)
	}

	id, err := strconv.Atoi(strings.TrimPrefix(name, userNamePrefix))
	if err != nil {
		return 0, fmt.Errorf("invalid numeric user id in %s: %w", name, err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("user id must be positive: %d", id)
	}

	return id, nil
}
