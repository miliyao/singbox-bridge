package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

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

// TrafficReporter collects per-user traffic and pushes it to Xboard.
// Failed payloads are buffered in memory and persisted to disk for recovery.
type TrafficReporter struct {
	collector trafficCollector
	pusher    trafficPusher
	logger    *zap.Logger

	reportMu        sync.Mutex
	pending         map[int]panel.TrafficData
	stateFile       string
	maxPendingUsers int

	lastReportAt      time.Time
	lastReportOK      bool
	lastReportError   string
	lastPushedUsers   int
	lastBufferedUsers int
}

func NewTrafficReporter(collector trafficCollector, pusher trafficPusher, logger *zap.Logger, stateFile string) *TrafficReporter {
	return NewTrafficReporterWithLimit(collector, pusher, logger, stateFile, 10000)
}

func NewTrafficReporterWithLimit(collector trafficCollector, pusher trafficPusher, logger *zap.Logger, stateFile string, maxPendingUsers int) *TrafficReporter {
	if maxPendingUsers <= 0 {
		maxPendingUsers = 10000
	}

	reporter := &TrafficReporter{
		collector:       collector,
		pusher:          pusher,
		logger:          logger,
		pending:         make(map[int]panel.TrafficData),
		stateFile:       stateFile,
		maxPendingUsers: maxPendingUsers,
	}
	reporter.loadPending()
	return reporter
}

func (t *TrafficReporter) Report(ctx context.Context) {
	t.reportMu.Lock()
	defer t.reportMu.Unlock()

	trafficList, collectErr := t.collector.CollectTraffic(ctx)
	if collectErr != nil {
		t.markReport(false, collectErr.Error(), 0, len(t.pending))
		t.logger.Warn("failed to collect traffic", zap.Error(collectErr), zap.String("state_file", t.stateFile))
	}

	pushData, bufferedUsers := t.buildPushPayload(trafficList)
	if len(pushData) == 0 {
		if collectErr == nil {
			t.markReport(true, "", 0, bufferedUsers)
		}
		return
	}

	if err := t.pusher.PushTraffic(pushData); err != nil {
		t.bufferPending(pushData)
		if persistErr := t.persistPending(); persistErr != nil {
			t.logger.Warn("failed to persist buffered traffic", zap.Error(persistErr), zap.String("state_file", t.stateFile))
		}
		t.logger.Warn("traffic push failed, payload buffered for retry",
			zap.Error(err),
			zap.Int("user_count", len(pushData)),
			zap.String("state_file", t.stateFile),
		)
		t.markReport(false, err.Error(), 0, len(t.pending))
		return
	}

	t.pending = make(map[int]panel.TrafficData)
	if err := t.clearPersistedPending(); err != nil {
		t.logger.Warn("failed to clear persisted traffic buffer", zap.Error(err), zap.String("state_file", t.stateFile))
	}

	var totalUp, totalDown int64
	for _, data := range pushData {
		totalUp += data.Upload
		totalDown += data.Download
	}

	t.logger.Info("traffic push succeeded",
		zap.Int("user_count", len(pushData)),
		zap.Int("retried_users", bufferedUsers),
		zap.Int64("upload_bytes", totalUp),
		zap.Int64("download_bytes", totalDown),
	)
	t.markReport(true, "", len(pushData), bufferedUsers)
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
	t.trimPending()
}

func (t *TrafficReporter) loadPending() {
	if t.stateFile == "" {
		return
	}

	data, err := os.ReadFile(t.stateFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.logger.Warn("failed to read persisted traffic buffer", zap.Error(err), zap.String("path", t.stateFile))
		}
		return
	}

	var payload []panel.TrafficData
	if err := json.Unmarshal(data, &payload); err != nil {
		t.logger.Warn("failed to decode persisted traffic buffer", zap.Error(err), zap.String("path", t.stateFile))
		t.backupCorruptStateFile()
		return
	}

	t.bufferPending(payload)
	if len(payload) > 0 {
		t.logger.Info("restored buffered traffic from disk",
			zap.Int("user_count", len(payload)),
			zap.String("path", t.stateFile),
		)
	}
}

func (t *TrafficReporter) persistPending() error {
	if t.stateFile == "" {
		return nil
	}

	payload := make([]panel.TrafficData, 0, len(t.pending))
	for _, item := range t.pending {
		payload = append(payload, item)
	}
	sort.Slice(payload, func(i, j int) bool {
		return payload[i].UID < payload[j].UID
	})

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(t.stateFile), 0o755); err != nil {
		return err
	}

	tmpFile := t.stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmpFile, t.stateFile)
}

func (t *TrafficReporter) clearPersistedPending() error {
	if t.stateFile == "" {
		return nil
	}

	err := os.Remove(t.stateFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (t *TrafficReporter) trimPending() {
	if t.maxPendingUsers <= 0 || len(t.pending) <= t.maxPendingUsers {
		return
	}

	keys := make([]int, 0, len(t.pending))
	for uid := range t.pending {
		keys = append(keys, uid)
	}
	sort.Ints(keys)

	for _, uid := range keys[:len(keys)-t.maxPendingUsers] {
		delete(t.pending, uid)
	}
	t.logger.Warn("traffic buffer exceeded max users, oldest uid entries dropped",
		zap.Int("max_pending_users", t.maxPendingUsers),
		zap.Int("dropped_users", len(keys)-t.maxPendingUsers),
	)
}

func (t *TrafficReporter) backupCorruptStateFile() {
	if t.stateFile == "" {
		return
	}

	backupPath := t.stateFile + ".corrupt"
	if err := os.Rename(t.stateFile, backupPath); err != nil {
		t.logger.Warn("failed to backup corrupt traffic buffer", zap.Error(err), zap.String("path", t.stateFile))
		return
	}
	t.logger.Warn("corrupt traffic buffer moved aside", zap.String("backup_path", backupPath))
}

func (t *TrafficReporter) markReport(ok bool, err string, pushedUsers, bufferedUsers int) {
	t.lastReportAt = time.Now()
	t.lastReportOK = ok
	t.lastReportError = err
	t.lastPushedUsers = pushedUsers
	t.lastBufferedUsers = bufferedUsers
}

func (t *TrafficReporter) Snapshot() TrafficSnapshot {
	t.reportMu.Lock()
	defer t.reportMu.Unlock()

	return TrafficSnapshot{
		PendingUsers:      len(t.pending),
		StateFile:         t.stateFile,
		MaxPendingUsers:   t.maxPendingUsers,
		LastReportAt:      t.lastReportAt,
		LastReportOK:      t.lastReportOK,
		LastReportError:   t.lastReportError,
		LastPushedUsers:   t.lastPushedUsers,
		LastBufferedUsers: t.lastBufferedUsers,
	}
}

type TrafficSnapshot struct {
	PendingUsers      int       `json:"pending_users"`
	StateFile         string    `json:"state_file"`
	MaxPendingUsers   int       `json:"max_pending_users"`
	LastReportAt      time.Time `json:"last_report_at,omitempty"`
	LastReportOK      bool      `json:"last_report_ok"`
	LastReportError   string    `json:"last_report_error,omitempty"`
	LastPushedUsers   int       `json:"last_pushed_users"`
	LastBufferedUsers int       `json:"last_buffered_users"`
}
