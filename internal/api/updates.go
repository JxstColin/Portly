package api

import (
	"net/http"

	"github.com/jxstcolin/portly/internal/updatecheck"
)

type updateStatusResponse struct {
	updatecheck.Status
	// CanApply reports whether ApplyUpdate is wired up at all. cmd/portly-server
	// always wires it up; this only ever comes back false for an embedder that
	// doesn't set Server.ApplyUpdate at all (e.g. a dev/test build) — the
	// actual sudo-grant check happens per-attempt inside ApplyUpdate itself,
	// so a missing grant surfaces as a specific error from clicking "Update
	// now" rather than the button not being shown.
	CanApply bool `json:"can_apply"`
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, updateStatusResponse{
		Status:   s.getUpdateStatus(),
		CanApply: s.ApplyUpdate != nil,
	})
}

// handleCheckUpdate runs an on-demand check (rather than waiting for the
// periodic background one) so a "Check now" click gets immediate feedback.
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	status := updatecheck.Check(r.Context(), s.BuildCommit)
	s.SetUpdateStatus(status)
	writeJSON(w, http.StatusOK, updateStatusResponse{
		Status:   status,
		CanApply: s.ApplyUpdate != nil,
	})
}

// handleApplyUpdate triggers the real update process (git pull, rebuild,
// service restart) as a detached background process — it responds once
// that process has started, not once it's finished, since this server
// itself gets restarted partway through and won't be around to answer.
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.ApplyUpdate == nil {
		writeError(w, http.StatusNotImplemented, "one-click update isn't wired up on this server build — update manually instead")
		return
	}
	if err := s.ApplyUpdate(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
