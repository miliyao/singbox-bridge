package core

import (
	"singbox-bridge/panel"

	"go.uber.org/zap"
)

type aliveProvider interface {
	GetUserAlive() (panel.AliveList, error)
}

type aliveUpdater interface {
	UpdateAliveList(panel.AliveList)
}

func refreshAliveCounts(client aliveProvider, updater aliveUpdater, logger *zap.Logger, phase string) {
	if client == nil || updater == nil {
		return
	}

	alive, err := client.GetUserAlive()
	if err != nil {
		if logger != nil {
			logger.Warn("failed to sync xboard alive list",
				zap.String("phase", phase),
				zap.Error(err),
			)
		}
		return
	}

	updater.UpdateAliveList(alive)
}
