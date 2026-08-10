// Package api implements Portly's management HTTP API: admin auth, CRUD for
// clients/tunnels, traffic history + a live WebSocket feed, and the
// self-installing "add machine" flow (GET /install.sh + client binary
// downloads + one-time enrollment code exchange). It's designed to be
// consumed by a separate Next.js web UI process over the network, so the UI
// stays up independently of the tunnel engine.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/tunnel"
)

const sessionCookieName = "portly_session"
const sessionTTL = 7 * 24 * time.Hour

type Server struct {
	DB             *db.DB
	Tunnels        *tunnel.Server
	Log            *slog.Logger
	AdvertiseHost  string
	ControlPort    int
	APIPort        int
	CAFingerprint  string
	AllowedOrigins []string
	ClientBinaries map[string][]byte // "linux-amd64" etc -> raw portly-client binary

	sessMu   sync.Mutex
	sessions map[string]sessionInfo
}

type sessionInfo struct {
	adminID string
	expires time.Time
}

func NewServer(database *db.DB, tunnels *tunnel.Server, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		DB:             database,
		Tunnels:        tunnels,
		Log:            logger,
		ClientBinaries: make(map[string][]byte),
		sessions:       make(map[string]sessionInfo),
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/auth/change-credentials", s.requireAuth(s.handleChangeCredentials))

	mux.HandleFunc("GET /api/server/info", s.requireAuth(s.handleServerInfo))

	mux.HandleFunc("GET /api/clients", s.requireAuth(s.handleListClients))
	mux.HandleFunc("POST /api/clients", s.requireAuth(s.handleCreateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireAuth(s.handleDeleteClient))

	mux.HandleFunc("GET /api/tunnels", s.requireAuth(s.handleListTunnels))
	mux.HandleFunc("POST /api/tunnels", s.requireAuth(s.handleCreateTunnel))
	mux.HandleFunc("PATCH /api/tunnels/{id}", s.requireAuth(s.handleUpdateTunnel))
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.requireAuth(s.handleDeleteTunnel))

	mux.HandleFunc("GET /api/tunnels/{id}/traffic", s.requireAuth(s.handleTunnelTraffic))
	mux.HandleFunc("GET /api/ws/live", s.requireAuth(s.handleLiveWS))

	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("GET /downloads/{osarch}", s.handleDownloadClient)
	mux.HandleFunc("POST /api/enroll/exchange", s.handleEnrollExchange)

	return s.withCORS(mux)
}

func (s *Server) Run(addr string) error {
	s.Log.Info("api listening", "addr", addr)
	return http.ListenAndServe(addr, s.Router())
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(origin string) bool {
	for _, o := range s.AllowedOrigins {
		if o == origin || o == "*" {
			return true
		}
	}
	return false
}

// --- session helpers ---

func (s *Server) createSession(adminID string) string {
	token := randomHex(32)
	s.sessMu.Lock()
	s.sessions[token] = sessionInfo{adminID: adminID, expires: time.Now().Add(sessionTTL)}
	s.sessMu.Unlock()
	return token
}

func (s *Server) sessionAdminID(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return "", false
	}
	s.sessMu.Lock()
	defer s.sessMu.Unlock()
	info, ok := s.sessions[c.Value]
	if !ok || time.Now().After(info.expires) {
		delete(s.sessions, c.Value)
		return "", false
	}
	return info.adminID, true
}

func (s *Server) destroySession(token string) {
	s.sessMu.Lock()
	delete(s.sessions, token)
	s.sessMu.Unlock()
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.sessionAdminID(r); !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		next(w, r)
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *Server) apiBaseURL() string {
	return fmt.Sprintf("http://%s:%d", s.AdvertiseHost, s.APIPort)
}
