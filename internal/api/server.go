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
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/tunnel"
	"github.com/jxstcolin/portly/internal/updatecheck"
)

const sessionCookieName = "portly_session"
const sessionTTL = 7 * 24 * time.Hour

type Server struct {
	DB              *db.DB
	Tunnels         *tunnel.Server
	Log             *slog.Logger
	AdvertiseHost   string
	AdvertiseHostV6 string // best-effort detected IPv6, "" if none/unavailable
	ControlPort     int
	APIPort         int
	CAFingerprint   string
	AllowedOrigins  []string
	ClientBinaries  map[string][]byte // "linux-amd64" etc -> raw portly-client binary
	// ClientBinarySHA256 mirrors ClientBinaries, keyed the same way, so
	// already-installed clients can poll a cheap hash instead of
	// downloading the full binary to check whether it's out of date.
	ClientBinarySHA256 map[string]string

	// WebUpstream, if set, is where non-API requests get reverse-proxied
	// (the Next.js UI process, e.g. "http://127.0.0.1:3000") so the browser
	// only ever talks to one origin — no CORS, no baked-in API base URL.
	WebUpstream string
	// PublicHTTPPort/PublicHTTPSPort are the ports end users actually hit,
	// used to build install links and other absolute URLs. 0 (or the
	// standard 80/443) is omitted from generated URLs.
	PublicHTTPPort  int
	PublicHTTPSPort int

	// OnDomainSet, if set by main.go (which owns the autocert.Manager), is
	// called after a new domain is persisted so it can kick off Let's
	// Encrypt certificate issuance and report progress back via
	// SetCertState. Runs in its own goroutine; may block.
	OnDomainSet func(domain string)

	// OnAdminClaimed, if set by main.go, is called once the first admin
	// account is created via the setup-code bootstrap flow, so it can clean
	// up the setup-code file it wrote to disk.
	OnAdminClaimed func()

	// OnFactoryReset, if set by main.go, is called after a factory reset
	// wipes the DB, so it can generate and print/write out a fresh setup
	// code the same way it does on a brand new install.
	OnFactoryReset func()

	// ApplyUpdate, if set by main.go, requests an update by writing the
	// marker file portly-update.path watches (see cmd/portly-server's
	// triggerUpdate) and returns once that request has been recorded, not
	// once the update itself is finished — the server gets restarted
	// partway through, so nothing here waits around for that. nil means
	// this server build has no updater wired up at all (a dev/test build).
	ApplyUpdate func() error
	// BuildCommit is the git commit this binary was built from (see
	// cmd/portly-server's buildCommit), used for on-demand update checks.
	BuildCommit string
	// DataDir is where scripts/quickstart-vps.sh writes update-progress.json
	// and update.log while an update runs, so handleUpdateProgress can read
	// them straight off disk — the one channel that survives the restart
	// ApplyUpdate triggers partway through every update.
	DataDir string

	sessMu   sync.Mutex
	sessions map[string]sessionInfo

	domainMu sync.RWMutex
	domain   string

	certMu    sync.RWMutex
	certState string // "", "pending", "ready", "error"
	certErr   string

	updateMu     sync.RWMutex
	updateStatus updatecheck.Status
}

type sessionInfo struct {
	adminID string
	expires time.Time
}

func NewServer(database *db.DB, tunnels *tunnel.Server, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		DB:                 database,
		Tunnels:            tunnels,
		Log:                logger,
		ClientBinaries:     make(map[string][]byte),
		ClientBinarySHA256: make(map[string]string),
		sessions:           make(map[string]sessionInfo),
	}
	if d, ok, err := database.GetSetting("domain"); err == nil && ok {
		s.domain = d
	}
	return s
}

// Domain returns the currently configured public domain, or "" if none has
// been set yet (the panel is only reachable by IP so far).
func (s *Server) Domain() string {
	s.domainMu.RLock()
	defer s.domainMu.RUnlock()
	return s.domain
}

func (s *Server) setDomain(d string) {
	s.domainMu.Lock()
	s.domain = d
	s.domainMu.Unlock()
}

// SetCertState is called by main.go's autocert integration to report
// certificate issuance progress ("pending", "ready", "error") back to the
// setup API.
func (s *Server) SetCertState(state, errMsg string) {
	s.certMu.Lock()
	s.certState, s.certErr = state, errMsg
	s.certMu.Unlock()
}

func (s *Server) getCertState() (state, errMsg string) {
	s.certMu.RLock()
	defer s.certMu.RUnlock()
	return s.certState, s.certErr
}

// SetUpdateStatus is called by main.go's periodic update checker to report
// the result of comparing this build against the latest commit on GitHub.
func (s *Server) SetUpdateStatus(status updatecheck.Status) {
	s.updateMu.Lock()
	s.updateStatus = status
	s.updateMu.Unlock()
}

