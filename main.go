package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"singbox-bridge/config"
	"singbox-bridge/core"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const shutdownTimeout = 30 * time.Second

func main() {
	// 在 1c1g VPS 环境下，若未配置 GOMEMLIMIT 环境变量，则设置 750MiB 软内存上限以积极触发 GC，防止发生 OOM-Killer 强杀
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(750 * 1024 * 1024)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		_ = logger.Sync()
	}()

	// 在程序启动时，执行 Linux 系统调优指标检测 (BBR 拥塞控制及 nofile 描述符限制)
	checkSystemSettings(logger)

	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		doctorCtx, doctorCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer doctorCancel()

		var results []core.DoctorResult
		allOK := true
		for _, id := range cfg.NodeIDs {
			nodeCfg := *cfg
			nodeCfg.NodeID = id
			nodeCfg.TrafficStateFile = specializeTrafficStateFile(cfg.TrafficStateFile, id)

			logger.Info("running doctor for node", zap.Int("node_id", id))
			res := core.RunDoctor(doctorCtx, &nodeCfg, logger)
			if !res.OK {
				allOK = false
			}
			results = append(results, res)
		}

		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		var output interface{}
		if len(results) == 1 {
			output = results[0]
		} else {
			output = results
		}
		if err := encoder.Encode(output); err != nil {
			logger.Error("failed to encode doctor result", zap.Error(err))
			os.Exit(1)
		}
		if !allOK {
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var nodes []*core.Node
	for _, id := range cfg.NodeIDs {
		nodeCfg := *cfg
		nodeCfg.NodeID = id
		nodeCfg.TrafficStateFile = specializeTrafficStateFile(cfg.TrafficStateFile, id)

		node := core.NewNode(&nodeCfg, logger)
		if err := node.Start(ctx); err != nil {
			logger.Error("节点启动失败", zap.Int("node_id", id), zap.Error(err))
			cancel()
			for _, startedNode := range nodes {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
				startedNode.Shutdown(shutdownCtx)
				shutdownCancel()
			}
			os.Exit(1)
		}
		nodes = append(nodes, node)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)

	sig := <-sigCh
	logger.Info("收到退出信号", zap.String("signal", sig.String()))

	cancel()

	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(n *core.Node) {
			defer wg.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer shutdownCancel()
			n.Shutdown(shutdownCtx)
		}(node)
	}
	wg.Wait()

	logger.Info("进程已退出")
}


func specializeTrafficStateFile(path string, nodeID int) string {
	if path == "" {
		return ""
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	return fmt.Sprintf("%s-node%d%s", base, nodeID, ext)
}

func newLogger(level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	switch level {
	case "debug":
		zapLevel = zap.DebugLevel
	case "info":
		zapLevel = zap.InfoLevel
	case "warn":
		zapLevel = zap.WarnLevel
	case "error":
		zapLevel = zap.ErrorLevel
	default:
		zapLevel = zap.InfoLevel
	}

	cfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(zapLevel),
		Development: false,
		Encoding:    "json",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			LineEnding:     zapcore.DefaultLineEnding,
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	return cfg.Build()
}
