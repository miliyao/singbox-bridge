package singbox

import (
	"context"
	"io"
	"net"
	"net/netip"
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

type ConnMeta struct {
	ConnID    string
	User      string
	SourceIP  netip.Addr
	StartedAt time.Time
}

type LimitDecision struct {
	Allow  bool
	Reason string
}

type limiterTracker struct {
	limiter      ConnectionLimiter
	stats        *StatsTracker
	rates        UserRateProvider
	logger       *zap.Logger
	connSequence uint64
}

type ConnectionLimiter interface {
	Check(meta ConnMeta, now time.Time) LimitDecision
	Register(meta ConnMeta)
	Unregister(meta ConnMeta)
}

type UserRateProvider interface {
	UserSpeedBucket(userName string) *ratelimit.Bucket
}

func newLimiterTracker(limiter ConnectionLimiter, stats *StatsTracker, rates UserRateProvider, logger *zap.Logger) *limiterTracker {
	return &limiterTracker{limiter: limiter, stats: stats, rates: rates, logger: logger}
}

func (t *limiterTracker) RoutedConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) net.Conn {
	meta := t.buildConnMeta(metadata)
	if decision := t.limiter.Check(meta, meta.StartedAt); !decision.Allow {
		if t.logger != nil {
			t.logger.Warn("connection denied",
				zap.String("user", meta.User),
				zap.String("ip", meta.SourceIP.String()),
				zap.String("reason", decision.Reason),
			)
		}
		_ = conn.Close()
		return conn
	}
	t.limiter.Register(meta)
	if t.stats != nil {
		t.stats.RegisterConn(meta.User, meta.ConnID)
	}
	conn = t.wrapSpeedLimit(meta.User, conn)
	return &trackedConn{
		Conn:    conn,
		limiter: t.limiter,
		stats:   t.stats,
		meta:    meta,
	}
}

func (t *limiterTracker) RoutedPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, matchedRule adapter.Rule, matchOutbound adapter.Outbound) N.PacketConn {
	meta := t.buildConnMeta(metadata)
	if decision := t.limiter.Check(meta, meta.StartedAt); !decision.Allow {
		if t.logger != nil {
			t.logger.Warn("packet connection denied",
				zap.String("user", meta.User),
				zap.String("ip", meta.SourceIP.String()),
				zap.String("reason", decision.Reason),
			)
		}
		_ = conn.Close()
		return conn
	}
	t.limiter.Register(meta)
	if t.stats != nil {
		t.stats.RegisterConn(meta.User, meta.ConnID)
	}
	return &trackedPacketConn{
		PacketConn: conn,
		limiter:    t.limiter,
		stats:      t.stats,
		meta:       meta,
	}
}

func (t *limiterTracker) wrapSpeedLimit(userName string, conn net.Conn) net.Conn {
	if t.rates == nil {
		return conn
	}

	bucket := t.rates.UserSpeedBucket(userName)
	if bucket == nil {
		return conn
	}

	if t.logger != nil {
		t.logger.Debug("speed limit applied", zap.String("user", userName))
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

const trafficBufferThreshold = 1024 * 1024 // 1MB 阈值，限制提交到 StatsTracker 的锁竞争频率

type trackedConn struct {
	net.Conn
	limiter     ConnectionLimiter
	stats       *StatsTracker
	meta        ConnMeta
	closeOnce   sync.Once
	uploadBuf   int64
	downloadBuf int64
}

func (c *trackedConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 && c.stats != nil {
		val := atomic.AddInt64(&c.uploadBuf, int64(n))
		if val >= trafficBufferThreshold {
			toUpload := atomic.SwapInt64(&c.uploadBuf, 0)
			if toUpload > 0 {
				c.stats.AddTraffic(c.meta.User, toUpload, 0)
			}
		}
	}
	return
}

func (c *trackedConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 && c.stats != nil {
		val := atomic.AddInt64(&c.downloadBuf, int64(n))
		if val >= trafficBufferThreshold {
			toDownload := atomic.SwapInt64(&c.downloadBuf, 0)
			if toDownload > 0 {
				c.stats.AddTraffic(c.meta.User, 0, toDownload)
			}
		}
	}
	return
}

func (c *trackedConn) Close() error {
	c.closeOnce.Do(func() {
		c.limiter.Unregister(c.meta)
		if c.stats != nil {
			c.stats.UnregisterConn(c.meta.User, c.meta.ConnID)
			// 清算残留的流量缓冲
			toUpload := atomic.SwapInt64(&c.uploadBuf, 0)
			toDownload := atomic.SwapInt64(&c.downloadBuf, 0)
			if toUpload > 0 || toDownload > 0 {
				c.stats.AddTraffic(c.meta.User, toUpload, toDownload)
			}
		}
	})
	return c.Conn.Close()
}

type trackedPacketConn struct {
	N.PacketConn
	limiter     ConnectionLimiter
	stats       *StatsTracker
	meta        ConnMeta
	closeOnce   sync.Once
	uploadBuf   int64
	downloadBuf int64
}

func (c *trackedPacketConn) ReadPacket(buffer *buf.Buffer) (metadata.Socksaddr, error) {
	addr, err := c.PacketConn.ReadPacket(buffer)
	if err == nil && c.stats != nil {
		val := atomic.AddInt64(&c.uploadBuf, int64(buffer.Len()))
		if val >= trafficBufferThreshold {
			toUpload := atomic.SwapInt64(&c.uploadBuf, 0)
			if toUpload > 0 {
				c.stats.AddTraffic(c.meta.User, toUpload, 0)
			}
		}
	}
	return addr, err
}

func (c *trackedPacketConn) WritePacket(buffer *buf.Buffer, destination metadata.Socksaddr) error {
	err := c.PacketConn.WritePacket(buffer, destination)
	if err == nil && c.stats != nil {
		val := atomic.AddInt64(&c.downloadBuf, int64(buffer.Len()))
		if val >= trafficBufferThreshold {
			toDownload := atomic.SwapInt64(&c.downloadBuf, 0)
			if toDownload > 0 {
				c.stats.AddTraffic(c.meta.User, 0, toDownload)
			}
		}
	}
	return err
}

func (c *trackedPacketConn) Close() error {
	c.closeOnce.Do(func() {
		c.limiter.Unregister(c.meta)
		if c.stats != nil {
			c.stats.UnregisterConn(c.meta.User, c.meta.ConnID)
			// 清算残留的流量缓冲
			toUpload := atomic.SwapInt64(&c.uploadBuf, 0)
			toDownload := atomic.SwapInt64(&c.downloadBuf, 0)
			if toUpload > 0 || toDownload > 0 {
				c.stats.AddTraffic(c.meta.User, toUpload, toDownload)
			}
		}
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
