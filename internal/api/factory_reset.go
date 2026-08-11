package api

import "net/http"

const factoryResetConfirmPhrase = "RESET"

type factoryResetRequest struct {
	Confirm string `json:"confirm"`
}

// handleFactoryReset wipes every client, tunnel, and the admin account
// back to a fresh-install state — connected machines are told to
// uninstall themselves first, then every session (including the one
// making this request) is invalidated, since the account it belongs to no
// longer exists. main.go's OnFactoryReset hook (if set) takes it from
// there, generating a new setup code exactly like a brand new install.
func (s *Server) handleFactoryReset(w http.ResponseWriter, r *http.Request) {
	var req factoryResetRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Confirm != factoryResetConfirmPhrase {
		writeError(w, http.StatusBadRequest, `type "RESET" to confirm`)
		return
	}

	clients, err := s.DB.ListClients()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range clients {
		s.Tunnels.PushUninstall(c.ID) // best-effort; offline ones learn via revoked_tokens instead
	}

	if err := s.DB.FactoryReset(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.setDomain("")
	s.SetCertState("", "")
	s.clearAllSessions()

	if s.OnFactoryReset != nil {
		s.OnFactoryReset()
	}

	w.WriteHeader(http.StatusNoContent)
}
