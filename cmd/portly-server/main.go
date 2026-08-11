// Command portly-server runs Portly's control-plane / data-plane daemon and
// provides CLI bootstrap commands (client/tunnel management) for use before
// the web UI exists.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/acme/autocert"

	"github.com/jxstcolin/portly/internal/api"
	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/netutil"
	"github.com/jxstcolin/portly/internal/tlsutil"
	"github.com/jxstcolin/portly/internal/tunnel"
)

var (
	dataDir        string
	controlAddr    string
	apiAddr        string
	webAddr        string
	httpsAddr      string
	webUpstream    string
	advertiseHost  []string
	allowedOrigins []string
)

func main() {
	root := &cobra.Command{
		Use:   "portly-server",
		Short: "Portly reverse-tunnel server",
	}
	root.PersistentFlags().StringVar(&dataDir, "data-dir", "/var/lib/portly", "directory for the SQLite DB and TLS certs")
	root.PersistentFlags().StringVar(&controlAddr, "control-addr", ":7000", "address the control-plane (tunnel clients) listens on")
	root.PersistentFlags().StringVar(&apiAddr, "api-addr", ":8080", "address a direct (CORS-enabled) API listener, mainly for local development")
	root.PersistentFlags().StringVar(&webAddr, "web-addr", ":80", "public address serving the API, installer, and (reverse-proxied) web UI on one origin; also handles Let's Encrypt HTTP-01 challenges")
	root.PersistentFlags().StringVar(&httpsAddr, "https-addr", ":443", "public HTTPS address, active once a domain is configured in the web UI")
	root.PersistentFlags().StringVar(&webUpstream, "web-upstream", "http://127.0.0.1:3000", "where to reverse-proxy non-API requests (the Next.js UI process)")
	root.PersistentFlags().StringSliceVar(&advertiseHost, "advertise-host", nil, "hostnames/IPs to embed in the server TLS certificate and use in install links (default: auto-detect this machine's public IP)")
	root.PersistentFlags().StringSliceVar(&allowedOrigins, "allowed-origin", []string{"http://localhost:3000"}, "origins allowed to call --api-addr with credentials (dev only — --web-addr is same-origin and needs none)")

	root.AddCommand(runCmd(), clientCmd(), tunnelCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// resolveAdvertiseHosts returns --advertise-host as given, or auto-detects
// this machine's public IP if the flag was left unset — so a fresh install
// works without the operator having to look up and type in their VPS's
// address by hand. localhost/127.0.0.1 are always added to the TLS
// certificate's SAN list on top of whatever's returned here, so local
// testing keeps working regardless.
func resolveAdvertiseHosts(logger *slog.Logger) []string {
	if len(advertiseHost) > 0 {
		return advertiseHost
	}
	ip, err := netutil.DetectPublicIP()
	if err != nil {
		logger.Warn("could not auto-detect public IP, falling back to localhost — pass --advertise-host to set it explicitly", "err", err)
		return []string{"localhost"}
	}
	logger.Info("auto-detected public IP", "ip", ip)
	return []string{ip}
}

func certHostsFor(hosts []string) []string {
	out := append([]string{}, hosts...)
	for _, h := range []string{"localhost", "127.0.0.1"} {
		if !containsString(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func openDB() (*db.DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	return db.Open(filepath.Join(dataDir, "portly.db"))
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Start the Portly server (control-plane + tunnel listeners)",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()

			setupCodePath := filepath.Join(dataDir, "setup-code.txt")
			if code, hasAdmin, err := database.EnsureSetupCode(); err != nil {
				logger.Warn("could not prepare setup code", "err", err)
			} else if !hasAdmin {
				if err := os.WriteFile(setupCodePath, []byte(code+"\n"), 0o600); err != nil {
					logger.Warn("could not write setup code file", "path", setupCodePath, "err", err)
				}
				logger.Warn("no admin account yet — open the web UI and enter this setup code to create one", "setup_code", code)
			}

			hosts := resolveAdvertiseHosts(logger)

			cert, fingerprint, err := tlsutil.EnsureServerCert(dataDir, certHostsFor(hosts))
			if err != nil {
				return fmt.Errorf("ensure server cert: %w", err)
			}
			logger.Info("server certificate ready", "sha256_fingerprint", fingerprint)

			tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}

			srv := tunnel.NewServer(database, tlsConfig, controlAddr, logger)

			clientBins, err := api.LoadEmbeddedClientBinaries()
			if err != nil {
				logger.Warn("could not load embedded client binaries, 'Add machine' installer will 404", "err", err)
				clientBins = map[string][]byte{}
			} else if len(clientBins) == 0 {
				logger.Warn("no embedded client binaries found — run 'make build-clientbins' before building, or use 'portly-client init' manually")
			}

			apiSrv := api.NewServer(database, srv, logger)
			apiSrv.AdvertiseHost = firstNonEmpty(hosts, "localhost")
			if ip6, err := netutil.DetectPublicIPv6(context.Background()); err == nil {
				apiSrv.AdvertiseHostV6 = ip6
			}
			apiSrv.ControlPort = mustPort(controlAddr)
			apiSrv.APIPort = mustPort(apiAddr)
			apiSrv.CAFingerprint = fingerprint
			apiSrv.AllowedOrigins = allowedOrigins
			apiSrv.ClientBinaries = clientBins
			apiSrv.ClientBinarySHA256 = api.ChecksumClientBinaries(clientBins)
			apiSrv.WebUpstream = webUpstream
			apiSrv.PublicHTTPPort = mustPort(webAddr)
			apiSrv.PublicHTTPSPort = mustPort(httpsAddr)

			certManager := &autocert.Manager{
				Prompt: autocert.AcceptTOS,
				Cache:  autocert.DirCache(filepath.Join(dataDir, "certs")),
				HostPolicy: func(ctx context.Context, host string) error {
					if d := apiSrv.Domain(); d != "" && host == d {
						return nil
					}
					return fmt.Errorf("host %q is not the domain configured in the Portly UI", host)
				},
			}

			fetchCert := func(domain string) {
				logger.Info("requesting Let's Encrypt certificate", "domain", domain)
				_, err := certManager.GetCertificate(&tls.ClientHelloInfo{ServerName: domain})
				if err != nil {
					logger.Error("certificate issuance failed", "domain", domain, "err", err)
					apiSrv.SetCertState("error", err.Error())
					return
				}
				logger.Info("certificate ready", "domain", domain)
				apiSrv.SetCertState("ready", "")
			}
			apiSrv.OnDomainSet = fetchCert
			if d := apiSrv.Domain(); d != "" {
				apiSrv.SetCertState("pending", "")
				go fetchCert(d)
			}

			apiSrv.OnAdminClaimed = func() {
				if err := os.Remove(setupCodePath); err != nil && !os.IsNotExist(err) {
					logger.Warn("could not remove setup code file", "path", setupCodePath, "err", err)
				}
			}

			go func() {
				if err := apiSrv.Run(apiAddr); err != nil {
					logger.Error("api server stopped", "err", err)
				}
			}()

			go func() {
				logger.Info("public web listener (API + UI + ACME challenges)", "addr", webAddr)
				handler := certManager.HTTPHandler(apiSrv.Router())
				if err := http.ListenAndServe(webAddr, handler); err != nil {
					logger.Error("public web listener stopped", "err", err)
				}
			}()

			go func() {
				httpsSrv := &http.Server{
					Addr:      httpsAddr,
					Handler:   apiSrv.Router(),
					TLSConfig: certManager.TLSConfig(),
				}
				logger.Info("public HTTPS listener starting (serves once a domain is configured)", "addr", httpsAddr)
				if err := httpsSrv.ListenAndServeTLS("", ""); err != nil {
					logger.Error("public HTTPS listener stopped", "err", err)
				}
			}()

			return srv.Run()
		},
	}
}

func clientCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "client", Short: "Manage tunnel clients"}
	cmd.AddCommand(clientAddCmd(), clientListCmd(), clientRmCmd())
	return cmd
}

func clientAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add [name]",
		Short: "Register a new client and print its connection token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()

			hosts := resolveAdvertiseHosts(logger)
			_, fingerprint, err := tlsutil.EnsureServerCert(dataDir, certHostsFor(hosts))
			if err != nil {
				return err
			}

			client, token, err := database.CreateClient(args[0])
			if err != nil {
				return err
			}

			fmt.Printf("Client %q created (id=%s)\n\n", client.Name, client.ID)
			fmt.Println("portly-client.yaml:")
			fmt.Printf("  server_addr: \"%s\"\n", firstNonEmpty(hosts, "YOUR_VPS_HOST")+controlAddrPortSuffix())
			fmt.Printf("  token: \"%s\"\n", token)
			fmt.Printf("  ca_fingerprint: \"%s\"\n", fingerprint)
			fmt.Println("\n(Token is shown once — store it now.)")
			return nil
		},
	}
}

func clientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered clients",
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()

			clients, err := database.ListClients()
			if err != nil {
				return err
			}
			for _, c := range clients {
				lastSeen := "never"
				if c.LastSeen != nil {
					lastSeen = c.LastSeen.Format("2006-01-02 15:04:05")
				}
				fmt.Printf("%s\t%s\tlast_seen=%s\n", c.ID, c.Name, lastSeen)
			}
			return nil
		},
	}
}

func clientRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm [id]",
		Short: "Delete a client (and its tunnels)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()
			return database.DeleteClient(args[0])
		},
	}
}

func tunnelCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tunnel", Short: "Manage tunnels"}
	cmd.AddCommand(tunnelAddCmd(), tunnelListCmd(), tunnelRmCmd())
	return cmd
}

func tunnelAddCmd() *cobra.Command {
	var clientRef, name, localHost, proto string
	var localPort, publicPort int

	c := &cobra.Command{
		Use:   "add",
		Short: "Create a tunnel: local_host:local_port on the client -> public_port on the VPS",
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()

			client, err := resolveClient(database, clientRef)
			if err != nil {
				return err
			}

			if name == "" {
				name = fmt.Sprintf("%s:%d->%d", localHost, localPort, publicPort)
			}

			if proto != "tcp" && proto != "udp" {
				return fmt.Errorf("--protocol must be 'tcp' or 'udp', got %q", proto)
			}

			t, err := database.CreateTunnel(client.ID, name, localHost, localPort, publicPort, proto)
			if err != nil {
				return err
			}
			fmt.Printf("Tunnel %q created (id=%s): %s/%s:%d -> public port %d (client %s)\n",
				t.Name, t.ID, t.Protocol, t.LocalHost, t.LocalPort, t.PublicPort, client.Name)
			return nil
		},
	}
	c.Flags().StringVar(&clientRef, "client", "", "client ID or name (required)")
	c.Flags().StringVar(&name, "name", "", "tunnel name (default: auto-generated)")
	c.Flags().StringVar(&localHost, "local-host", "127.0.0.1", "host to dial on the client machine")
	c.Flags().IntVar(&localPort, "local-port", 0, "port to dial on the client machine (required)")
	c.Flags().IntVar(&publicPort, "public-port", 0, "public port to open on the VPS (required)")
	c.Flags().StringVar(&proto, "protocol", "tcp", "tunnel protocol: tcp or udp")
	c.MarkFlagRequired("client")
	c.MarkFlagRequired("local-port")
	c.MarkFlagRequired("public-port")
	return c
}

func tunnelListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List tunnels",
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()

			tunnels, err := database.ListAllTunnels()
			if err != nil {
				return err
			}
			for _, t := range tunnels {
				status := "enabled"
				if !t.Enabled {
					status = "disabled"
				}
				fmt.Printf("%s\t%s\t%s/%s:%d -> :%d\tclient=%s\t%s\n",
					t.ID, t.Name, t.Protocol, t.LocalHost, t.LocalPort, t.PublicPort, t.ClientID, status)
			}
			return nil
		},
	}
}

func tunnelRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm [id]",
		Short: "Delete a tunnel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			database, err := openDB()
			if err != nil {
				return err
			}
			defer database.Close()
			return database.DeleteTunnel(args[0])
		},
	}
}

func resolveClient(database *db.DB, ref string) (db.Client, error) {
	if c, err := database.GetClientByID(ref); err == nil {
		return c, nil
	}
	clients, err := database.ListClients()
	if err != nil {
		return db.Client{}, err
	}
	for _, c := range clients {
		if c.Name == ref {
			return c, nil
		}
	}
	return db.Client{}, fmt.Errorf("no client found matching %q", ref)
}

func firstNonEmpty(hosts []string, fallback string) string {
	for _, h := range hosts {
		if h != "" && h != "localhost" && h != "127.0.0.1" {
			return h
		}
	}
	if len(hosts) > 0 {
		return hosts[0]
	}
	return fallback
}

func controlAddrPortSuffix() string {
	_, port, err := splitHostPort(controlAddr)
	if err != nil {
		return controlAddr
	}
	return ":" + port
}

func mustPort(addr string) int {
	_, portStr, err := splitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

func splitHostPort(addr string) (host, port string, err error) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("no port in address %q", addr)
}
