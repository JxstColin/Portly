package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/jxstcolin/portly/internal/protocol"
	"github.com/jxstcolin/portly/internal/tlsutil"
)

const (
	dialTimeout = 10 * time.Second
	minBackoff  = 1 * time.Second
	maxBackoff  = 30 * time.Second
)

type Client struct {
	ServerAddr    string
	Token         string
	CAFingerprint string
	Log           *slog.Logger

	mu      sync.RWMutex
	targets map[string]protocol.TunnelSpec // tunnel ID -> current spec
}

func NewClient(serverAddr, token, caFingerprint string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		ServerAddr:    serverAddr,
		Token:         token,
		CAFingerprint: caFingerprint,
		Log:           logger,
		targets:       make(map[string]protocol.TunnelSpec),
	}
}

// Run connects to the server and services tunnels until ctx is cancelled,
// automatically reconnecting with backoff on any failure.
func (c *Client) Run(ctx context.Context) error {
	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.Log.Warn("disconnected, retrying", "err", err, "backoff", backoff)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	conn, err := tlsutil.DialPinned(c.ServerAddr, c.CAFingerprint)
	if err != nil {
		return fmt.Errorf("dial server: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(dialTimeout))
	if err := protocol.WriteFrame(conn, protocol.AuthRequest{Token: c.Token, ClientVersion: "0.1"}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	var resp protocol.AuthResponse
	if err := protocol.ReadFrame(conn, &resp); err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("auth rejected: %s", resp.Error)
	}
	conn.SetDeadline(time.Time{})

	c.Log.Info("connected", "server", c.ServerAddr, "client_id", resp.ClientID, "name", resp.Name)

	session, err := yamux.Client(conn, yamuxConfig())
	if err != nil {
		return fmt.Errorf("yamux client: %w", err)
	}
	defer session.Close()

	controlStream, err := session.Accept()
	if err != nil {
		return fmt.Errorf("accept control stream: %w", err)
	}

	// Reset backoff on a fully successful handshake by returning nil only
	// through ctx cancellation; a live session resets naturally since the
	// caller re-enters runOnce only after this returns.
	errCh := make(chan error, 2)

	go func() {
		errCh <- c.readControlStream(controlStream)
	}()

	go func() {
		errCh <- c.acceptDataStreams(session)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (c *Client) readControlStream(stream net.Conn) error {
	for {
		var push protocol.TunnelConfigPush
		if err := protocol.ReadFrame(stream, &push); err != nil {
			return fmt.Errorf("control stream closed: %w", err)
		}

		next := make(map[string]protocol.TunnelSpec, len(push.Tunnels))
		for _, t := range push.Tunnels {
			next[t.ID] = t
		}

		c.mu.Lock()
		c.targets = next
		c.mu.Unlock()

		c.Log.Info("tunnel config updated", "count", len(next))
	}
}

func (c *Client) acceptDataStreams(session *yamux.Session) error {
	for {
		stream, err := session.Accept()
		if err != nil {
			return fmt.Errorf("session closed: %w", err)
		}
		go c.handleDataStream(stream)
	}
}

func (c *Client) handleDataStream(stream net.Conn) {
	defer stream.Close()

	var hdr protocol.StreamHeader
	if err := protocol.ReadFrame(stream, &hdr); err != nil {
		c.Log.Warn("read stream header failed", "err", err)
		return
	}

	c.mu.RLock()
	spec, ok := c.targets[hdr.TunnelID]
	c.mu.RUnlock()
	if !ok {
		c.Log.Warn("unknown tunnel id in stream header", "tunnel_id", hdr.TunnelID)
		return
	}

	local := net.JoinHostPort(spec.LocalHost, fmt.Sprintf("%d", spec.LocalPort))
	localConn, err := net.DialTimeout("tcp", local, dialTimeout)
	if err != nil {
		c.Log.Warn("dial local target failed", "tunnel", spec.Name, "target", local, "err", err)
		return
	}
	defer localConn.Close()

	pipe(localConn, stream)
}
