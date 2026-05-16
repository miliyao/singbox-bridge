package core

import (
	"context"

	"phantom-node/panel"
	"phantom-node/singbox"

	"go.uber.org/zap"
)

// TrafficReporter 负责定时采集流量并上报到面板
type TrafficReporter struct {
	engine      *singbox.Engine
	panelClient *panel.Client
	logger      *zap.Logger
}

// NewTrafficReporter 创建流量上报器
func NewTrafficReporter(engine *singbox.Engine, panelClient *panel.Client, logger *zap.Logger) *TrafficReporter {
	return &TrafficReporter{
		engine:      engine,
		panelClient: panelClient,
		logger:      logger,
	}
}

// Report 执行一次流量采集并上报
// 策略：读取后清零（gRPC reset=true），上报失败静默丢弃
func (t *TrafficReporter) Report(ctx context.Context) {
	// 采集当前增量流量
	trafficList, err := t.engine.CollectTraffic(ctx)
	if err != nil {
		t.logger.Warn("流量采集失败", zap.Error(err))
		return
	}

	if len(trafficList) == 0 {
		return // 没有流量数据，跳过上报
	}

	// 转换为面板格式
	pushData := make([]panel.TrafficData, 0, len(trafficList))
	for _, traffic := range trafficList {
		pushData = append(pushData, panel.TrafficData{
			UID:      traffic.UserID,
			Upload:   traffic.Upload,
			Download: traffic.Download,
		})
	}

	// 上报到面板，失败则静默丢弃（参考 XrayR 哲学）
	if err := t.panelClient.PushTraffic(pushData); err != nil {
		t.logger.Warn("流量上报失败（已丢弃）",
			zap.Error(err),
			zap.Int("用户数", len(pushData)),
		)
		return
	}

	// 统计上报总量（仅用于日志）
	var totalUp, totalDown int64
	for _, d := range pushData {
		totalUp += d.Upload
		totalDown += d.Download
	}

	t.logger.Info("流量上报成功",
		zap.Int("用户数", len(pushData)),
		zap.Int64("上行_bytes", totalUp),
		zap.Int64("下行_bytes", totalDown),
	)
}
