// Package tunnel implements Portly's control-plane and data-plane: the
// server accepts authenticated client sessions, multiplexes them via yamux,
// and dynamically opens/closes public TCP/UDP listeners per tunnel without
// ever needing a process restart. The client mirrors this to receive tunnel
// config and dial local targets.
package tunnel

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/protocol"
)

const (
	authTimeout          = 10 * time.Second
	reconcileInterval    = 3 * time.Second
	trafficFlushInterval = 1 * time.Second
)

type Server struct {
	DB          *db.DB
	TLSConfig   *tls.Config
	ControlAddr string
	Log         *slog.Logger

	mu       sync.RWMutex
	sessions map[string]*clientSession // client ID -> active session

	listenersMu sync.Mutex
	listeners   map[string]*publicListener // tunnel ID -> running listener

	trafficMu sync.Mutex
	liveBytes map[string]*liveCounter // tunnel ID -> live in-memory counters

	devicesMu sync.RWMutex
	devices   map[string][]protocol.Device // client ID -> last reported LAN devices

	controlLnMu sync.Mutex
	controlLn   net.Listener // set once Run's Accept loop is up, for Shutdown to close

	shutdownOnce sync.Once
	shutdownCh   chan struct{} // closed by Shutdown; reconcileLoop/reconcileListeners stop reacting once closed

	// activeConns tracks in-flight proxied TCP connections (one per actual
	// player/game-client socket) — Shutdown waits for this to drain instead
	// of just severing everyone the instant the process is asked to stop,
	// so a restart/update only refuses *new* connections during the grace
	// window instead of kicking everyone already playing.
	activeConns sync.WaitGroup
}

type clientSession struct {
	clientID      string
	name          string
	session       *yamux.Session
	controlStream net.Conn
	writeMu       sync.Mutex
	lastPushed    string // cheap change-detection fingerprint
}

type publicListener struct {
	tunnelID string
	closer   io.Closer
	cancel   func()
}

// liveCounter holds cumulative byte counts for a tunnel, updated as bytes
// actually flow (not just when a connection closes), so the WS live feed
// and the periodic DB flush both see real-time-accurate totals.
type liveCounter struct {
	in, out int64 // access via atomic
}

func NewServer(database *db.DB, tlsConfig *tls.Config, controlAddr string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		DB:          database,
		TLSConfig:   tlsConfig,
		ControlAddr: controlAddr,
		Log:         logger,
		sessions:    make(map[string]*clientSession),
		listeners:   make(map[string]*publicListener),
		liveBytes:   make(map[string]*liveCounter),
		devices:     make(map[string][]protocol.Device),
		shutdownCh:  make(chan struct{}),
	}
}

// Run starts the control-plane listener and the reconciliation/traffic
// loops. It blocks until the listener fails, Shutdown is called, or the
// process is terminated.
func (s *Server) Run() error {
	ln, err := tls.Listen("tcp", s.ControlAddr, s.TLSConfig)
	if err != nil {
		return fmt.Errorf("listen control addr: %w", err)
	}
	defer ln.Close()

	s.controlLnMu.Lock()
	s.controlLn = ln
	s.controlLnMu.Unlock()

	s.Log.Info("control-plane listening", "addr", s.ControlAddr)

	go s.reconcileLoop()
	go s.trafficFlushLoop()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.shutdownCh:
				// Expected: Shutdown closed the listener on purpose.
				// Already-connected client sessions and the player
				// connections flowing through them are untouched by this.
				return nil
			default:
				return fmt.Errorf("accept: %w", err)
			}
		}
		go s.handleConn(conn)
	}
}

