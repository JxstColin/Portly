package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// The Minecraft hostname router lets any number of tunnels share a single
// public listener (conventionally the default Minecraft port, 25565)
// instead of each needing its own dedicated public_port and its own DNS SRV
// record. It works by peeking the Minecraft handshake packet every client
// sends first (which carries the hostname the player typed in their server
// list), matching it against tunnels.public_hostname, and then handing the
// connection off to the exact same proxyConn machinery a dedicated
// per-tunnel listener uses — the only difference is how the target tunnel
// was found.
//
// Only the modern (1.7+) handshake is understood; the legacy pre-1.7
// ping/connect protocol is not, and such a connection is simply dropped.

const (
	mcHandshakeTimeout = 5 * time.Second
	mcMaxPacketLen     = 2048 // generous headroom for a real handshake packet (address ~253 bytes max)
	mcMaxAddressLen    = 255
)

// RunMinecraftRouter starts the shared hostname-based listener and blocks
// until it fails, Shutdown is called, or the process is terminated — mirrors
// Run's shape for the control-plane listener. addr is typically ":25565".
func (s *Server) RunMinecraftRouter(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen mc router addr: %w", err)
	}
	defer ln.Close()

	s.mcRouterLnMu.Lock()
	s.mcRouterLn = ln
	s.mcRouterLnMu.Unlock()

	s.Log.Info("minecraft hostname router listening", "addr", addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.shutdownCh:
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go s.handleMinecraftConn(conn)
	}
}

func (s *Server) handleMinecraftConn(conn net.Conn) {
	conn.SetReadDeadline(time.Now().Add(mcHandshakeTimeout))

	hostname, replay, err := peekMinecraftHostname(conn)
	if err != nil {
		s.Log.Debug("minecraft router: handshake peek failed, dropping connection", "remote", conn.RemoteAddr(), "err", err)
		conn.Close()
		return
	}
	conn.SetReadDeadline(time.Time{})

	t, err := s.DB.GetTunnelByHostname(hostname)
	if err != nil {
		s.Log.Debug("minecraft router: no tunnel for hostname, dropping connection", "hostname", hostname, "remote", conn.RemoteAddr())
		conn.Close()
		return
	}
	if t.Protocol != "" && t.Protocol != "tcp" {
		s.Log.Warn("minecraft router: matched tunnel isn't a tcp tunnel, dropping connection", "hostname", hostname, "tunnel", t.Name)
		conn.Close()
		return
	}

	s.proxyConn(t, &prefixedConn{Conn: conn, r: replay})
}

// prefixedConn wraps a net.Conn so the bytes already consumed while peeking
// the handshake (r) are replayed before further reads fall through to the
// underlying connection — the downstream target (via proxyConn -> the
// client machine's local service) must see the exact same byte stream a
// direct connection would have produced, handshake included.
type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

// peekMinecraftHostname reads just far enough into conn to parse a modern
// Minecraft handshake packet (see wiki.vg/Protocol#Handshake) and extract
// the "server address" field, then returns a reader that replays every byte
// physically read from conn during parsing (including whatever bufio
// read ahead beyond what parsing logically consumed) followed by conn
// itself — so nothing is lost for the actual proxied connection.
func peekMinecraftHostname(conn net.Conn) (hostname string, replay io.Reader, err error) {
	var raw bytes.Buffer
	br := bufio.NewReader(io.TeeReader(conn, &raw))

	addr, err := readHandshakeAddress(br)
	if err != nil {
		return "", nil, err
	}

	// Forge/FML (and some proxies) append extra NUL-separated metadata
	// after the real hostname, e.g. "play.example.com\x00192.168.1.1\x00...".
	// Only the part before the first NUL is an actual hostname.
	if i := strings.IndexByte(addr, 0); i >= 0 {
		addr = addr[:i]
	}
	addr = strings.ToLower(strings.TrimSuffix(addr, "."))
	if addr == "" {
		return "", nil, errors.New("empty server address in handshake")
	}

	return addr, io.MultiReader(bytes.NewReader(raw.Bytes()), conn), nil
}

// readHandshakeAddress reads a Handshake packet (packet ID 0x00: VarInt
// protocol version, String server address, unsigned short server port,
// VarInt next state) and returns the server address field. The packet
// length prefix, packet ID, protocol version, port and next-state fields
// are all read (to stay positioned correctly / validate shape) but only the
// address is needed for routing.
func readHandshakeAddress(r *bufio.Reader) (string, error) {
	length, err := readVarInt(r)
	if err != nil {
		return "", fmt.Errorf("read packet length: %w", err)
	}
	if length <= 0 || length > mcMaxPacketLen {
		return "", fmt.Errorf("implausible handshake packet length %d", length)
	}

	packetID, err := readVarInt(r)
	if err != nil {
		return "", fmt.Errorf("read packet id: %w", err)
	}
	if packetID != 0x00 {
		return "", fmt.Errorf("unexpected packet id 0x%02x, not a handshake", packetID)
	}

	if _, err := readVarInt(r); err != nil { // protocol version, unused
		return "", fmt.Errorf("read protocol version: %w", err)
	}

	addrLen, err := readVarInt(r)
	if err != nil {
		return "", fmt.Errorf("read address length: %w", err)
	}
	if addrLen < 0 || addrLen > mcMaxAddressLen {
		return "", fmt.Errorf("implausible address length %d", addrLen)
	}
	addrBytes := make([]byte, addrLen)
	if _, err := io.ReadFull(r, addrBytes); err != nil {
		return "", fmt.Errorf("read address: %w", err)
	}

	// server port (uint16) + next state (VarInt) follow but aren't needed.
	return string(addrBytes), nil
}

// readVarInt reads a Minecraft protocol VarInt (LEB128, up to 5 bytes for a
// 32-bit value) from r.
func readVarInt(r io.ByteReader) (int32, error) {
	var value int32
	var position uint
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= int32(b&0x7F) << position
		if b&0x80 == 0 {
			break
		}
		position += 7
		if position >= 32 {
			return 0, errors.New("varint is too big")
		}
	}
	return value, nil
}
