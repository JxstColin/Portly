package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jxstcolin/portly/internal/updatecheck"
)

type updateStatusResponse struct {
	updatecheck.Status
	// CanApply reports whether ApplyUpdate is wired up at all. cmd/portly-server
	// always wires it up; this only ever comes back false for an embedder that
	// doesn't set Server.ApplyUpdate at all (e.g. a dev/test build) —
	// per-attempt failures (e.g. the updater unit not being installed yet)
	// surface as a specific error from clicking "Update now" instead, rather
	// than the button not being shown at all.
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

// UpdateProgress mirrors the JSON status file scripts/quickstart-vps.sh
// writes to <data-dir>/update-progress.json as it works through an update,
// plus a tail of <data-dir>/update.log (the script's own console output)
// read straight off disk. Both files are written by the script itself, not
// by portly-server — deliberately, since portly-server gets restarted
// partway through every update. Reading them fresh off disk on every
// request is what lets the "Portly is updating" screen keep working across
// that restart, and across a plain page refresh.
type UpdateProgress struct {
	Status      string `json:"status,omitempty"` // "running", "done", "failed", or "" (no update has ever run)
	Stage       int    `json:"stage,omitempty"`
	TotalStages int    `json:"total_stages,omitempty"`
	Label       string `json:"label,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	LogTail     string `json:"log_tail,omitempty"`
}

const updateLogTailBytes = 8 * 1024

// handleUpdateProgress is deliberately not behind requireAuth: sessions are
// held in memory (see Server.sessions), and an update restarts
// portly-server partway through, wiping them — an auth check here would
// bounce the admin to the login screen at exactly the moment they need to
// see the update screen instead. The response carries nothing sensitive,
// just how far the update script has gotten and its own console output.
func (s *Server) handleUpdateProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.readUpdateProgress())
}

func (s *Server) readUpdateProgress() UpdateProgress {
	var p UpdateProgress
	if s.DataDir == "" {
		return p
	}
	if data, err := os.ReadFile(filepath.Join(s.DataDir, "update-progress.json")); err == nil {
		_ = json.Unmarshal(data, &p)
	}
	p.LogTail = tailFile(filepath.Join(s.DataDir, "update.log"), updateLogTailBytes)
	return p
}

// tailFile returns up to the last n bytes of the file at path, or "" if it
// doesn't exist or can't be read.
func tailFile(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return ""
	}
	offset := int64(0)
	if info.Size() > n {
		offset = info.Size() - n
	}
	buf := make([]byte, info.Size()-offset)
	if _, err := f.ReadAt(buf, offset); err != nil {
		return ""
	}
	return string(buf)
}
