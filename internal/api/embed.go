package api

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

//go:embed clientbins
var clientBinsFS embed.FS

const clientBinPrefix = "clientbins/portly-client-"

// LoadEmbeddedClientBinaries reads whatever prebuilt portly-client binaries
// were cross-compiled into internal/api/clientbins at build time (see
// `make build-clientbins`) and returns them keyed by "<os>-<arch>", ready to
// hand to Server.ClientBinaries.
func LoadEmbeddedClientBinaries() (map[string][]byte, error) {
	out := make(map[string][]byte)

	entries, err := clientBinsFS.ReadDir("clientbins")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := "clientbins/" + e.Name()
		if !strings.HasPrefix(name, clientBinPrefix) {
			continue
		}
		data, err := clientBinsFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		key := strings.TrimPrefix(name, clientBinPrefix)
		out[key] = data
	}
	return out, nil
}

// ChecksumClientBinaries hashes each embedded portly-client binary so
// running clients can cheaply poll for an update (GET the hash instead of
// the whole binary) and know when to fetch a new one — see
// Server.handleDownloadClientChecksum.
func ChecksumClientBinaries(bins map[string][]byte) map[string]string {
	out := make(map[string]string, len(bins))
	for key, data := range bins {
		sum := sha256.Sum256(data)
		out[key] = hex.EncodeToString(sum[:])
	}
	return out
}