// Shutdown stops accepting new client-machine and player connections, then
// waits (up to ctx's deadline) for already-established player connections
// to finish naturally before returning. It deliberately does NOT touch
// existing client-machine sessions or their in-flight proxied streams —
// those keep working right up until either they end on their own or ctx's
// deadline passes, so a restart/update only refuses brand-new connections
// during the drain window instead of severing everyone already playing.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })

	s.controlLnMu.Lock()
	if s.controlLn != nil {
		s.controlLn.Close()
	}
	s.controlLnMu.Unlock()

	s.listenersMu.Lock()
	for id, pl := range s.listeners {
		pl.cancel()
		pl.closer.Close()
		delete(s.listeners, id)
	}
	s.listenersMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.activeConns.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) handleConn(conn net.Conn) {
	log := s.Log.With("remote", conn.RemoteAddr().String())

	conn.SetDeadline(time.Now().Add(authTimeout))
	var req protocol.AuthRequest
	if err := protocol.ReadFrame(conn, &req); err != nil {
		log.Warn("auth read failed", "err", err)
		conn.Close()
		return
	}

	tokenHash := db.HashToken(req.Token)
	client, err := s.DB.GetClientByTokenHash(tokenHash)
	if err != nil {
		if s.DB.IsTokenRevoked(tokenHash) {
			log.Info("rejecting deleted client, telling it to uninstall")
			protocol.WriteFrame(conn, protocol.AuthResponse{OK: false, Uninstall: true, Error: "this machine was removed in the Portly UI"})
		} else {
			log.Warn("auth rejected: unknown token")
			protocol.WriteFrame(conn, protocol.AuthResponse{OK: false, Error: "invalid token"})
		}
		conn.Close()
		return
	}

	if err := protocol.WriteFrame(conn, protocol.AuthResponse{OK: true, ClientID: client.ID, Name: client.Name}); err != nil {
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})

	log = log.With("client", client.Name, "client_id", client.ID)
	log.Info("client authenticated")

	ySession, err := yamux.Server(conn, yamuxConfig())
	if err != nil {
		log.Error("yamux session failed", "err", err)
		conn.Close()
		return
	}

	controlStream, err := ySession.Open()
	if err != nil {
		log.Error("open control stream failed", "err", err)
		ySession.Close()
		return
	}

	// Unlike every other stream here, which the server opens, device reports
	// are opened by the client (short-lived, one report per stream) — accept
	// whatever it sends on this session for as long as it's alive.
	go s.acceptClientStreams(ySession, client.ID, log)

	cs := &clientSession{
		clientID:      client.ID,
		name:          client.Name,
		session:       ySession,
		controlStream: controlStream,
	}

	s.mu.Lock()
	if old, exists := s.sessions[client.ID]; exists {
		old.session.Close() // replace stale session on reconnect
	}
	s.sessions[client.ID] = cs
	s.mu.Unlock()

	s.DB.UpdateClientLastSeen(client.ID)
	s.pushTunnelConfig(cs)

	<-ySession.CloseChan()
	log.Info("client disconnected")

	s.mu.Lock()
	if s.sessions[client.ID] == cs {
		delete(s.sessions, client.ID)
	}
	s.mu.Unlock()
}

// acceptClientStreams accepts client-opened streams for the lifetime of a
// session — currently only ever device reports, but kept generic (reading
// just a DeviceReport frame) rather than assuming that forever.
func (s *Server) acceptClientStreams(session *yamux.Session, clientID string, log *slog.Logger) {
	for {
		stream, err := session.Accept()
		if err != nil {
			return // session closed
		}
		go func() {
			defer stream.Close()
			var report protocol.DeviceReport
			if err := protocol.ReadFrame(stream, &report); err != nil {
				log.Warn("read device report failed", "err", err)
				return
			}
			s.devicesMu.Lock()
			s.devices[clientID] = report.Devices
			s.devicesMu.Unlock()
		}()
	}
}

// DiscoveredDevices returns the last set of LAN devices clientID reported
// (nil if it never has), for the "Add tunnel" UI's local-host suggestions.
func (s *Server) DiscoveredDevices(clientID string) []protocol.Device {
	s.devicesMu.RLock()
	defer s.devicesMu.RUnlock()
	return s.devices[clientID]
}

