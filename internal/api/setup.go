package api

import (
	"net/http"
	"regexp"
	"strings"
)

var domainPattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

type setupStatusResponse struct {
	PublicIP    string `json:"public_ip"`
	ControlPort int    `json:"control_port"`
	Domain      string `json:"domain,omitempty"`
	CertState   string `json:"cert_state,omitempty"` // "", "pending", "ready", "error"
	CertError   string `json:"cert_error,omitempty"`
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	certState, certErr := s.getCertState()
	writeJSON(w, http.StatusOK, setupStatusResponse{
		PublicIP:    s.AdvertiseHost,
		ControlPort: s.ControlPort,
		Domain:      s.Domain(),
		CertState:   certState,
		CertError:   certErr,
	})
}

type setDomainRequest struct {
	Domain string `json:"domain"`
}

// handleSetDomain persists the admin's chosen public domain and (if
// OnDomainSet is wired up by main.go) kicks off Let's Encrypt certificate
// issuance for it. An empty domain clears it, reverting to IP-only access.
func (s *Server) handleSetDomain(w http.ResponseWriter, r *http.Request) {
	var req setDomainRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	domain := strings.ToLower(strings.TrimSpace(req.Domain))

	if domain != "" && !domainPattern.MatchString(domain) {
		writeError(w, http.StatusBadRequest, "that doesn't look like a valid domain, e.g. panel.example.com")
		return
	}

	if err := s.DB.SetSetting("domain", domain); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setDomain(domain)

	if domain == "" {
		s.SetCertState("", "")
	} else {
		s.SetCertState("pending", "")
		if s.OnDomainSet != nil {
			go s.OnDomainSet(domain)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"domain": domain})
}
