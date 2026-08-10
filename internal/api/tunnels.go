package api

import (
	"net/http"
	"strings"

	"github.com/jxstcolin/portly/internal/db"
)

type tunnelView struct {
	ID                string `json:"id"`
	ClientID          string `json:"client_id"`
	Name              string `json:"name"`
	LocalHost         string `json:"local_host"`
	LocalPort         int    `json:"local_port"`
	PublicPort        int    `json:"public_port"`
	Enabled           bool   `json:"enabled"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes,omitempty"`
	BytesInTotal      int64  `json:"bytes_in_total"`
	BytesOutTotal     int64  `json:"bytes_out_total"`
	CreatedAt         int64  `json:"created_at"`
}

func toTunnelView(t db.Tunnel) tunnelView {
	return tunnelView{
		ID:                t.ID,
		ClientID:          t.ClientID,
		Name:              t.Name,
		LocalHost:         t.LocalHost,
		LocalPort:         t.LocalPort,
		PublicPort:        t.PublicPort,
		Enabled:           t.Enabled,
		TrafficLimitBytes: t.TrafficLimitBytes,
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

	if req.ClientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}
	if _, err := s.DB.GetClientByID(req.ClientID); err != nil {
		writeError(w, http.StatusNotFound, "client not found")
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

	t, err := s.DB.CreateTunnel(req.ClientID, req.Name, req.LocalHost, req.LocalPort, req.PublicPort)
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
	if err := s.DB.SetTunnelEnabled(id, *req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.DB.DeleteTunnel(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
