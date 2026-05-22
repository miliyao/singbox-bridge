//go:build linux

package main

import (
	"os"
	"strings"
	"syscall"

	"go.uber.org/zap"
)

func checkSystemSettings(logger *zap.Logger) {
	// 1. 检测 TCP 拥塞控制算法是否为 BBR
	congestionControlPath := "/proc/sys/net/ipv4/tcp_congestion_control"
	if data, err := os.ReadFile(congestionControlPath); err == nil {
		algo := strings.TrimSpace(string(data))
		if algo != "bbr" {
			logger.Warn("系统未启用 TCP BBR 拥塞控制算法，这在跨境网络环境下可能会影响连接速度。建议通过以下命令开启:\n" +
				"  echo \"net.core.default_qdisc=fq\" >> /etc/sysctl.conf\n" +
				"  echo \"net.ipv4.tcp_congestion_control=bbr\" >> /etc/sysctl.conf\n" +
				"  sysctl -p")
		} else {
			logger.Info("系统 TCP BBR 拥塞控制算法检测: 已启用", zap.String("algo", algo))
		}
	}

	// 2. 检测最大打开文件描述符软限制 (RLIMIT_NOFILE)
	var rlimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlimit); err == nil {
		// 建议至少为 32768，防止高并发时抛出 too many open files
		if rlimit.Cur < 32768 {
			logger.Warn("当前文件描述符(nofile)限制过低，可能会在高并发下导致连接失败。建议调大限制:",
				zap.Uint64("current_soft_limit", rlimit.Cur),
				zap.Uint64("current_hard_limit", rlimit.Max),
			)
			logger.Warn("建议在 /etc/security/limits.conf 中追加以下配置，或者在 systemd 服务配置中添加 LimitNOFILE=65535:\n" +
				"  * soft nofile 65535\n" +
				"  * hard nofile 65535")
		} else {
			logger.Info("当前文件描述符限制检测: 正常", zap.Uint64("soft_limit", rlimit.Cur))
		}
	}
}
