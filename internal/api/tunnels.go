package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jxstcolin/portly/internal/db"
)

type tunnelView struct {
	ID                string `json:"id"`
	ClientID          string `json:"client_id"`
	Name              string `json:"name"`
	Protocol          string `json:"protocol"`
	LocalHost         string `json:"local_host"`
	LocalPort         int    `json:"local_port"`
	PublicPort        int    `json:"public_port"`
	Enabled           bool   `json:"enabled"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes,omitempty"`
	PublicHostname    string `json:"public_hostname,omitempty"`
	BytesInTotal      int64  `json:"bytes_in_total"`
	BytesOutTotal     int64  `json:"bytes_out_total"`
	CreatedAt         int64  `json:"created_at"`
}

func toTunnelView(t db.Tunnel) tunnelView {
	return tunnelView{
		ID:                t.ID,
		ClientID:          t.ClientID,
		Name:              t.Name,
		Protocol:          t.Protocol,
		LocalHost:         t.LocalHost,
		LocalPort:         t.LocalPort,
		PublicPort:        t.PublicPort,
		Enabled:           t.Enabled,
		TrafficLimitBytes: t.TrafficLimitBytes,
		PublicHostname:    t.PublicHostname,
		BytesInTotal:      t.BytesInTotal,
		BytesOutTotal:     t.BytesOutTotal,
		CreatedAt:         t.CreatedAt.Unix(),
	}
}

func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")

	var tunnels []db.Tunnel
	var err error
	if clientID != "" {
		tunnels, err = s.DB.ListTunnelsByClient(clientID)
	} else {
		tunnels, err = s.DB.ListAllTunnels()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]tunnelView, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, toTunnelView(t))
	}
	writeJSON(w, http.StatusOK, out)
}

type createTunnelRequest struct {
	ClientID   string `json:"client_id"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol"`
	LocalHost  string `json:"local_host"`
	LocalPort  int    `json:"local_port"`
	PublicPort int    `json:"public_port"`
}

func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var req createTunnelRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	req.LocalHost = strings.TrimSpace(req.LocalHost)
	if req.LocalHost == "" {
		req.LocalHost = "127.0.0.1"
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Protocol == "" {
		req.Protocol = "tcp"
	}

	if req.ClientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}
	if _, err := s.DB.GetClientByID(req.ClientID); err != nil {
		writeError(w, http.StatusNotFound, "client not found")
		return
	}
	if req.Protocol != "tcp" && req.Protocol != "udp" {
		writeError(w, http.StatusBadRequest, "protocol must be 'tcp' or 'udp'")
		return
	}
	if req.LocalPort < 1 || req.LocalPort > 65535 {
		writeError(w, http.StatusBadRequest, "local_port must be 1-65535")
		return
	}
	if req.PublicPort < 1 || req.PublicPort > 65535 {
		writeError(w, http.StatusBadRequest, "public_port must be 1-65535")
		return
	}
	if req.Name == "" {
		req.Name = req.LocalHost
	}

	if err := s.Tunnels.ProbePublicPort(req.Protocol, req.PublicPort); err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("public port %d is already in use on this server (%s) — pick a different port", req.PublicPort, err))
		return
	}

	t, err := s.DB.CreateTunnel(req.ClientID, req.Name, req.LocalHost, req.LocalPort, req.PublicPort, req.Protocol)
	if err != nil {
		writeError(w, http.StatusConflict, "public_port is already in use by another tunnel")
		return
	}
	writeJSON(w, http.StatusCreated, toTunnelView(t))
}

type updateTunnelRequest struct {
	Enabled *bool `json:"enabled"`
}

func (s *Server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateTunnelRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	if *req.Enabled {
		t, err := s.DB.GetTunnelByID(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "tunnel not found")
			return
		}
		if err := s.Tunnels.ProbePublicPort(t.Protocol, t.PublicPort); err != nil {
			writeError(w, http.StatusConflict, fmt.Sprintf("public port %d is already in use on this server (%s) — pick a different port", t.PublicPort, err))
			return
		}
	}
	if err := s.DB.SetTunnelEnabled(id, *req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateTunnelSettingsRequest struct {
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
	PublicHostname    string `json:"public_hostname"`
}

// handleUpdateTunnelSettings sets a tunnel's traffic limit and public
// hostname — separate from handleUpdateTunnel's enabled toggle since these
// are always saved together as one "settings" form, not partial patches.
func (s *Server) handleUpdateTunnelSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req updateTunnelSettingsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TrafficLimitBytes != nil && *req.TrafficLimitBytes <= 0 {
		writeError(w, http.StatusBadRequest, "traffic_limit_bytes must be positive, or omitted/null for unlimited")
		return
	}
	req.PublicHostname = strings.TrimSpace(strings.ToLower(req.PublicHostname))
	if len(req.PublicHostname) > 253 {
		writeError(w, http.StatusBadRequest, "public_hostname is too long")
		return
	}

	if err := s.DB.UpdateTunnelSettings(id, req.TrafficLimitBytes, req.PublicHostname); err != nil {
		writeError(w, http.StatusNotFound, "tunnel not found")
		return
	}

	t, err := s.DB.GetTunnelByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toTunnelView(t))
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.DB.DeleteTunnel(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
