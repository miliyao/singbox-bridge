package singbox

import (
	"context"
	"fmt"
	"strings"
	"time"

	v2rayapi "github.com/sagernet/sing-box/experimental/v2rayapi"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// StatsClient 封装 sing-box V2Ray Stats gRPC 客户端
type StatsClient struct {
	conn   *grpc.ClientConn
	client v2rayapi.StatsServiceClient
}

// UserTraffic 表示单用户流量数据
type UserTraffic struct {
	UserID   int   // 面板用户 ID（从 "user-{id}" 解析）
	Upload   int64 // 上行字节数
	Download int64 // 下行字节数
}

// NewStatsClient 创建 Stats gRPC 客户端
// sing-box v1.13 在 init() 中将 gRPC 服务名改为 v2ray.core.app.stats.command.StatsService
// 但生成的客户端代码使用 experimental.v2rayapi.StatsService，因此使用 conn.Invoke 直接指定路径
func NewStatsClient(listenAddr string) (*StatsClient, error) {
	// 给 sing-box gRPC 服务一点启动时间
	time.Sleep(500 * time.Millisecond)

	conn, err := grpc.NewClient(
		listenAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("连接 V2Ray Stats API 失败: %w", err)
	}

	return &StatsClient{
		conn:   conn,
		client: v2rayapi.NewStatsServiceClient(conn),
	}, nil
}

// QueryUserTraffic 查询所有用户的流量增量并重置计数器
func (s *StatsClient) QueryUserTraffic(ctx context.Context) ([]UserTraffic, error) {
	resp, err := s.queryStats(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("查询流量统计失败: %w", err)
	}

	// 聚合结果
	trafficMap := make(map[int]*UserTraffic)

	for _, stat := range resp.GetStat() {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) < 4 {
			continue
		}
		userName := parts[1]
		direction := parts[3]

		userID, err := parseUserID(userName)
		if err != nil {
			continue
		}

		if _, ok := trafficMap[userID]; !ok {
			trafficMap[userID] = &UserTraffic{UserID: userID}
		}

		switch direction {
		case "uplink":
			trafficMap[userID].Upload = stat.Value
		case "downlink":
			trafficMap[userID].Download = stat.Value
		}
	}

	result := make([]UserTraffic, 0, len(trafficMap))
	for _, t := range trafficMap {
		if t.Upload > 0 || t.Download > 0 {
			result = append(result, *t)
		}
	}

	return result, nil
}

// GetOnlineCount 通过查询有活跃流量的用户数来估算在线人数
func (s *StatsClient) GetOnlineCount(ctx context.Context) (int, error) {
	resp, err := s.queryStats(ctx, false)
	if err != nil {
		return 0, err
	}

	activeUsers := make(map[string]bool)
	for _, stat := range resp.GetStat() {
		parts := strings.Split(stat.Name, ">>>")
		if len(parts) >= 2 && stat.Value > 0 {
			activeUsers[parts[1]] = true
		}
	}

	return len(activeUsers), nil
}

// queryStats 使用正确的 gRPC 服务路径调用 QueryStats
// 关键：sing-box init() 将服务端注册名改为 v2ray.core.app.stats.command.StatsService
// 所以客户端必须用相同路径
func (s *StatsClient) queryStats(ctx context.Context, reset bool) (*v2rayapi.QueryStatsResponse, error) {
	req := &v2rayapi.QueryStatsRequest{
		Reset_: reset,
	}
	resp := new(v2rayapi.QueryStatsResponse)

	// 使用 conn.Invoke 直接指定正确的服务路径
	err := s.conn.Invoke(
		ctx,
		"/v2ray.core.app.stats.command.StatsService/QueryStats",
		req,
		resp,
	)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// Close 关闭 gRPC 连接
func (s *StatsClient) Close() error {
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

// parseUserID 从 "user-123" 格式中提取用户 ID
func parseUserID(name string) (int, error) {
	if !strings.HasPrefix(name, "user-") {
		return 0, fmt.Errorf("无效的用户名格式: %s", name)
	}
	idStr := name[5:]
	id := 0
	for _, c := range idStr {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("非数字字符: %s", name)
		}
		id = id*10 + int(c-'0')
	}
	return id, nil
}
