package api

import (
	"fmt"
	"net/http"
)

// installScriptTemplate is filled in with the API's own base URL and the
// one-time enrollment code, then served as-is. It intentionally does the
// least possible in bash: detect the platform, fetch the matching prebuilt
// binary, and hand off to 'portly-client enroll', which does the actual
// auth exchange, config writing, and service setup in Go.
const installScriptTemplate = `#!/usr/bin/env bash
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Please run this as root, e.g.: curl -fsSL '%[1]s/install.sh?code=%[2]s' | sudo bash" >&2
  exit 1
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

BASE="%[1]s"
CODE="%[2]s"

echo "Downloading portly-client for ${OS}/${ARCH}..."
curl -fsSL -o /usr/local/bin/portly-client "${BASE}/downloads/${OS}-${ARCH}"
chmod +x /usr/local/bin/portly-client

echo "Enrolling with ${BASE}..."
exec /usr/local/bin/portly-client enroll --api "${BASE}" --code "${CODE}"
`

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code query parameter", http.StatusBadRequest)
		return
	}
	script := fmt.Sprintf(installScriptTemplate, s.apiBaseURL(), code)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(script))
}

func (s *Server) handleDownloadClient(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("osarch")
	bin, ok := s.ClientBinaries[key]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no prebuilt portly-client for %s", key))
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=portly-client")
	_, _ = w.Write(bin)
}

type enrollExchangeRequest struct {
	Code string `json:"code"`
}

// handleEnrollExchange is intentionally unauthenticated (no session cookie
// exists yet on the enrolling machine) — its only protection is the
// short-lived, single-use enrollment code itself.
func (s *Server) handleEnrollExchange(w http.ResponseWriter, r *http.Request) {
	var req enrollExchangeRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	client, token, err := s.DB.ExchangeEnrollmentCode(req.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":           client.Name,
		"token":          token,
		"control_addr":   fmt.Sprintf("%s:%d", s.AdvertiseHost, s.ControlPort),
		"ca_fingerprint": s.CAFingerprint,
	})
}