// ConnectedClientIDs returns the set of client IDs with a live control-plane
// session right now, for the API/UI to show online/offline status.
func (s *Server) ConnectedClientIDs() map[string]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]bool, len(s.sessions))
	for id := range s.sessions {
		out[id] = true
	}
	return out
}

// PushUninstall tells a connected client to uninstall itself and disconnects
// it, for immediate effect when its machine is deleted in the UI. Returns
// false if the client wasn't connected (its token is still revoked via the
// DB, so it'll get the same instruction the next time it tries to connect).
func (s *Server) PushUninstall(clientID string) bool {
	s.mu.RLock()
	cs, ok := s.sessions[clientID]
	s.mu.RUnlock()
	if !ok {
		return false
	}

	cs.writeMu.Lock()
	err := protocol.WriteFrame(cs.controlStream, protocol.TunnelConfigPush{Uninstall: true})
	cs.writeMu.Unlock()
	if err != nil {
		s.Log.Warn("push uninstall failed", "client", cs.name, "err", err)
		return false
	}
	return true
}

// pushTunnelConfig sends the client's current tunnel set over its control
// stream, if it changed since the last push.
func (s *Server) pushTunnelConfig(cs *clientSession) {
	// Called once at connect and then every reconcileInterval for as long as
	// the session stays open (reconcileClientConfigs), so this doubles as a
	// cheap "still alive" heartbeat — without it, last_seen would only ever
	// reflect the moment a long-lived connection first started, showing a
	// stale "5h ago" for a client that's actually online right now.
	if err := s.DB.UpdateClientLastSeen(cs.clientID); err != nil {
		s.Log.Warn("update last_seen failed", "client", cs.name, "err", err)
	}

	tunnels, err := s.DB.ListTunnelsByClient(cs.clientID)
	if err != nil {
		s.Log.Error("list tunnels for push failed", "err", err)
		return
	}

	specs := make([]protocol.TunnelSpec, 0, len(tunnels))
	fingerprint := ""
	for _, t := range tunnels {
		if !t.Enabled {
			continue
		}
		specs = append(specs, protocol.TunnelSpec{
			ID:            t.ID,
			Name:          t.Name,
			LocalHost:     t.LocalHost,
			LocalPort:     t.LocalPort,
			PublicPort:    t.PublicPort,
			Protocol:      protocol.Protocol(t.Protocol),
			ProxyProtocol: t.ProxyProtocol,
		})
		fingerprint += fmt.Sprintf("%s:%s:%d:%s:%t;", t.ID, t.LocalHost, t.LocalPort, t.Protocol, t.ProxyProtocol)
	}

	if fingerprint == cs.lastPushed {
		return
	}

	cs.writeMu.Lock()
	err = protocol.WriteFrame(cs.controlStream, protocol.TunnelConfigPush{Tunnels: specs})
	cs.writeMu.Unlock()
	if err != nil {
		s.Log.Warn("push tunnel config failed", "client", cs.name, "err", err)
		return
	}
	cs.lastPushed = fingerprint
}

// reconcileLoop periodically reconciles both the set of running public
// listeners and each connected client's pushed tunnel config against the
// database, so `portly-server tunnel add/rm` takes effect live without
// restarting the server process.
func (s *Server) reconcileLoop() {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	s.reconcileListeners()
	for {
		select {
		case <-s.shutdownCh:
			return
		case <-ticker.C:
			s.reconcileListeners()
			s.reconcileClientConfigs()
		}
	}
}

func (s *Server) reconcileClientConfigs() {
	s.mu.RLock()
	sessions := make([]*clientSession, 0, len(s.sessions))
	for _, cs := range s.sessions {
		sessions = append(sessions, cs)
	}
	s.mu.RUnlock()

	for _, cs := range sessions {
		s.pushTunnelConfig(cs)
	}
}

