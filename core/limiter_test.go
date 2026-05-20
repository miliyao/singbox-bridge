package core

import (
	"net/netip"
	"testing"
	"time"

	"singbox-bridge/panel"
	"singbox-bridge/singbox"
)

func TestLimiterUpdateUsersReplacesUserMap(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{
		{ID: 1, UUID: "uuid-a", DeviceLimit: 2},
		{ID: 2, UUID: "uuid-b", DeviceLimit: 3},
	})

	limiter.UpdateUsers([]panel.User{
		{ID: 3, UUID: "uuid-c", DeviceLimit: 1},
	})

	if len(limiter.userByName) != 1 {
		t.Fatalf("expected 1 user after replacement, got %d", len(limiter.userByName))
	}
	if _, ok := limiter.userByName["user-3"]; !ok {
		t.Fatal("expected user-3 to exist after replacement")
	}
	if _, ok := limiter.userByName["user-1"]; ok {
		t.Fatal("expected stale user-1 to be removed")
	}
}

func TestLimiterCheckRejectsMissingAndUnknownUsers(t *testing.T) {
	limiter := NewLimiter()

	if got := limiter.Check(singbox.ConnMeta{}, time.Now()); got.Allow || got.Reason != "missing user" {
		t.Fatalf("unexpected decision for missing user: %#v", got)
	}

	if got := limiter.Check(singbox.ConnMeta{User: "user-1"}, time.Now()); got.Allow || got.Reason != "unknown user" {
		t.Fatalf("unexpected decision for unknown user: %#v", got)
	}
}

func TestLimiterWithConfigEnforcesCustomConnectionLimit(t *testing.T) {
	limiter := NewLimiterWithConfig(LimiterConfig{
		MaxConnPerUser:          1,
		MaxConnPerIP:            10,
		MaxNewConnPerUserPerMin: 10,
		MaxNewConnPerIPPerMin:   10,
	})
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a"}})
	limiter.Register(singbox.ConnMeta{
		ConnID:   "conn-a",
		User:     "user-1",
		SourceIP: netip.MustParseAddr("1.1.1.1"),
	})

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-b",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("2.2.2.2"),
		StartedAt: now,
	}, now)
	if got.Allow || got.Reason != "active connections per user limit exceeded" {
		t.Fatalf("expected custom connection limit rejection, got %#v", got)
	}
}

func TestLimiterSnapshotIncludesConfiguredLimits(t *testing.T) {
	limiter := NewLimiterWithConfig(LimiterConfig{
		MaxConnPerUser:          9,
		MaxConnPerIP:            8,
		MaxNewConnPerUserPerMin: 7,
		MaxNewConnPerIPPerMin:   6,
	})

	snapshot := limiter.Snapshot()
	if snapshot.MaxConnPerUser != 9 || snapshot.MaxConnPerIP != 8 || snapshot.MaxNewConnPerUser != 7 || snapshot.MaxNewConnPerIP != 6 {
		t.Fatalf("unexpected limiter snapshot: %#v", snapshot)
	}
}

func TestLimiterTracksActiveConnectionsAndOnlineDevices(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", DeviceLimit: 2}})

	metaA := singbox.ConnMeta{
		ConnID:   "conn-a",
		User:     "user-1",
		SourceIP: netip.MustParseAddr("1.1.1.1"),
	}
	metaB := singbox.ConnMeta{
		ConnID:   "conn-b",
		User:     "user-1",
		SourceIP: netip.MustParseAddr("2.2.2.2"),
	}

	limiter.Register(metaA)
	limiter.Register(metaB)

	payload := limiter.BuildAlivePayload()
	wantPayload := map[int][]string{
		1: {"1.1.1.1", "2.2.2.2"},
	}
	if len(payload) != 1 || len(payload[1]) != 2 || payload[1][0] != wantPayload[1][0] || payload[1][1] != wantPayload[1][1] {
		t.Fatalf("unexpected alive payload: got %#v want %#v", payload, wantPayload)
	}
	if got := len(payload[1]); got != 2 {
		t.Fatalf("expected alive payload for user 1 to contain 2 IPs, got %d", got)
	}

	limiter.Unregister(metaA)
	limiter.Unregister(metaB)

	if _, ok := limiter.activeConnByUser["user-1"]; ok {
		t.Fatalf("expected active connections for user-1 to be cleared, got %#v", limiter.activeConnByUser["user-1"])
	}
	if _, ok := limiter.activeConnByIP["1.1.1.1"]; ok {
		t.Fatalf("expected active ip index for 1.1.1.1 to be cleared, got %#v", limiter.activeConnByIP["1.1.1.1"])
	}

	if payload := limiter.BuildAlivePayload(); len(payload) != 0 {
		t.Fatalf("expected no online payload after unregister, got %#v", payload)
	}
}