func (s *Server) getUpdateStatus() updatecheck.Status {
	s.updateMu.RLock()
	defer s.updateMu.RUnlock()
	return s.updateStatus
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))
	mux.HandleFunc("POST /api/auth/change-credentials", s.requireAuth(s.handleChangeCredentials))

	mux.HandleFunc("GET /api/bootstrap/status", s.handleBootstrapStatus)
	mux.HandleFunc("POST /api/bootstrap/claim", s.handleBootstrapClaim)

	// requireAuthOrAPIKey rather than requireAuth on the routes an external
	// service like a game panel actually needs (reading server info,
	// listing clients to pick one, full tunnel CRUD) — everything else
	// (creating/deleting machines, traffic history, the live WS, settings)
	// stays session-only, since those are either a physical-install step or
	// purely a web-UI concern an API key has no business touching.
	mux.HandleFunc("GET /api/server/info", s.requireAuthOrAPIKey(s.handleServerInfo))

	mux.HandleFunc("GET /api/clients", s.requireAuthOrAPIKey(s.handleListClients))
	mux.HandleFunc("POST /api/clients", s.requireAuth(s.handleCreateClient))
	mux.HandleFunc("DELETE /api/clients/{id}", s.requireAuth(s.handleDeleteClient))
	mux.HandleFunc("PATCH /api/clients/{id}/settings", s.requireAuth(s.handleUpdateClientSettings))
	mux.HandleFunc("GET /api/clients/{id}/devices", s.requireAuth(s.handleListClientDevices))
	mux.HandleFunc("POST /api/clients/{id}/reissue-install", s.requireAuth(s.handleReissueInstall))

	mux.HandleFunc("GET /api/tunnels", s.requireAuthOrAPIKey(s.handleListTunnels))
	mux.HandleFunc("POST /api/tunnels", s.requireAuthOrAPIKey(s.handleCreateTunnel))
	mux.HandleFunc("PATCH /api/tunnels/{id}", s.requireAuthOrAPIKey(s.handleUpdateTunnel))
	mux.HandleFunc("PATCH /api/tunnels/{id}/settings", s.requireAuthOrAPIKey(s.handleUpdateTunnelSettings))
	mux.HandleFunc("DELETE /api/tunnels/{id}", s.requireAuthOrAPIKey(s.handleDeleteTunnel))

	mux.HandleFunc("GET /api/tunnels/{id}/traffic", s.requireAuth(s.handleTunnelTraffic))
	mux.HandleFunc("GET /api/ws/live", s.requireAuth(s.handleLiveWS))

	mux.HandleFunc("GET /api/api-keys", s.requireAuth(s.handleListAPIKeys))
	mux.HandleFunc("POST /api/api-keys", s.requireAuth(s.handleCreateAPIKey))
	mux.HandleFunc("DELETE /api/api-keys/{id}", s.requireAuth(s.handleDeleteAPIKey))

	mux.HandleFunc("GET /install.sh", s.handleInstallScript)
	mux.HandleFunc("GET /downloads/{osarch}", s.handleDownloadClient)
	mux.HandleFunc("GET /downloads/{osarch}/sha256", s.handleDownloadClientChecksum)
	mux.HandleFunc("POST /api/enroll/exchange", s.handleEnrollExchange)

	mux.HandleFunc("GET /api/setup", s.requireAuth(s.handleSetupStatus))
	mux.HandleFunc("POST /api/setup/domain", s.requireAuth(s.handleSetDomain))

	mux.HandleFunc("POST /api/settings/factory-reset", s.requireAuth(s.handleFactoryReset))
	mux.HandleFunc("GET /api/settings/update-status", s.requireAuth(s.handleUpdateStatus))
	mux.HandleFunc("POST /api/settings/check-update", s.requireAuth(s.handleCheckUpdate))
	mux.HandleFunc("POST /api/settings/apply-update", s.requireAuth(s.handleApplyUpdate))
	// Deliberately NOT behind requireAuth — see handleUpdateProgress.
	mux.HandleFunc("GET /api/settings/update-progress", s.handleUpdateProgress)

	if s.WebUpstream != "" {
		if target, err := url.Parse(s.WebUpstream); err == nil {
			mux.Handle("/", httputil.NewSingleHostReverseProxy(target))
		} else {
			s.Log.Error("invalid web upstream URL, UI won't be reachable through this server", "web_upstream", s.WebUpstream, "err", err)
		}
	}

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

// clearAllSessions invalidates every active session — used by factory
// reset, since the admin account they belong to no longer exists.
func (s *Server) clearAllSessions() {
	s.sessMu.Lock()
	s.sessions = make(map[string]sessionInfo)
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

// requireAuthOrAPIKey accepts either an admin session (browser/web UI) or a
// "ptly_api_..." bearer token (external services, e.g. a game panel
// managing tunnels for it). Route registration in Router() decides which
// endpoints allow the API-key path at all — see the comment there.
func (s *Server) requireAuthOrAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.sessionAdminID(r); ok {
			next(w, r)
			return
		}
		if key, ok := s.apiKeyFromRequest(r); ok {
			// Best-effort — a failed timestamp update shouldn't block the
			// actual request the key was presented for.
			_ = s.DB.UpdateAPIKeyLastUsed(key.ID)
			next(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "not authenticated")
	}
}

func (s *Server) apiKeyFromRequest(r *http.Request) (db.ApiKey, bool) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		return db.ApiKey{}, false
	}
	token := strings.TrimPrefix(auth, prefix)
	if !strings.HasPrefix(token, "ptly_api_") {
		return db.ApiKey{}, false
	}
	key, err := s.DB.GetAPIKeyByHash(db.HashToken(token))
	if err != nil {
		return db.ApiKey{}, false
	}
	return key, true
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

// apiBaseURL is the origin end users and install scripts should hit: the
// configured domain over HTTPS if one is set, otherwise the advertised IP
// over plain HTTP — omitting the port whenever it's the standard 80/443.
func (s *Server) apiBaseURL() string {
	if d := s.Domain(); d != "" {
		if s.PublicHTTPSPort != 0 && s.PublicHTTPSPort != 443 {
			return fmt.Sprintf("https://%s:%d", d, s.PublicHTTPSPort)
		}
		return "https://" + d
	}
	if s.PublicHTTPPort != 0 && s.PublicHTTPPort != 80 {
		return fmt.Sprintf("http://%s:%d", s.AdvertiseHost, s.PublicHTTPPort)
	}
	return fmt.Sprintf("http://%s", s.AdvertiseHost)
}
