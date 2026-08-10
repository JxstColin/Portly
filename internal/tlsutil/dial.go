package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// DialPinned opens a TLS connection to addr, trusting the server certificate
// only if its SHA-256 fingerprint matches expectedFingerprint. This avoids
// requiring a real CA trust chain for a self-hosted, self-signed setup.
func DialPinned(addr, expectedFingerprint string) (*tls.Conn, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, // we verify manually via fingerprint below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("no peer certificate presented")
			}
			got := Fingerprint(cs.PeerCertificates[0].Raw)
			if got != expectedFingerprint {
				return fmt.Errorf("server certificate fingerprint mismatch: got %s, want %s", got, expectedFingerprint)
			}
			return nil
		},
	}

	conn, err := tls.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// LeafFingerprint extracts the SHA-256 fingerprint from a PEM-encoded certificate.
func LeafFingerprint(cert *x509.Certificate) string {
	return Fingerprint(cert.Raw)
}