func (s *Server) reconcileListeners() {
	select {
	case <-s.shutdownCh:
		// Shutdown already closed every listener on purpose — never race
		// against it and re-open one for a tunnel that's still enabled in
		// the DB (a tick could otherwise land concurrently with Shutdown).
		return
	default:
	}

	tunnels, err := s.DB.ListEnabledTunnels()
	if err != nil {
		s.Log.Error("list enabled tunnels failed", "err", err)
		return
	}

	wanted := make(map[string]db.Tunnel, len(tunnels))
	for _, t := range tunnels {
		wanted[t.ID] = t
	}

	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()

	// Stop listeners for tunnels that were deleted/disabled.
	for id, pl := range s.listeners {
		if _, ok := wanted[id]; !ok {
			pl.cancel()
			pl.closer.Close()
			delete(s.listeners, id)
			s.Log.Info("stopped tunnel listener", "tunnel_id", id)
		}
	}

	// Start listeners for new tunnels.
	for id, t := range wanted {
		if _, ok := s.listeners[id]; ok {
			continue
		}
		pl, err := s.startTunnelListener(t)
		if err != nil {
			s.Log.Error("start tunnel listener failed", "tunnel", t.Name, "public_port", t.PublicPort, "err", err)
			continue
		}
		s.listeners[id] = pl
	}
}

// ProbePublicPort reports whether a public port is actually bindable right
// now, by binding it and immediately closing it again. Used to give
// immediate feedback when a tunnel is created/enabled instead of letting it
// silently fail 3 seconds later in reconcileListeners with nothing but a log
// line — e.g. picking a port the VPS's own sshd, portly-server itself, or
// another process already owns.
func (s *Server) ProbePublicPort(proto string, port int) error {
	if proto == string(protocol.ProtocolUDP) {
		ln, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
		if err != nil {
			return err
		}
		return ln.Close()
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return ln.Close()
}

func (s *Server) startTunnelListener(t db.Tunnel) (*publicListener, error) {
	if t.Protocol == string(protocol.ProtocolUDP) {
		return s.startUDPListener(t)
	}
	return s.startTCPListener(t)
}

func (s *Server) startTCPListener(t db.Tunnel) (*publicListener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", t.PublicPort))
	if err != nil {
		return nil, err
	}

	stop := make(chan struct{})
	cancel := func() { close(stop) }

	go func() {
		s.Log.Info("tunnel listener started", "tunnel", t.Name, "protocol", "tcp", "public_port", t.PublicPort, "client_id", t.ClientID)
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stop:
					return
				default:
					s.Log.Warn("accept failed on tunnel listener", "tunnel", t.Name, "err", err)
					return
				}
			}
			go s.proxyConn(t, conn)
		}
	}()

	return &publicListener{tunnelID: t.ID, closer: ln, cancel: cancel}, nil
}

func (s *Server) proxyConn(t db.Tunnel, publicConn net.Conn) {
	defer publicConn.Close()

	// Tracked so Shutdown can wait for every already-accepted player
	// connection to finish naturally instead of severing it — this is
	// exactly what makes an update/restart not kick anyone already playing.
	s.activeConns.Add(1)
	defer s.activeConns.Done()

	s.mu.RLock()
	cs, ok := s.sessions[t.ClientID]
	s.mu.RUnlock()
	if !ok {
		s.Log.Warn("no active session for tunnel, dropping connection", "tunnel", t.Name)
		return
	}

	stream, err := cs.session.Open()
	if err != nil {
		s.Log.Warn("open stream failed", "tunnel", t.Name, "err", err)
		return
	}
	defer stream.Close()

	if err := protocol.WriteFrame(stream, protocol.StreamHeader{TunnelID: t.ID, RemoteAddr: publicConn.RemoteAddr().String()}); err != nil {
		s.Log.Warn("write stream header failed", "tunnel", t.Name, "err", err)
		return
	}

	lc := s.getLiveCounter(t.ID)
	pipe(publicConn, stream, &lc.in, &lc.out)
}

