package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"phantom-node/config"
	"phantom-node/core"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// 1. 加载环境变量配置
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置加载失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化结构化 JSON 日志
	logger, err := newLogger(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "日志初始化失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// 3. 创建全局 Context（优雅关停的信号枢纽）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 4. 创建并启动节点
	node := core.NewNode(cfg, logger)
	if err := node.Start(ctx); err != nil {
		logger.Fatal("节点启动失败", zap.Error(err))
	}

	// 5. 监听系统信号，等待 SIGTERM / SIGINT
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigCh
	logger.Info("收到系统信号，准备关停", zap.String("信号", sig.String()))

	// 6. 通知所有 goroutine 退出
	cancel()

	// 7. 执行优雅关停序列（限时 30 秒）
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*1e9) // 30s
	defer shutdownCancel()

	node.Shutdown(shutdownCtx)

	logger.Info("进程退出")
}

// newLogger 创建纯 JSON 结构化日志（zap）
func newLogger(level string) (*zap.Logger, error) {
	// 解析日志级别
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

	// 构建 JSON 日志配置
	config := zap.Config{
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

	return config.Build()
}
