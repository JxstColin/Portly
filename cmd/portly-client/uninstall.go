package main

import (
	"log/slog"
	"os"
	"os/exec"
)

// performSelfUninstall runs when the server tells this client its machine
// was deleted in the UI. It deliberately never sends itself SIGTERM/SIGKILL
// (via `systemctl stop`/`disable --now`) — this function executes from
// inside the very process systemd would be asked to kill, and racing that
// signal against the cleanup steps below could cut them short. Instead it
// disables the unit (so it won't start again), removes the unit file and
// config, removes its own binary (safe to unlink while running on Linux),
// and lets the caller exit the process on its own terms once this returns.
func performSelfUninstall(configPath string, logger *slog.Logger) {
	if hasSystemd() {
		exec.Command("systemctl", "disable", "portly-client").Run()
		if err := os.Remove(systemUnitPath); err != nil && !os.IsNotExist(err) {
			logger.Warn("remove systemd unit failed", "err", err)
		}
		exec.Command("systemctl", "daemon-reload").Run()
	}

	if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
		logger.Warn("remove config failed", "err", err)
	}

	if exe, err := os.Executable(); err == nil {
		if err := os.Remove(exe); err != nil && !os.IsNotExist(err) {
			logger.Warn("remove binary failed", "err", err)
		}
	}

	logger.Info("uninstalled")
}
