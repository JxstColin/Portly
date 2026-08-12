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
	"net"
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
	// ProxyProtocol, if set, tells the client to prefix the local connection
	// with a PROXY protocol v1 header carrying the real public client's
	// address (from StreamHeader.RemoteAddr) before relaying bytes — so the
	// local service can see the real player/client IP instead of the
	// client machine's own loopback/LAN address. Only takes effect if the
	// local service itself understands PROXY protocol (e.g. Paper's
	// proxy-protocol setting for Minecraft) — otherwise it'll misinterpret
	// the header as garbage protocol data and the connection will fail, so
	// this defaults to off and is opt-in per tunnel.
	ProxyProtocol bool `json:"proxy_protocol,omitempty"`
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
	// RemoteAddr is the real public client's address (host:port) that
	// connected to the tunnel's public port, for tunnels with ProxyProtocol
	// enabled to relay onward via a PROXY protocol v1 header.
	RemoteAddr string `json:"remote_addr,omitempty"`
}

// UDPPacket carries one UDP datagram plus the public-side address it came
// from (server→client) or should be sent back to (client→server), so a
// single yamux stream can multiplex every public sender for a UDP tunnel.
type UDPPacket struct {
	SourceAddr string `json:"src"`
	Data       []byte `json:"data"`
}

// Device is one host the client discovered on its local network, for the
// "Add tunnel" UI's local-host suggestions.
type Device struct {
	IP       string `json:"ip"`
	MAC      string `json:"mac,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// DeviceReport is sent by the client to the server on its own short-lived
// yamux stream (opened by the client, unlike every other stream in this
// protocol which the server opens) whenever it refreshes its local network
// scan. It always carries the client's full current device list.
type DeviceReport struct {
	Devices []Device `json:"devices"`
}

// ProxyProtocolV1Header builds a HAProxy PROXY protocol v1 header line
// (https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt) reporting
// srcAddr (the real public client) connecting through to dstAddr (the
// tunnel's public address), for a portly-client to write to a local
// service ahead of relaying raw bytes — so the local service sees the real
// client address instead of the tunnel connection's own loopback/LAN
// source. Falls back to "PROXY UNKNOWN\r\n" (valid per the spec) if either
// address can't be parsed, rather than sending nothing at all: once a
// tunnel has this enabled, the local service always expects a PROXY line
// as the very first bytes of every connection.
func ProxyProtocolV1Header(srcAddr, dstAddr string) string {
	srcIP, srcPort, err := splitHostPortIP(srcAddr)
	if err != nil {
		// Without a real source address there's nothing worth reporting.
		return "PROXY UNKNOWN\r\n"
	}
	family := "TCP4"
	zero := "0.0.0.0"
	if srcIP.To4() == nil {
		family = "TCP6"
		zero = "::"
	}

	dstIP, dstPort, err := splitHostPortIP(dstAddr)
	if err != nil {
		// The source is still worth sending even if the destination
		// couldn't be resolved to a literal IP (e.g. --advertise-host set
		// to a hostname) — an all-zero address in the same family is valid
		// per the spec and doesn't throw away what actually matters here.
		dstIP, dstPort = net.ParseIP(zero), "0"
	}
	return fmt.Sprintf("PROXY %s %s %s %s %s\r\n", family, srcIP.String(), dstIP.String(), srcPort, dstPort)
}

func splitHostPortIP(addr string) (net.IP, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, "", err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, "", fmt.Errorf("invalid ip %q", host)
	}
	return ip, port, nil
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
