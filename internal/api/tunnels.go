package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
	ProxyProtocol     bool   `json:"proxy_protocol"`
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
		ProxyProtocol:     t.ProxyProtocol,
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

// portSpec accepts either a bare JSON number (25565) or a "start-end" range
// string ("25565-25574") for local_port/public_port — a range expands into
// one tunnel per port pair, so a whole contiguous block (e.g. an FTP
// passive-mode port range) can be tunneled in a single request instead of
// one per port.
type portSpec struct {
	values []int
}

func (p *portSpec) UnmarshalJSON(data []byte) error {
	var n int
	if err := json.Unmarshal(data, &n); err == nil {
		p.values = []int{n}
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("must be a number or a \"start-end\" range string")
	}
	s = strings.TrimSpace(s)

	before, after, isRange := strings.Cut(s, "-")
	if !isRange {
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("invalid port value %q", s)
		}
		p.values = []int{n}
		return nil
	}

	start, err1 := strconv.Atoi(strings.TrimSpace(before))
	end, err2 := strconv.Atoi(strings.TrimSpace(after))
	if err1 != nil || err2 != nil || start > end {
		return fmt.Errorf("invalid port range %q", s)
	}
	values := make([]int, 0, end-start+1)
	for v := start; v <= end; v++ {
		values = append(values, v)
	}
	p.values = values
	return nil
}

type createTunnelRequest struct {
	ClientID   string   `json:"client_id"`
	Name       string   `json:"name"`
	Protocol   string   `json:"protocol"`
	LocalHost  string   `json:"local_host"`
	LocalPort  portSpec `json:"local_port"`
	PublicPort portSpec `json:"public_port"`
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

	localPorts := req.LocalPort.values
	publicPorts := req.PublicPort.values
	isRange := len(localPorts) > 1 || len(publicPorts) > 1
	if isRange && len(localPorts) != len(publicPorts) {
		writeError(w, http.StatusBadRequest, "local_port and public_port ranges must be the same length")
		return
	}

	for _, p := range localPorts {
		if p < 1 || p > 65535 {
			writeError(w, http.StatusBadRequest, "local_port must be 1-65535")
			return
		}
	}
	for _, p := range publicPorts {
		// 0 means "no dedicated public port" — the tunnel is only reachable
		// via the shared Minecraft hostname router (set public_hostname to
		// route to it), not its own listener. Doesn't make sense inside a
		// range, since every port in it would collide on the same 0.
		if p != 0 && (p < 1 || p > 65535) {
			writeError(w, http.StatusBadRequest, "public_port must be 1-65535, or 0 for hostname-only routing")
			return
		}
		if isRange && p == 0 {
			writeError(w, http.StatusBadRequest, "public_port 0 (hostname-only routing) isn't valid inside a range")
			return
		}
	}

	baseName := req.Name
	if baseName == "" {
		baseName = req.LocalHost
	}

	created := make([]tunnelView, 0, len(localPorts))
	rollback := func() {
		for _, c := range created {
			_ = s.DB.DeleteTunnel(c.ID)
		}
	}

	for i, lp := range localPorts {
		pp := publicPorts[0]
		if len(publicPorts) > 1 {
			pp = publicPorts[i]
		}
		name := baseName
		if isRange {
			name = fmt.Sprintf("%s-%d", baseName, pp)
		}

		if pp != 0 {
			if err := s.Tunnels.ProbePublicPort(req.Protocol, pp); err != nil {
				rollback()
				writeError(w, http.StatusConflict, fmt.Sprintf("public port %d is already in use on this server (%s) — pick a different port", pp, err))
				return
			}
		}

		t, err := s.DB.CreateTunnel(req.ClientID, name, req.LocalHost, lp, pp, req.Protocol)
		if err != nil {
			rollback()
			writeError(w, http.StatusConflict, "public_port is already in use by another tunnel")
			return
		}
		created = append(created, toTunnelView(t))
	}

	if isRange {
		writeJSON(w, http.StatusCreated, created)
	} else {
		writeJSON(w, http.StatusCreated, created[0])
	}
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
		if t.PublicPort != 0 {
			if err := s.Tunnels.ProbePublicPort(t.Protocol, t.PublicPort); err != nil {
				writeError(w, http.StatusConflict, fmt.Sprintf("public port %d is already in use on this server (%s) — pick a different port", t.PublicPort, err))
				return
			}
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
	ProxyProtocol     bool   `json:"proxy_protocol"`
}

// handleUpdateTunnelSettings sets a tunnel's traffic limit, public
// hostname, and PROXY protocol toggle — separate from handleUpdateTunnel's
// enabled toggle since these are always saved together as one "settings"
// form, not partial patches.
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

	// public_hostname now drives real routing (see RunMinecraftRouter), so
	// a collision isn't just cosmetic anymore — reject it clearly instead
	// of silently letting two tunnels shadow each other.
	if req.PublicHostname != "" {
		if taken, err := s.DB.HostnameTaken(req.PublicHostname, id); err == nil && taken {
			writeError(w, http.StatusConflict, fmt.Sprintf("hostname %q is already in use by another tunnel", req.PublicHostname))
			return
		}
	}

	if err := s.DB.UpdateTunnelSettings(id, req.TrafficLimitBytes, req.PublicHostname, req.ProxyProtocol); err != nil {
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
