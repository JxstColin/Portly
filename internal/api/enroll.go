package api

import (
	"fmt"
	"net/http"
	"strings"
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

# Download to a temp file in the same directory as the target, then rename
# into place. A running portly-client keeps its old binary mapped even
# after this replaces the directory entry, so re-running the installer
# (e.g. to rotate a machine's credentials) never fails trying to overwrite
# an in-use executable.
echo "Downloading portly-client for ${OS}/${ARCH}..."
TMP_BIN="$(mktemp /usr/local/bin/portly-client.XXXXXX)"
curl -fsSL -o "${TMP_BIN}" "${BASE}/downloads/${OS}-${ARCH}"
chmod +x "${TMP_BIN}"
systemctl stop portly-client 2>/dev/null || true
mv -f "${TMP_BIN}" /usr/local/bin/portly-client

echo "Enrolling with ${BASE}..."
exec /usr/local/bin/portly-client enroll --api "${BASE}" --code "${CODE}"
`

func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code query parameter", http.StatusBadRequest)
		return
	}

	if !looksLikeShellFetch(r) {
		s.writeInstallBlockedPage(w, code)
		return
	}

	script := fmt.Sprintf(installScriptTemplate, s.apiBaseURL(), code)
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	_, _ = w.Write([]byte(script))
}

// looksLikeShellFetch is a soft heuristic distinguishing 'curl | bash' from
// someone opening the link in a browser — not a security boundary (the
// script itself has no secret in it; the enrollment code is single-use and
// only does anything via the separate exchange endpoint), just so casually
// visiting/sharing the link shows a helpful page instead of raw bash.
func looksLikeShellFetch(r *http.Request) bool {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	for _, prefix := range []string{"curl/", "wget/"} {
		if strings.HasPrefix(ua, prefix) {
			return true
		}
	}
	return false
}

const installBlockedPageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Portly installer</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
    background: #f9f9f7; color: #0b0b0b;
  }
  @media (prefers-color-scheme: dark) {
    body { background: #0d0d0d; color: #fff; }
    .card { background: #1a1a19 !important; border-color: rgba(255,255,255,.1) !important; }
    code { background: #212120 !important; color: #fff; }
  }
  .card {
    max-width: 30rem; margin: 1.5rem; padding: 2rem;
    border: 1px solid rgba(11,11,11,.1); border-radius: 12px; background: #fcfcfb;
  }
  h1 { font-size: 1.15rem; margin: 0 0 .5rem; }
  p { color: #52514e; line-height: 1.5; font-size: .95rem; }
  code {
    display: block; background: #f0efec; padding: .75rem 1rem; border-radius: 8px;
    margin-top: 1rem; font-size: .8rem; overflow-x: auto; white-space: pre-wrap; word-break: break-all;
  }
</style>
</head>
<body>
  <div class="card">
    <h1>This is an installer, not a web page</h1>
    <p>This link is meant to be piped straight into a shell on the machine you're
    setting up, not opened in a browser. Run it there instead:</p>
    <code>curl -fsSL '%s' | sudo bash</code>
  </div>
</body>
</html>
`

func (s *Server) writeInstallBlockedPage(w http.ResponseWriter, code string) {
	url := fmt.Sprintf("%s/install.sh?code=%s", s.apiBaseURL(), code)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, installBlockedPageTemplate, url)
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
