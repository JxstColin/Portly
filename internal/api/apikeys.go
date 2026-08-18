package api

import (
	"net/http"
	"strings"

	"github.com/jxstcolin/portly/internal/db"
)

// API keys let an external service (e.g. a game panel) manage clients and
// tunnels without an admin session — see requireAuthOrAPIKey in server.go.
// Only session-authenticated admins can create/list/revoke keys themselves;
// a key can never be used to mint another key.

type apiKeyView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  *int64 `json:"last_used,omitempty"`
}

func toAPIKeyView(k db.ApiKey) apiKeyView {
	v := apiKeyView{ID: k.ID, Name: k.Name, CreatedAt: k.CreatedAt.Unix()}
	if k.LastUsed != nil {
		ts := k.LastUsed.Unix()
		v.LastUsed = &ts
	}
	return v
}

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.DB.ListAPIKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]apiKeyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, toAPIKeyView(k))
	}
	writeJSON(w, http.StatusOK, out)
}

type createAPIKeyRequest struct {
	Name string `json:"name"`
}

// handleCreateAPIKey returns the plaintext token exactly once, in this
// response — only its hash is ever persisted, same as client tokens.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req createAPIKeyRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name must be 64 characters or fewer")
		return
	}

	key, token, err := s.DB.CreateAPIKey(req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"api_key": toAPIKeyView(key),
		"token":   token,
	})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.DB.DeleteAPIKey(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
