package tunnel

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/protocol"
)

const (
	udpReadBufSize        = 64 * 1024
	udpSessionIdleTimeout = 60 * time.Second
	udpRedialBackoff      = 2 * time.Second
)

// --- Server side ---

func (s *Server) startUDPListener(t db.Tunnel) (*publicListener, error) {
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{Port: t.PublicPort})
	if err != nil {
		return nil, err
	}

	stop := make(chan struct{})
	go s.serveUDPTunnel(t, udpConn, stop)

	return &publicListener{tunnelID: t.ID, closer: udpConn, cancel: func() { close(stop) }}, nil
}

// serveUDPTunnel owns the public UDP socket for the tunnel's lifetime,
// pairing it with one yamux stream to the owning client at a time. If the
// client is offline or the stream breaks, it redials with a short backoff
// rather than giving up — the public socket itself stays open throughout.
func (s *Server) serveUDPTunnel(t db.Tunnel, udpConn *net.UDPConn, stop <-chan struct{}) {
	s.Log.Info("tunnel listener started", "tunnel", t.Name, "protocol", "udp", "public_port", t.PublicPort, "client_id", t.ClientID)
	lc := s.getLiveCounter(t.ID)

	for {
		select {
		case <-stop:
			return
		default:
		}

		stream, err := s.openClientStream(t)
		if err != nil {
			select {
			case <-stop:
				return
			case <-time.After(udpRedialBackoff):
				continue
			}
		}

		s.runUDPSession(udpConn, stream, lc, stop)
		stream.Close()
	}
}

// openClientStream opens a fresh yamux stream to the tunnel's owning client
// and writes the initial StreamHeader, or errors if it isn't connected.
func (s *Server) openClientStream(t db.Tunnel) (net.Conn, error) {
	s.mu.RLock()
	cs, ok := s.sessions[t.ClientID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client not connected")
	}

	stream, err := cs.session.Open()
	if err != nil {
		return nil, err
	}
	if err := protocol.WriteFrame(stream, protocol.StreamHeader{TunnelID: t.ID}); err != nil {
		stream.Close()
		return nil, err
	}
	return stream, nil
}

// runUDPSession pumps datagrams between the public UDP socket and one yamux
// stream until the stream breaks, demultiplexing by public sender address
// so many simultaneous senders share the single stream. Blocks until done.
func (s *Server) runUDPSession(udpConn *net.UDPConn, stream net.Conn, lc *liveCounter, stop <-chan struct{}) {
	var addrMu sync.Mutex
	addrs := make(map[string]*net.UDPAddr)

	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() { closeOnce.Do(func() { close(done) }) }

	// client -> public: replies from the local service, routed back to
	// whichever public sender they're addressed to.
	go func() {
		defer closeDone()
		for {
			var pkt protocol.UDPPacket
			if err := protocol.ReadFrame(stream, &pkt); err != nil {
				return
			}
			addrMu.Lock()
			addr := addrs[pkt.SourceAddr]
			addrMu.Unlock()
			if addr == nil {
				continue
			}
			n, _ := udpConn.WriteToUDP(pkt.Data, addr)
			atomic.AddInt64(&lc.out, int64(n))
		}
	}()

	// public -> client: datagrams from the outside world, tagged with their
	// sender and forwarded over the stream.
	buf := make([]byte, udpReadBufSize)
	for {
		select {
		case <-done:
			return
		case <-stop:
			return
		default:
		}

		udpConn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, addr, err := udpConn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}

		addrMu.Lock()
		addrs[addr.String()] = addr
		addrMu.Unlock()

		data := make([]byte, n)
		copy(data, buf[:n])
		if err := protocol.WriteFrame(stream, protocol.UDPPacket{SourceAddr: addr.String(), Data: data}); err != nil {
			return
		}
		atomic.AddInt64(&lc.in, int64(n))
	}
}

// --- Client side ---

type udpClientSession struct {
	conn       *net.UDPConn
	lastActive int64 // unix nano, atomic
}

// handleUDPStream services one UDP tunnel's dedicated stream for as long as
// it stays open. Each distinct public-side sender address gets its own
// local UDP socket to the target service, so responses aren't cross-wired
// between simultaneous senders; idle sessions are reaped after a timeout
// since UDP has no notion of "closed".
func (c *Client) handleUDPStream(stream net.Conn, spec protocol.TunnelSpec) {
	defer stream.Close()

	local := net.JoinHostPort(spec.LocalHost, fmt.Sprintf("%d", spec.LocalPort))
	localAddr, err := net.ResolveUDPAddr("udp", local)
	if err != nil {
		c.Log.Warn("resolve local UDP target failed", "tunnel", spec.Name, "target", local, "err", err)
		return
	}

	var writeMu sync.Mutex
	var sessionsMu sync.Mutex
	sessions := make(map[string]*udpClientSession)

	stopReaper := make(chan struct{})
	defer close(stopReaper)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopReaper:
				return
			case <-ticker.C:
				cutoff := time.Now().Add(-udpSessionIdleTimeout).UnixNano()
				sessionsMu.Lock()
				for addr, sess := range sessions {
					if atomic.LoadInt64(&sess.lastActive) < cutoff {
						sess.conn.Close()
						delete(sessions, addr)
					}
				}
				sessionsMu.Unlock()
			}
		}
	}()

	for {
		var pkt protocol.UDPPacket
		if err := protocol.ReadFrame(stream, &pkt); err != nil {
			return
		}

		sessionsMu.Lock()
		sess, ok := sessions[pkt.SourceAddr]
		if !ok {
			localConn, err := net.DialUDP("udp", nil, localAddr)
			if err != nil {
				sessionsMu.Unlock()
				c.Log.Warn("dial local UDP target failed", "tunnel", spec.Name, "err", err)
				continue
			}
			sess = &udpClientSession{conn: localConn}
			sessions[pkt.SourceAddr] = sess
			sessionsMu.Unlock()

			srcAddr := pkt.SourceAddr
			go func() {
				buf := make([]byte, udpReadBufSize)
				for {
					localConn.SetReadDeadline(time.Now().Add(udpSessionIdleTimeout))
					n, err := localConn.Read(buf)
					if err != nil {
						return
					}
					atomic.StoreInt64(&sess.lastActive, time.Now().UnixNano())

					data := make([]byte, n)
					copy(data, buf[:n])
					writeMu.Lock()
					err = protocol.WriteFrame(stream, protocol.UDPPacket{SourceAddr: srcAddr, Data: data})
					writeMu.Unlock()
					if err != nil {
						return
					}
				}
			}()
		} else {
			sessionsMu.Unlock()
		}

		atomic.StoreInt64(&sess.lastActive, time.Now().UnixNano())
		if _, err := sess.conn.Write(pkt.Data); err != nil {
			c.Log.Warn("write to local UDP target failed", "tunnel", spec.Name, "err", err)
		}
	}
}
