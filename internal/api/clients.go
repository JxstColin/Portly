package api

import (
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/jxstcolin/portly/internal/db"
	"github.com/jxstcolin/portly/internal/protocol"
)

var clientNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

const enrollCodeTTL = 15 * time.Minute

type clientView struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	CreatedAt         int64  `json:"created_at"`
	LastSeen          *int64 `json:"last_seen,omitempty"`
	Connected         bool   `json:"connected"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes,omitempty"`
}

func toClientView(c db.Client, connected bool) clientView {
	v := clientView{
		ID:                c.ID,
		Name:              c.Name,
		CreatedAt:         c.CreatedAt.Unix(),
		Connected:         connected,
		TrafficLimitBytes: c.TrafficLimitBytes,
	}
	if c.LastSeen != nil {
		ts := c.LastSeen.Unix()
		v.LastSeen = &ts
	}
	return v
}

func (s *Server) handleListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.DB.ListClients()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	connected := s.Tunnels.ConnectedClientIDs()

	out := make([]clientView, 0, len(clients))
	for _, c := range clients {
		out = append(out, toClientView(c, connected[c.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

type createClientRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var req createClientRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !clientNamePattern.MatchString(req.Name) {
		writeError(w, http.StatusBadRequest, "name must be 1-64 characters: letters, digits, '-', '_'")
		return
	}

	client, token, err := s.DB.CreateClient(req.Name)
	if err != nil {
		writeError(w, http.StatusConflict, "a client with that name already exists")
		return
	}

	code, err := s.DB.CreateEnrollmentCode(client.ID, token, enrollCodeTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"client":          toClientView(client, false),
		"install_command": s.installCommand(code),
		"enroll_code":     code,
		"expires_at":      time.Now().Add(enrollCodeTTL).Unix(),
	})
}

func (s *Server) installCommand(code string) string {
	return fmt.Sprintf("curl -fsSL '%s/install.sh?code=%s' | sudo bash", s.apiBaseURL(), code)
}

// handleReissueInstall gets a machine a fresh install command — for when
// its original enrollment code expired unused, or the "Add machine" dialog
// got closed before it was ever run. Only allowed for machines that have
// never successfully connected: rotating the token of one that's already
// running would silently break it the next time it reconnects, which isn't
// what "I lost the command" means.
func (s *Server) handleReissueInstall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	client, err := s.DB.GetClientByID(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	if client.LastSeen != nil {
		writeError(w, http.StatusConflict, "this machine already connected once — delete and re-add it to get a fresh install command")
		return
	}

	token, err := s.DB.RotateClientToken(client.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	code, err := s.DB.CreateEnrollmentCode(client.ID, token, enrollCodeTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"client":          toClientView(client, false),
		"install_command": s.installCommand(code),
		"enroll_code":     code,
		"expires_at":      time.Now().Add(enrollCodeTTL).Unix(),
	})
}

// handleListClientDevices returns whatever LAN devices this client last
// reported, for the "Add tunnel" UI's local-host suggestions. Empty (not an
// error) if it's never reported one yet — e.g. it just connected and hasn't
// completed its first scan.
func (s *Server) handleListClientDevices(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.DB.GetClientByID(id); err != nil {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	devices := s.Tunnels.DiscoveredDevices(id)
	if devices == nil {
		devices = []protocol.Device{}
	}
	writeJSON(w, http.StatusOK, devices)
}

type updateClientSettingsRequest struct {
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
}

// handleUpdateClientSettings sets a machine-wide traffic limit — the
// combined total across all of its tunnels (see db.AddTunnelTraffic).
func (s *Server) handleUpdateClientSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateClientSettingsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TrafficLimitBytes != nil && *req.TrafficLimitBytes <= 0 {
		writeError(w, http.StatusBadRequest, "traffic_limit_bytes must be positive, or omitted/null for unlimited")
		return
	}

	if err := s.DB.UpdateClientTrafficLimit(id, req.TrafficLimitBytes); err != nil {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}

	c, err := s.DB.GetClientByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toClientView(c, s.Tunnels.ConnectedClientIDs()[id]))
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Tell the machine to clean itself up before forgetting about it. If
	// it's offline right now, DeleteClient below still revokes its token,
	// so it gets the same instruction the moment it next tries to connect.
	if s.Tunnels.PushUninstall(id) {
		s.Log.Info("pushed uninstall to connected client", "client_id", id)
	}

	if err := s.DB.DeleteClient(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
