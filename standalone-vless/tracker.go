package main

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juju/ratelimit"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"go.uber.org/zap"
)

type limiterTracker struct {
	limiter      *Limiter
	logger       *zap.Logger
	connSequence uint64
}

func newLimiterTracker(limiter *Limiter, logger *zap.Logger) *limiterTracker {
	return &limiterTracker{
		limiter: limiter,
		logger:  logger,
	}
}

// RoutedConnection 处理 TCP 路由拦截，注入限速与连接计数
func (t *limiterTracker) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	meta := t.buildConnMeta(metadata)
	if decision := t.limiter.Check(meta, meta.StartedAt); !decision.Allow {
		if t.logger != nil {
			t.logger.Warn("TCP 连接被拒绝",
				zap.String("user", meta.User),
				zap.String("ip", meta.SourceIP.String()),
				zap.String("reason", decision.Reason),
			)
		}
		_ = conn.Close()
		return conn
	}

	t.limiter.Register(meta)

	// 注入速度限制
	conn = t.wrapSpeedLimit(meta.User, conn)

	return &trackedConn{
		Conn:    conn,
		limiter: t.limiter,
		meta:    meta,
	}
}

// RoutedPacketConnection 处理 UDP 路由拦截
func (t *limiterTracker) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	meta := t.buildConnMeta(metadata)
	if decision := t.limiter.Check(meta, meta.StartedAt); !decision.Allow {
		if t.logger != nil {
			t.logger.Warn("UDP 连接被拒绝",
				zap.String("user", meta.User),
				zap.String("ip", meta.SourceIP.String()),
				zap.String("reason", decision.Reason),
			)
		}
		_ = conn.Close()
		return conn
	}

	t.limiter.Register(meta)

	return &trackedPacketConn{
		PacketConn: conn,
		limiter:    t.limiter,
		meta:       meta,
	}
}

func (t *limiterTracker) wrapSpeedLimit(userName string, conn net.Conn) net.Conn {
	bucket := t.limiter.UserSpeedBucket(userName)
	if bucket == nil {
		return conn
	}

	if t.logger != nil {
		t.logger.Debug("已应用用户限速", zap.String("user", userName))
	}

	return &speedLimitedConn{
		Conn:   conn,
		reader: ratelimit.Reader(conn, bucket),
		writer: ratelimit.Writer(conn, bucket),
	}
}

func (t *limiterTracker) buildConnMeta(metadata adapter.InboundContext) ConnMeta {
	seq := atomic.AddUint64(&t.connSequence, 1)
	meta := ConnMeta{
		ConnID:    strconv.FormatUint(seq, 36),
		User:      metadata.User,
		StartedAt: time.Now(),
	}
	if metadata.Source.IsValid() && metadata.Source.Addr.IsValid() {
		meta.SourceIP = metadata.Source.Addr
	}
	return meta
}

type trackedConn struct {
	net.Conn
	limiter   *Limiter
	meta      ConnMeta
	closeOnce sync.Once
}

func (c *trackedConn) Upstream() any {
	return c.Conn
}

func (c *trackedConn) Unwrap() any {
	return c.Conn
}

func (c *trackedConn) Close() error {
	c.closeOnce.Do(func() {
		c.limiter.Unregister(c.meta)
	})
	return c.Conn.Close()
}

type trackedPacketConn struct {
	N.PacketConn
	limiter   *Limiter
	meta      ConnMeta
	closeOnce sync.Once
}

func (c *trackedPacketConn) Upstream() any {
	return c.PacketConn
}

func (c *trackedPacketConn) Unwrap() any {
	return c.PacketConn
}

func (c *trackedPacketConn) FrontHeadroom() int {
	if f, ok := c.PacketConn.(interface{ FrontHeadroom() int }); ok {
		return f.FrontHeadroom()
	}
	return 0
}

func (c *trackedPacketConn) RearHeadroom() int {
	if f, ok := c.PacketConn.(interface{ RearHeadroom() int }); ok {
		return f.RearHeadroom()
	}
	return 0
}

func (c *trackedPacketConn) ReadPacket(buffer *buf.Buffer) (metadata.Socksaddr, error) {
	return c.PacketConn.ReadPacket(buffer)
}

func (c *trackedPacketConn) WritePacket(buffer *buf.Buffer, destination metadata.Socksaddr) error {
	return c.PacketConn.WritePacket(buffer, destination)
}

func (c *trackedPacketConn) Close() error {
	c.closeOnce.Do(func() {
		c.limiter.Unregister(c.meta)
	})
	return c.PacketConn.Close()
}

type speedLimitedConn struct {
	net.Conn
	reader io.Reader
	writer io.Writer
}

func (c *speedLimitedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *speedLimitedConn) Write(p []byte) (int, error) {
	return c.writer.Write(p)
}

var _ adapter.ConnectionTracker = (*limiterTracker)(nil)