func TestLimiterCheckEnforcesDeviceLimitFromAliveSnapshot(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", DeviceLimit: 1}})

	existing := singbox.ConnMeta{
		ConnID:   "conn-a",
		User:     "user-1",
		SourceIP: netip.MustParseAddr("1.1.1.1"),
	}
	limiter.Register(existing)
	limiter.BuildAlivePayload()

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-b",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("2.2.2.2"),
		StartedAt: now,
	}, now); got.Allow || got.Reason != "device limit exceeded" {
		t.Fatalf("expected device-limit rejection, got %#v", got)
	}

	if got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-c",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("1.1.1.1"),
		StartedAt: now,
	}, now); !got.Allow {
		t.Fatalf("expected same-ip reconnect to be allowed, got %#v", got)
	}
}

func TestLimiterCheckUsesKnownOldIpBeforeAliveSnapshot(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", DeviceLimit: 1}})

	meta := singbox.ConnMeta{
		ConnID:   "conn-a",
		User:     "user-1",
		SourceIP: netip.MustParseAddr("1.1.1.1"),
	}
	limiter.Register(meta)
	limiter.BuildAlivePayload()
	limiter.Unregister(meta)

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-b",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("1.1.1.1"),
		StartedAt: now,
	}, now); !got.Allow {
		t.Fatalf("expected known old ip to be allowed, got %#v", got)
	}
}

func TestLimiterUpdateAliveListReplacesSnapshot(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateAliveList(panel.AliveList{1: {"r-1-0", "r-1-1"}, 2: {"r-2-0", "r-2-1", "r-2-2"}})
	limiter.UpdateAliveList(panel.AliveList{9: {"r-9-0"}})

	if len(limiter.aliveList) != 1 {
		t.Fatalf("expected alive snapshot to be replaced, got %#v", limiter.aliveList)
	}
	if limiter.aliveList[9] != 1 {
		t.Fatalf("expected alive count for user 9 to be 1, got %#v", limiter.aliveList)
	}
	if _, ok := limiter.aliveList[1]; ok {
		t.Fatalf("expected stale alive entry for user 1 to be removed, got %#v", limiter.aliveList)
	}
}

func TestLimiterBuildAlivePayloadDoesNotOverwriteAliveCounts(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", DeviceLimit: 1}})
	limiter.UpdateAliveList(panel.AliveList{1: {"r-1-0", "r-1-1"}})

	if payload := limiter.BuildAlivePayload(); len(payload) != 0 {
		t.Fatalf("expected empty payload without active connections, got %#v", payload)
	}
	if limiter.aliveList[1] != 2 {
		t.Fatalf("expected alive snapshot to remain unchanged, got %#v", limiter.aliveList)
	}
}

func TestLimiterCheckUsesRemoteAliveIPsForDeviceLimit(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", DeviceLimit: 1}})
	limiter.UpdateAliveList(panel.AliveList{1: {"1.1.1.1"}})

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	if got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-a",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("1.1.1.1"),
		StartedAt: now,
	}, now); !got.Allow {
		t.Fatalf("expected known remote ip to be allowed, got %#v", got)
	}
	if got := limiter.Check(singbox.ConnMeta{
		ConnID:    "conn-b",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("2.2.2.2"),
		StartedAt: now,
	}, now); got.Allow || got.Reason != "device limit exceeded" {
		t.Fatalf("expected unknown remote ip to be rejected, got %#v", got)
	}
}

func TestLimiterUserSpeedBucketReusesBucketForSameUser(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", SpeedLimit: 10}})

	bucketA := limiter.UserSpeedBucket("user-1")
	if bucketA == nil {
		t.Fatal("expected speed bucket for user-1")
	}

	bucketB := limiter.UserSpeedBucket("user-1")
	if bucketA != bucketB {
		t.Fatal("expected speed bucket to be reused for same user")
	}
}

func TestLimiterUserSpeedBucketReturnsNilForZeroSpeedLimit(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a", SpeedLimit: 0}})

	if bucket := limiter.UserSpeedBucket("user-1"); bucket != nil {
		t.Fatalf("expected nil bucket for zero speed limit, got %#v", bucket)
	}
}

func TestLimiterCheckTrimsExpiredConnectionEvents(t *testing.T) {
	limiter := NewLimiter()
	limiter.UpdateUsers([]panel.User{{ID: 1, UUID: "uuid-a"}})

	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	first := singbox.ConnMeta{
		ConnID:    "conn-a",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("1.1.1.1"),
		StartedAt: now,
	}
	second := singbox.ConnMeta{
		ConnID:    "conn-b",
		User:      "user-1",
		SourceIP:  netip.MustParseAddr("1.1.1.1"),
		StartedAt: now.Add(2 * time.Minute),
	}

	if got := limiter.Check(first, now); !got.Allow {
		t.Fatalf("expected first connection to be allowed, got %#v", got)
	}
	if got := limiter.Check(second, now.Add(2*time.Minute)); !got.Allow {
		t.Fatalf("expected second connection to be allowed after window expiry, got %#v", got)
	}

	if got := len(limiter.recentConnByUser["user-1"]); got != 1 {
		t.Fatalf("expected 1 recent user event after trimming, got %d", got)
	}
	if got := len(limiter.recentConnByIP["1.1.1.1"]); got != 1 {
		t.Fatalf("expected 1 recent ip event after trimming, got %d", got)
	}
}
