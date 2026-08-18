package tunnel

import (
	"bytes"
	"io"
	"net"
	"testing"
)

// pipeConn returns a net.Conn that yields data (then EOF once fully read)
// via a real in-memory net.Pipe, so peekMinecraftHostname can be exercised
// against an actual net.Conn without a real socket.
func pipeConn(t *testing.T, data []byte) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	go func() {
		client.Write(data)
		client.Close()
	}()
	t.Cleanup(func() { server.Close() })
	return server
}

// encodeVarInt mirrors the wire format readVarInt decodes, for building
// synthetic handshake packets in tests.
func encodeVarInt(v int32) []byte {
	var out []byte
	uv := uint32(v)
	for {
		b := byte(uv & 0x7F)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if uv == 0 {
			break
		}
	}
	return out
}

// buildHandshake constructs a full length-prefixed Minecraft Handshake
// packet (packet ID 0x00) with the given protocol version, server address,
// port, and next-state.
func buildHandshake(protocolVersion int32, address string, port uint16, nextState int32) []byte {
	var body bytes.Buffer
	body.Write(encodeVarInt(0x00)) // packet ID
	body.Write(encodeVarInt(protocolVersion))
	body.Write(encodeVarInt(int32(len(address))))
	body.WriteString(address)
	body.WriteByte(byte(port >> 8))
	body.WriteByte(byte(port))
	body.Write(encodeVarInt(nextState))

	var pkt bytes.Buffer
	pkt.Write(encodeVarInt(int32(body.Len())))
	pkt.Write(body.Bytes())
	return pkt.Bytes()
}

func TestReadVarInt(t *testing.T) {
	cases := []int32{0, 1, 127, 128, 255, 25565, 2097151, 2147483647}
	for _, v := range cases {
		r := bytes.NewReader(encodeVarInt(v))
		got, err := readVarInt(r)
		if err != nil {
			t.Fatalf("readVarInt(%d): %v", v, err)
		}
		if got != v {
			t.Fatalf("readVarInt round-trip: want %d, got %d", v, got)
		}
	}
}

func TestReadVarIntTooBig(t *testing.T) {
	// 5 bytes, all with the continuation bit set - never terminates within
	// a valid 32-bit value.
	r := bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if _, err := readVarInt(r); err == nil {
		t.Fatal("expected error for oversized varint, got nil")
	}
}

func TestPeekMinecraftHostname(t *testing.T) {
	pkt := buildHandshake(770, "play.example.com", 25565, 2)
	// Simulate the next packet (e.g. Login Start) arriving immediately
	// after, pipelined in the same TCP segment - exercises bufio's
	// read-ahead consuming bytes beyond the handshake itself.
	extra := []byte{0x05, 0x00, 0x01, 0x02, 0x03}
	conn := pipeConn(t, append(append([]byte{}, pkt...), extra...))

	hostname, replay, err := peekMinecraftHostname(conn)
	if err != nil {
		t.Fatalf("peekMinecraftHostname: %v", err)
	}
	if hostname != "play.example.com" {
		t.Fatalf("hostname = %q, want %q", hostname, "play.example.com")
	}

	replayed, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay: %v", err)
	}
	want := append(append([]byte{}, pkt...), extra...)
	if !bytes.Equal(replayed, want) {
		t.Fatalf("replay mismatch:\n got  %x\n want %x", replayed, want)
	}
}

func TestPeekMinecraftHostnameForgeSuffix(t *testing.T) {
	// Forge/FML appends NUL-separated metadata after the real hostname.
	pkt := buildHandshake(770, "play.example.com\x00FML3\x00", 25565, 2)
	conn := pipeConn(t, pkt)

	hostname, _, err := peekMinecraftHostname(conn)
	if err != nil {
		t.Fatalf("peekMinecraftHostname: %v", err)
	}
	if hostname != "play.example.com" {
		t.Fatalf("hostname = %q, want %q (Forge suffix should be stripped)", hostname, "play.example.com")
	}
}

func TestPeekMinecraftHostnameUppercaseNormalized(t *testing.T) {
	pkt := buildHandshake(770, "Play.Example.COM", 25565, 2)
	conn := pipeConn(t, pkt)

	hostname, _, err := peekMinecraftHostname(conn)
	if err != nil {
		t.Fatalf("peekMinecraftHostname: %v", err)
	}
	if hostname != "play.example.com" {
		t.Fatalf("hostname = %q, want lowercase %q", hostname, "play.example.com")
	}
}

func TestPeekMinecraftHostnameGarbageRejected(t *testing.T) {
	conn := pipeConn(t, []byte{0x16, 0x03, 0x01, 0x00, 0xF8}) // looks like a TLS ClientHello, not a handshake
	if _, _, err := peekMinecraftHostname(conn); err == nil {
		t.Fatal("expected error for non-handshake input, got nil")
	}
}

func TestPeekMinecraftHostnameEmptyAddressRejected(t *testing.T) {
	pkt := buildHandshake(770, "", 25565, 1)
	conn := pipeConn(t, pkt)
	if _, _, err := peekMinecraftHostname(conn); err == nil {
		t.Fatal("expected error for empty server address, got nil")
	}
}

func TestPeekMinecraftHostnameOversizedLengthRejected(t *testing.T) {
	var pkt bytes.Buffer
	pkt.Write(encodeVarInt(1 << 20)) // absurd packet length
	conn := pipeConn(t, pkt.Bytes())
	if _, _, err := peekMinecraftHostname(conn); err == nil {
		t.Fatal("expected error for oversized packet length, got nil")
	}
}

func TestPeekMinecraftHostnameTruncatedRejected(t *testing.T) {
	pkt := buildHandshake(770, "play.example.com", 25565, 2)
	conn := pipeConn(t, pkt[:len(pkt)-10]) // cut off mid-address (only trailing port+next-state are unread by the parser)
	if _, _, err := peekMinecraftHostname(conn); err == nil {
		t.Fatal("expected error for truncated handshake, got nil")
	}
}
