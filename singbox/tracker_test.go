package singbox

import (
	"net"
	"testing"
	"time"
)

// mockLimiter 用于测试，实现了 ConnectionLimiter 接口
type mockLimiter struct{}

func (m *mockLimiter) Check(meta ConnMeta, now time.Time) LimitDecision {
	return LimitDecision{Allow: true}
}
func (m *mockLimiter) Register(meta ConnMeta)   {}
func (m *mockLimiter) Unregister(meta ConnMeta) {}

func TestTrackedConnTrafficBuffering(t *testing.T) {
	// 创建内存中的双端管道连接
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	stats := NewStatsTracker()
	limiter := &mockLimiter{}
	meta := ConnMeta{User: "user-123", ConnID: "test-conn"}

	conn := &trackedConn{
		Conn:    client,
		limiter: limiter,
		stats:   stats,
		meta:    meta,
	}

	// 1. 测试未达到 1MB 阈值时的本地缓冲
	data := make([]byte, 100)
	go func() {
		_, _ = server.Write(data)
	}()

	buf := make([]byte, 100)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if n != 100 {
		t.Fatalf("期望读取 100 字节，实际读取 %d 字节", n)
	}

	// 因为没有到 1MB，StatsTracker 里的流量此时应该依然是 0
	traffics, _ := stats.CollectTraffic(nil)
	if len(traffics) != 0 {
		t.Fatalf("期望 StatsTracker 中无数据，实际获取到: %v", traffics)
	}

	// 2. 测试关闭连接时强制清算流量
	err = conn.Close()
	if err != nil {
		t.Fatalf("关闭连接失败: %v", err)
	}

	// 关闭后，残留的 100 字节应当被清算并上报给 StatsTracker
	traffics, _ = stats.CollectTraffic(nil)
	if len(traffics) != 1 {
		t.Fatalf("期望 StatsTracker 中有 1 条记录，实际获取到 %d 条", len(traffics))
	}
	if traffics[0].UserID != 123 {
		t.Errorf("期望 UserID 为 123，实际为 %d", traffics[0].UserID)
	}
	if traffics[0].Upload != 100 {
		t.Errorf("期望 Upload 流量为 100，实际为 %d", traffics[0].Upload)
	}
}
