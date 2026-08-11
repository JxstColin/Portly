// Command portly-client connects out to a portly-server control-plane and
// forwards proxied connections to local services. It needs no local tunnel
// config — the server pushes the full tunnel set after authentication.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/jxstcolin/portly/internal/config"
	"github.com/jxstcolin/portly/internal/tunnel"
)

func main() {
	var configPath string

	root := &cobra.Command{
		Use:   "portly-client",
		Short: "Portly reverse-tunnel client",
	}
	root.PersistentFlags().StringVar(&configPath, "config", "portly-client.yaml", "path to client config file")

	root.AddCommand(runCmd(&configPath), initCmd(&configPath), enrollCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runCmd(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Connect to the server and service tunnels",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.LoadClientConfig(*configPath)
			if err != nil {
				return err
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
			c := tunnel.NewClient(cfg.ServerAddr, cfg.Token, cfg.CAFingerprint, logger)
			c.OnUninstall = func() { performSelfUninstall(*configPath, logger) }

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			go runSelfUpdateLoop(ctx, cfg.APIBase, logger)

			err = c.Run(ctx)
			if err == context.Canceled {
				return nil
			}
			if errors.Is(err, tunnel.ErrUninstalled) {
				fmt.Println("Uninstalled — exiting.")
				return nil
			}
			return err
		},
	}
}

func initCmd(configPath *string) *cobra.Command {
	var serverAddr, token, caFingerprint string

	c := &cobra.Command{
		Use:   "init",
		Short: "Write a new portly-client.yaml from the values printed by 'portly-server client add'",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := &config.ClientConfig{
				ServerAddr:    serverAddr,
				Token:         token,
				CAFingerprint: caFingerprint,
			}
			if err := config.SaveClientConfig(*configPath, cfg); err != nil {
				return err
			}
			fmt.Printf("Wrote %s\n", *configPath)
			return nil
		},
	}
	c.Flags().StringVar(&serverAddr, "server-addr", "", "server host:port (required)")
	c.Flags().StringVar(&token, "token", "", "client token (required)")
	c.Flags().StringVar(&caFingerprint, "ca-fingerprint", "", "server CA SHA-256 fingerprint (required)")
	c.MarkFlagRequired("server-addr")
	c.MarkFlagRequired("token")
	c.MarkFlagRequired("ca-fingerprint")
	return c
}
