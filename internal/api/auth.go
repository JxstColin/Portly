package api

import (
	"net/http"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	admin, err := s.DB.GetAdminUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin account not found")
		return
	}

	if admin.Username != req.Username {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	token := s.createSession(admin.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"username":             admin.Username,
		"must_change_password": admin.MustChangePassword,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		s.destroySession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	admin, err := s.DB.GetAdminUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin account not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":             admin.Username,
		"must_change_password": admin.MustChangePassword,
	})
}

type changeCredentialsRequest struct {
	CurrentPassword string `json:"current_password"`
	NewUsername     string `json:"new_username"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangeCredentials(w http.ResponseWriter, r *http.Request) {
	var req changeCredentialsRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	admin, err := s.DB.GetAdminUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "admin account not found")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	newUsername := admin.Username
	if req.NewUsername != "" {
		if utf8.RuneCountInString(req.NewUsername) < 3 {
			writeError(w, http.StatusBadRequest, "username must be at least 3 characters")
			return
		}
		newUsername = req.NewUsername
	}

	if utf8.RuneCountInString(req.NewPassword) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	if err := s.DB.UpdateAdminCredentials(admin.ID, newUsername, string(hash), false); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"username": newUsername, "must_change_password": false})
}

// handleBootstrapStatus is unauthenticated (there's nothing to authenticate
// against yet on a fresh install) — the web UI calls it before rendering
// the login page to decide whether to show the setup-code bootstrap flow
// instead.
func (s *Server) handleBootstrapStatus(w http.ResponseWriter, r *http.Request) {
	hasAdmin, err := s.DB.HasAdminUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": !hasAdmin})
}

type bootstrapClaimRequest struct {
	SetupCode string `json:"setup_code"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

// handleBootstrapClaim creates the first (and only) admin account, gated by
// the one-time setup code portly-server printed/wrote out on first run —
// replacing the old seeded admin/portly-with-forced-change flow, which
// meant anyone who found the panel before the real admin logged in once
// could log in with a publicly-known default password.
func (s *Server) handleBootstrapClaim(w http.ResponseWriter, r *http.Request) {
	var req bootstrapClaimRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	hasAdmin, err := s.DB.HasAdminUser()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if hasAdmin {
		writeError(w, http.StatusConflict, "setup already completed")
		return
	}

	if err := s.DB.ClaimSetupCode(req.SetupCode); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid or already-used setup code")
		return
	}

	if utf8.RuneCountInString(req.Username) < 3 {
		writeError(w, http.StatusBadRequest, "username must be at least 3 characters")
		return
	}
	if utf8.RuneCountInString(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}
	admin, err := s.DB.CreateAdminUser(req.Username, string(hash))
	if err != nil {
		writeError(w, http.StatusConflict, "could not create admin account")
		return
	}

	if s.OnAdminClaimed != nil {
		s.OnAdminClaimed()
	}

	token := s.createSession(admin.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"username":             admin.Username,
		"must_change_password": false,
	})
}

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"advertise_host": s.AdvertiseHost,
		"control_port":   s.ControlPort,
		"api_port":       s.APIPort,
		"ca_fingerprint": s.CAFingerprint,
	})
}
