package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/meigma/mock-oidc/internal/config"
)

// TLS termination is adapter-tier concern, so the key-bearing crypto packages
// (crypto/rsa, crypto/x509, crypto/tls via the server) live here in internal/app
// and NEVER in internal/oidc — the core stays transport/key-crypto free, enforced
// by the oidc-core depguard rule and arch_test's TestCoreImportsAreClean.

const (
	// selfSignedValidity is how long a generated localhost certificate is valid.
	// It is generous because the certificate is regenerated on every boot and is
	// only ever trusted by a test client (curl -k / InsecureSkipVerify).
	selfSignedValidity = 365 * 24 * time.Hour
	// rsaKeyBits is the modulus size of the generated self-signed key.
	rsaKeyBits = 2048
	// serialBits bounds the random certificate serial number.
	serialBits = 128
)

// resolveTLS returns the cert/key paths the API listener will use. TLS off (not
// enabled and no explicit cert) → both empty (plain HTTP). An explicit
// TLSCertFile → those files verbatim. TLS enabled with no files (the upstream
// ssl:{} path) → a freshly generated self-signed localhost certificate written
// to a temp PEM pair, so listen() always finds files when the cert path is set.
func resolveTLS(cfg config.Config) (string, string, error) {
	if !cfg.TLSEnabled && cfg.TLSCertFile == "" {
		return "", "", nil
	}
	if cfg.TLSCertFile != "" {
		return cfg.TLSCertFile, cfg.TLSKeyFile, nil
	}
	return generateSelfSignedLocalhost()
}

// generateSelfSignedLocalhost creates an RSA self-signed certificate for
// CN=localhost with SANs localhost, 127.0.0.1, and ::1, writes the cert and key
// to a temp PEM pair, and returns their paths. It backs the zero-cert ssl:{}
// path so a for-testing HTTPS run needs no operator-supplied certificate.
func generateSelfSignedLocalhost() (string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return "", "", fmt.Errorf("generate RSA key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return "", "", fmt.Errorf("generate serial number: %w", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	certFile, err := writeTempPEM("mock-oidc-cert-*.pem", certPEM)
	if err != nil {
		return "", "", err
	}
	keyFile, err := writeTempPEM("mock-oidc-key-*.pem", keyPEM)
	if err != nil {
		return "", "", err
	}
	return certFile, keyFile, nil
}

// writeTempPEM writes data to a new temp file matching pattern and returns its
// path. The 0600 default from CreateTemp keeps the private key unreadable by
// other users; the files live for the process lifetime (the listener reads them
// by path).
func writeTempPEM(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close %s: %w", f.Name(), err)
	}
	return f.Name(), nil
}
