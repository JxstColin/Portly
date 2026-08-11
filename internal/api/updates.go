package api

import (
	"net/http"

	"github.com/jxstcolin/portly/internal/updatecheck"
)

type updateStatusResponse struct {
	updatecheck.Status
	// CanApply reports whether ApplyUpdate is wired up at all (i.e. the
	// one-click-update sudo grant was detected at startup) — the UI uses
	// this to show either an "Update now" button or manual instructions.
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
		writeError(w, http.StatusNotImplemented, "one-click update isn't enabled on this server — see the README for how to enable it, or update manually")
		return
	}
	if err := s.ApplyUpdate(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
