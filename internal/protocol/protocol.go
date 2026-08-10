// Package protocol defines the control-plane messages exchanged between
// portly-server and portly-client, plus the length-prefixed JSON framing
// used both for the initial auth handshake (on the raw TLS conn) and the
// control stream (over yamux, after the session is established).
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const maxFrameSize = 1 << 20 // 1 MiB, generous headroom for tunnel config pushes

// AuthRequest is sent by the client immediately after the TLS handshake,
// before any yamux session exists.
type AuthRequest struct {
	Token         string `json:"token"`
	ClientVersion string `json:"client_version"`
}

// AuthResponse is the server's reply to AuthRequest.
type AuthResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	Name     string `json:"name,omitempty"`
	// Uninstall is set when OK is false because this client's token was
	// revoked (its machine was deleted in the UI) rather than simply being
	// invalid — the client should clean itself up instead of retrying.
	Uninstall bool `json:"uninstall,omitempty"`
}

// Protocol is the tunnel's transport protocol.
type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

// TunnelSpec describes one tunnel as configured by the admin. It is pushed
// from server to client over the control stream whenever the client's
// tunnel set changes.
type TunnelSpec struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	LocalHost  string   `json:"local_host"`
	LocalPort  int      `json:"local_port"`
	PublicPort int      `json:"public_port"`
	Protocol   Protocol `json:"protocol"`
}

// TunnelConfigPush is sent by the server over the dedicated control stream
// whenever the client's tunnel configuration changes (or on initial connect).
// It always carries the client's full current tunnel set.
type TunnelConfigPush struct {
	Tunnels []TunnelSpec `json:"tunnels"`
	// Uninstall tells an already-connected client that its machine was just
	// deleted in the UI — it should clean itself up and disconnect for good.
	Uninstall bool `json:"uninstall,omitempty"`
}

// StreamHeader is written as the first frame on every yamux data stream the
// server opens for a proxied connection, so the client knows which local
// target and protocol to use. For TCP tunnels, the stream carries raw
// proxied bytes in both directions after this frame. For UDP tunnels, the
// stream instead stays open for the tunnel's lifetime and carries a
// sequence of UDPPacket frames (see below), since a single UDP "tunnel" can
// carry many independent public-side senders.
type StreamHeader struct {
	TunnelID string `json:"tunnel_id"`
}

// UDPPacket carries one UDP datagram plus the public-side address it came
// from (server→client) or should be sent back to (client→server), so a
// single yamux stream can multiplex every public sender for a UDP tunnel.
type UDPPacket struct {
	SourceAddr string `json:"src"`
	Data       []byte `json:"data"`
}

// WriteFrame writes a length-prefixed JSON-encoded message to w.
func WriteFrame(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(data) > maxFrameSize {
		return fmt.Errorf("frame too large: %d bytes", len(data))
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("write frame length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write frame body: %w", err)
	}
	return nil
}

// ReadFrame reads a length-prefixed JSON-encoded message from r into v.
func ReadFrame(r io.Reader, v any) error {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return fmt.Errorf("read frame length: %w", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > maxFrameSize {
		return fmt.Errorf("frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return fmt.Errorf("read frame body: %w", err)
	}
	if err := json.Unmarshal(buf, v); err != nil {
		return fmt.Errorf("unmarshal frame: %w", err)
	}
	return nil
}
