// Package tunnel implements Portly's control-plane and data-plane: the
// server accepts authenticated client sessions, multiplexes them via yamux,
// and dynamically opens/closes public TCP listeners per tunnel without ever
// needing a process restart. The client mirrors this to receive tunnel
// config and dial local targets.
package tunnel

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/protocol"
)

const (
	authTimeout       = 10 * time.Second
	reconcileInterval = 3 * time.Second
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
	ln       net.Listener
	cancel   func()
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
	}
}

// Run starts the control-plane listener and the reconciliation loop. It
// blocks until the listener fails or the process is terminated.
func (s *Server) Run() error {
	ln, err := tls.Listen("tcp", s.ControlAddr, s.TLSConfig)
	if err != nil {
		return fmt.Errorf("listen control addr: %w", err)
	}
	defer ln.Close()

	s.Log.Info("control-plane listening", "addr", s.ControlAddr)

	go s.reconcileLoop()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go s.handleConn(conn)
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

	client, err := s.DB.GetClientByTokenHash(db.HashToken(req.Token))
	if err != nil {
		log.Warn("auth rejected: unknown token")
		protocol.WriteFrame(conn, protocol.AuthResponse{OK: false, Error: "invalid token"})
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

// pushTunnelConfig sends the client's current tunnel set over its control
// stream, if it changed since the last push.
func (s *Server) pushTunnelConfig(cs *clientSession) {
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
			ID:         t.ID,
			Name:       t.Name,
			LocalHost:  t.LocalHost,
			LocalPort:  t.LocalPort,
			PublicPort: t.PublicPort,
			Protocol:   protocol.ProtocolTCP,
		})
		fingerprint += fmt.Sprintf("%s:%s:%d;", t.ID, t.LocalHost, t.LocalPort)
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
	for range ticker.C {
		s.reconcileListeners()
		s.reconcileClientConfigs()
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
			pl.ln.Close()
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

func (s *Server) startTunnelListener(t db.Tunnel) (*publicListener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", t.PublicPort))
	if err != nil {
		return nil, err
	}

	stop := make(chan struct{})
	cancel := func() { close(stop) }

	go func() {
		s.Log.Info("tunnel listener started", "tunnel", t.Name, "public_port", t.PublicPort, "client_id", t.ClientID)
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

	return &publicListener{tunnelID: t.ID, ln: ln, cancel: cancel}, nil
}

func (s *Server) proxyConn(t db.Tunnel, publicConn net.Conn) {
	defer publicConn.Close()

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

	if err := protocol.WriteFrame(stream, protocol.StreamHeader{TunnelID: t.ID}); err != nil {
		s.Log.Warn("write stream header failed", "tunnel", t.Name, "err", err)
		return
	}

	bytesIn, bytesOut := pipe(publicConn, stream)
	if bytesIn > 0 || bytesOut > 0 {
		s.DB.AddTunnelTraffic(t.ID, bytesIn, bytesOut)
	}
}

// pipe copies bytes bidirectionally between a (client) and b (tunnel stream)
// until either side closes, returning bytes read from a (in) and from b (out).
func pipe(a, b io.ReadWriteCloser) (bytesIn, bytesOut int64) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, _ := io.Copy(b, a)
		bytesIn = n
		b.Close()
	}()
	go func() {
		defer wg.Done()
		n, _ := io.Copy(a, b)
		bytesOut = n
		a.Close()
	}()

	wg.Wait()
	return
}

func yamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 15 * time.Second
	cfg.LogOutput = io.Discard
	return cfg
}
