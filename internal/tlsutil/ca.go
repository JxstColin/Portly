// Package tlsutil manages Portly's self-signed CA and server certificate,
// and provides fingerprint-pinned TLS dialing for clients.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile     = "ca-cert.pem"
	caKeyFile      = "ca-key.pem"
	serverCertFile = "server-cert.pem"
	serverKeyFile  = "server-key.pem"
	certValidity   = 10 * 365 * 24 * time.Hour
)

// EnsureServerCert loads the CA + server certificate from dataDir, generating
// them on first run. It returns a tls.Certificate ready for use in a
// tls.Config, plus the SHA-256 fingerprint of the leaf server certificate
// that clients pin against.
func EnsureServerCert(dataDir string, hosts []string) (tls.Certificate, string, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create data dir: %w", err)
	}

	caCertPath := filepath.Join(dataDir, caCertFile)
	caKeyPath := filepath.Join(dataDir, caKeyFile)
	serverCertPath := filepath.Join(dataDir, serverCertFile)
	serverKeyPath := filepath.Join(dataDir, serverKeyFile)

	if !fileExists(caCertPath) || !fileExists(caKeyPath) {
		if err := generateCA(caCertPath, caKeyPath); err != nil {
			return tls.Certificate{}, "", fmt.Errorf("generate CA: %w", err)
		}
	}

	if !fileExists(serverCertPath) || !fileExists(serverKeyPath) {
		if err := generateServerCert(caCertPath, caKeyPath, serverCertPath, serverKeyPath, hosts); err != nil {
			return tls.Certificate{}, "", fmt.Errorf("generate server cert: %w", err)
		}
	}

	cert, err := tls.LoadX509KeyPair(serverCertPath, serverKeyPath)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("load server cert: %w", err)
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("parse server cert: %w", err)
	}

	return cert, Fingerprint(leaf.Raw), nil
}

// Fingerprint returns the hex-encoded SHA-256 fingerprint of a raw DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func generateCA(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Portly Root CA", Organization: []string{"Portly"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}

	return writeKeyPair(certPath, keyPath, der, key)
}

func generateServerCert(caCertPath, caKeyPath, certPath, keyPath string, hosts []string) error {
	caCertPEM, err := os.ReadFile(caCertPath)
	if err != nil {
		return err
	}
	caKeyPEM, err := os.ReadFile(caKeyPath)
	if err != nil {
		return err
	}

	caCertBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return err
	}
	caKeyBlock, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParseECPrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Portly Server", Organization: []string{"Portly"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}

	return writeKeyPair(certPath, keyPath, der, key)
}

func writeKeyPair(certPath, keyPath string, der []byte, key *ecdsa.PrivateKey) error {
	certOut, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		return err
	}

	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
