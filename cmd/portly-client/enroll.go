package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jxstcolin/portly/internal/config"
)

const (
	systemConfigPath = "/etc/portly/portly-client.yaml"
	systemUnitPath   = "/etc/systemd/system/portly-client.service"
	systemBinaryPath = "/usr/local/bin/portly-client"
)

func enrollCmd() *cobra.Command {
	var apiBase, code string

	c := &cobra.Command{
		Use:   "enroll",
		Short: "Exchange a one-time enrollment code (from the web UI's 'Add machine') for real credentials, then configure and start the client",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEnroll(apiBase, code)
		},
	}
	c.Flags().StringVar(&apiBase, "api", "", "Portly API base URL, e.g. http://vps-host:8080 (required)")
	c.Flags().StringVar(&code, "code", "", "one-time enrollment code (required)")
	c.MarkFlagRequired("api")
	c.MarkFlagRequired("code")
	return c
}

type exchangeResponse struct {
	Name          string `json:"name"`
	Token         string `json:"token"`
	ControlAddr   string `json:"control_addr"`
	CAFingerprint string `json:"ca_fingerprint"`
	Error         string `json:"error"`
}

func runEnroll(apiBase, code string) error {
	resp, err := exchangeEnrollCode(apiBase, code)
	if err != nil {
		return err
	}
	fmt.Printf("Enrolled as %q\n", resp.Name)

	cfg := &config.ClientConfig{
		ServerAddr:    resp.ControlAddr,
		Token:         resp.Token,
		CAFingerprint: resp.CAFingerprint,
	}

	isRoot := os.Geteuid() == 0
	configPath := "portly-client.yaml"
	if isRoot {
		configPath = systemConfigPath
		if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
			return fmt.Errorf("create config dir: %w", err)
		}
	}
	if err := config.SaveClientConfig(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Printf("Wrote %s\n", configPath)

	if !isRoot {
		fmt.Println("Not running as root — start the client manually:")
		fmt.Printf("  portly-client --config %s run\n", configPath)
		return nil
	}

	if hasSystemd() {
		return installSystemdService(configPath)
	}

	fmt.Println("systemd not detected — starting portly-client as a background process instead.")
	fmt.Println("(It won't survive a reboot; install systemd or start it yourself for persistence.)")
	return startDetached(configPath)
}

func exchangeEnrollCode(apiBase, code string) (*exchangeResponse, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	httpResp, err := http.Post(apiBase+"/api/enroll/exchange", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", apiBase, err)
	}
	defer httpResp.Body.Close()

	var resp exchangeResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		if resp.Error != "" {
			return nil, fmt.Errorf("enrollment failed: %s", resp.Error)
		}
		return nil, fmt.Errorf("enrollment failed: HTTP %d", httpResp.StatusCode)
	}
	return &resp, nil
}

func hasSystemd() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func installSystemdService(configPath string) error {
	unit := fmt.Sprintf(`[Unit]
Description=Portly reverse-tunnel client
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s --config %s run
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`, systemBinaryPath, configPath)

	if err := os.WriteFile(systemUnitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", "--now", "portly-client"},
	} {
		cmd := exec.Command("systemctl", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemctl %v: %w", args, err)
		}
	}

	fmt.Println("Installed and started the portly-client systemd service.")
	fmt.Println("Check status with: systemctl status portly-client")
	return nil
}

// startDetached launches the client as a background process when systemd
// isn't available (containers, non-systemd distros). Best-effort only.
func startDetached(configPath string) error {
	exePath, err := os.Executable()
	if err != nil {
		exePath = systemBinaryPath
	}

	cmd := exec.Command(exePath, "--config", configPath, "run")
	logFile, err := os.OpenFile("/var/log/portly-client.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		cmd.Stdout, cmd.Stderr = logFile, logFile
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start portly-client: %w", err)
	}

	fmt.Printf("Started portly-client in the background (pid %d).\n", cmd.Process.Pid)
	// Give it a moment before we exit so a hard failure surfaces here.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("portly-client exited immediately: %w", err)
		}
		return nil
	case <-time.After(1500 * time.Millisecond):
		return nil
	}
}