// getLiveCounter returns (creating if necessary) the in-memory byte counter
// for a tunnel. It's never reset — it mirrors the DB total, just updated in
// real time instead of only at connection-close or on a fixed tick.
func (s *Server) getLiveCounter(tunnelID string) *liveCounter {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	c, ok := s.liveBytes[tunnelID]
	if !ok {
		c = &liveCounter{}
		s.liveBytes[tunnelID] = c
	}
	return c
}

// LiveBytesSnapshot returns each tunnel's current cumulative byte counts
// as of right now (sub-second fresh), for the WS live feed to compute
// accurate throughput without waiting on the DB flush cadence.
func (s *Server) LiveBytesSnapshot() map[string][2]int64 {
	s.trafficMu.Lock()
	defer s.trafficMu.Unlock()
	out := make(map[string][2]int64, len(s.liveBytes))
	for id, c := range s.liveBytes {
		out[id] = [2]int64{atomic.LoadInt64(&c.in), atomic.LoadInt64(&c.out)}
	}
	return out
}

// trafficFlushLoop periodically persists the delta since the last flush for
// every tunnel with in-memory activity, so totals/history in the DB stay
// close to real time instead of only updating when a connection ends
// (which, for a long-lived connection, could be hours away).
func (s *Server) trafficFlushLoop() {
	ticker := time.NewTicker(trafficFlushInterval)
	defer ticker.Stop()

	lastFlushed := make(map[string][2]int64)
	for range ticker.C {
		for id, cur := range s.LiveBytesSnapshot() {
			prev := lastFlushed[id]
			deltaIn := cur[0] - prev[0]
			deltaOut := cur[1] - prev[1]
			if deltaIn <= 0 && deltaOut <= 0 {
				continue
			}
			if err := s.DB.AddTunnelTraffic(id, deltaIn, deltaOut); err != nil {
				s.Log.Warn("flush traffic failed", "tunnel_id", id, "err", err)
				continue
			}
			lastFlushed[id] = cur
		}
	}
}

// countingReader wraps a reader, atomically adding every byte actually read
// to counter — used so pipe() reports live throughput as data flows instead
// of only once the whole copy finishes.
type countingReader struct {
	io.Reader
	counter *int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.Reader.Read(p)
	if n > 0 {
		atomic.AddInt64(c.counter, int64(n))
	}
	return n, err
}

// pipe copies bytes bidirectionally between a and b until either side
// closes, atomically adding to inCounter (a->b) and outCounter (b->a) as
// bytes actually flow.
func pipe(a, b io.ReadWriteCloser, inCounter, outCounter *int64) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(b, &countingReader{Reader: a, counter: inCounter})
		b.Close()
	}()
	go func() {
		defer wg.Done()
		io.Copy(a, &countingReader{Reader: b, counter: outCounter})
		a.Close()
	}()

	wg.Wait()
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 15 * time.Second

	// yamux's default here is 10s, and it is not just a write deadline: the
	// keepalive ping has to complete a full round trip within it too, and a
	// miss on either tears down the WHOLE session — which means every
	// player on every tunnel of that machine is dropped at once, and the
	// client then reconnects as if nothing were wrong. On a home uplink
	// that is far too tight. A backup starting, someone uploading a video,
	// or simply a burst of players is enough to queue 10s of bufferbloat
	// ahead of the ping, and everyone gets kicked for a link that was never
	// actually down. Detecting a genuinely dead peer now takes up to a
	// minute instead of ten seconds, which costs nothing in practice (the
	// client reconnects about a second later either way) and is a much
	// better trade than severing live sessions over transient congestion.
	cfg.ConnectionWriteTimeout = 60 * time.Second

	// Default is 256 KiB per stream. Raising it gives each player's
	// connection more room in flight before it has to stall waiting for
	// window updates, which matters because a stall long enough to trip
	// the game server's own read timeout gets the player kicked even when
	// the tunnel itself is perfectly healthy. 1 MiB keeps worst-case
	// buffering per connection bounded and modest.
	cfg.MaxStreamWindowSize = 1024 * 1024

	cfg.LogOutput = io.Discard
	return cfg
}
