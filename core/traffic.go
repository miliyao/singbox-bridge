package core

import (
	"context"
	"sort"
	"sync"

	"phantom-node/panel"
	"phantom-node/singbox"

	"go.uber.org/zap"
)

type trafficCollector interface {
	CollectTraffic(ctx context.Context) ([]singbox.UserTraffic, error)
}

type trafficPusher interface {
	PushTraffic(data []panel.TrafficData) error
}

// TrafficReporter collects traffic deltas and pushes them to the panel.
// If a push fails, data is buffered in memory and retried on the next report.
type TrafficReporter struct {
	collector trafficCollector
	pusher    trafficPusher
	logger    *zap.Logger

	reportMu sync.Mutex
	pending  map[int]panel.TrafficData
}

func NewTrafficReporter(collector trafficCollector, pusher trafficPusher, logger *zap.Logger) *TrafficReporter {
	return &TrafficReporter{
		collector: collector,
		pusher:    pusher,
		logger:    logger,
		pending:   make(map[int]panel.TrafficData),
	}
}

func (t *TrafficReporter) Report(ctx context.Context) {
	t.reportMu.Lock()
	defer t.reportMu.Unlock()

	trafficList, err := t.collector.CollectTraffic(ctx)
	if err != nil {
		t.logger.Warn("failed to collect traffic", zap.Error(err))
	}

	pushData, bufferedUsers := t.buildPushPayload(trafficList)
	if len(pushData) == 0 {
		return
	}

	if err := t.pusher.PushTraffic(pushData); err != nil {
		t.bufferPending(pushData)
		t.logger.Warn("failed to push traffic; buffered for retry",
			zap.Error(err),
			zap.Int("user_count", len(pushData)),
		)
		return
	}

	t.pending = make(map[int]panel.TrafficData)

	var totalUp, totalDown int64
	for _, data := range pushData {
		totalUp += data.Upload
		totalDown += data.Download
	}

	t.logger.Info("reported traffic",
		zap.Int("user_count", len(pushData)),
		zap.Int("retried_users", bufferedUsers),
		zap.Int64("upload_bytes", totalUp),
		zap.Int64("download_bytes", totalDown),
	)
}

func (t *TrafficReporter) buildPushPayload(trafficList []singbox.UserTraffic) ([]panel.TrafficData, int) {
	merged := make(map[int]panel.TrafficData, len(t.pending)+len(trafficList))
	for uid, data := range t.pending {
		merged[uid] = data
	}

	bufferedUsers := len(t.pending)

	for _, traffic := range trafficList {
		data := merged[traffic.UserID]
		data.UID = traffic.UserID
		data.Upload += traffic.Upload
		data.Download += traffic.Download
		merged[traffic.UserID] = data
	}

	pushData := make([]panel.TrafficData, 0, len(merged))
	for _, data := range merged {
		if data.Upload == 0 && data.Download == 0 {
			continue
		}
		pushData = append(pushData, data)
	}

	sort.Slice(pushData, func(i, j int) bool {
		return pushData[i].UID < pushData[j].UID
	})

	return pushData, bufferedUsers
}

func (t *TrafficReporter) bufferPending(data []panel.TrafficData) {
	t.pending = make(map[int]panel.TrafficData, len(data))
	for _, item := range data {
		t.pending[item.UID] = item
	}
}
