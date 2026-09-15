package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Snapshot copies the database on request.
func (a *App) Snapshot(ctx context.Context) (string, error) {
	v, err := a.Store.Version(ctx)
	if err != nil {
		return "", err
	}
	return a.Store.Snapshot(ctx, filepath.Join(a.cfg.DataDir, "snapshots"), v)
}

// tlsFiles returns the certificate and key paths.
func (a *App) tlsFiles() (cert, key string) {
	dir := filepath.Join(a.cfg.DataDir, "tls")
	return filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
}

// ServeTLS reports whether the service serves TLS: HTTPS is switched on
// and a valid pair is stored. The session cookie is Secure only then.
func (a *App) ServeTLS() bool {
	if !a.Settings().HTTPSEnabled {
		return false
	}
	_, err := a.LoadTLS()
	return err == nil
}

// LoadTLS returns the stored certificate and key. The TLS listener calls
// it at each handshake, so a new pair applies without a restart. The pair
// is parsed again only when a file changed.
func (a *App) LoadTLS() (*tls.Certificate, error) {
	cert, key := a.tlsFiles()
	ci, err := os.Stat(cert)
	if err != nil {
		return nil, err
	}
	ki, err := os.Stat(key)
	if err != nil {
		return nil, err
	}
	stamp := ci.ModTime().String() + ki.ModTime().String()
	a.tlsMu.Lock()
	defer a.tlsMu.Unlock()
	if a.tlsCert != nil && a.tlsStamp == stamp {
		return a.tlsCert, nil
	}
	c, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		return nil, err
	}
	a.tlsCert, a.tlsStamp = &c, stamp
	return &c, nil
}

// StoreTLS validates a certificate and key pair and writes it to the
// data directory.
func (a *App) StoreTLS(certPEM, keyPEM string) error {
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("the certificate and key do not match or do not parse: %w", err)
	}
	return a.writeTLS([]byte(certPEM), []byte(keyPEM))
}

// writeTLS stores the pair. Each file is renamed into place, so a
// handshake never reads a half-written file.
func (a *App) writeTLS(certPEM, keyPEM []byte) error {
	cert, key := a.tlsFiles()
	if err := os.MkdirAll(filepath.Dir(cert), 0o700); err != nil {
		return err
	}
	if err := replaceFile(key, keyPEM, 0o600); err != nil {
		return err
	}
	return replaceFile(cert, certPEM, 0o644)
}

func replaceFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SelfSignedTLS generates a certificate for this host's name and
// addresses, valid ten years, and stores it.
func (a *App) SelfSignedTLS() error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "jukem"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host, Organization: []string{"jukem"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host, "localhost"},
		IPAddresses:           localAddresses(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return a.writeTLS(certPEM, keyPEM)
}

// localAddresses lists the host's unicast addresses, for the
// certificate.
func localAddresses() []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1")}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, addr := range addrs {
		if n, ok := addr.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// RequestRestart stops the service cleanly. The supervisor, or Docker,
// starts it again.
func (a *App) RequestRestart() error {
	if a.Restart == nil {
		return errors.New("restart is not available")
	}
	a.log.Warn("restart requested from the UI")
	a.Restart()
	return nil
}
