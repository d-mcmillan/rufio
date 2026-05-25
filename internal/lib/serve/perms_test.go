package serve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestServeTLS_RefusesWorldReadableKey (security audit L5) pins the
// key-file permissions gate: a 0644 (or worse) TLS key file must
// refuse the server's startup. The operator's audit-event-prone
// mistake (chmod 644 key.pem) gets a loud error pointing at the fix.
//
// Skipped on Windows where Unix permission bits aren't meaningful.
func TestServeTLS_RefusesWorldReadableKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics not applicable on Windows")
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if err := writeSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatalf("writeSelfSignedCert: %v", err)
	}
	// Force the key to 0644 — the foot-gun the test pins.
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	// Validate the gate fires BEFORE listen. Use a synchronous helper
	// — we don't want Run to actually bind a port.
	err := checkTLSKeyPerms(keyPath)
	if err == nil {
		t.Fatal("checkTLSKeyPerms accepted 0644 key — must refuse")
	}
	if !strings.Contains(err.Error(), "permissions too open") {
		t.Errorf("error should mention 'permissions too open'; got %v", err)
	}

	// And the safe 0600 form must pass.
	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatalf("chmod 0600: %v", err)
	}
	if err := checkTLSKeyPerms(keyPath); err != nil {
		t.Errorf("checkTLSKeyPerms(0600) should pass; got %v", err)
	}
	// 0400 also OK (read-only owner).
	if err := os.Chmod(keyPath, 0o400); err != nil {
		t.Fatalf("chmod 0400: %v", err)
	}
	if err := checkTLSKeyPerms(keyPath); err != nil {
		t.Errorf("checkTLSKeyPerms(0400) should pass; got %v", err)
	}
}

// TestCheckTLSKeyPerms_GroupReadableRefused covers the 0640 case (group
// can read) — a common docker/k8s mistake that still leaks to anyone
// who lands in the right group.
func TestCheckTLSKeyPerms_GroupReadableRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission semantics not applicable on Windows")
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-key"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := checkTLSKeyPerms(keyPath); err == nil {
		t.Error("checkTLSKeyPerms accepted 0640 key — must refuse (group can read)")
	}
}

// TestCheckTLSKeyPerms_MissingFile reports a clear error rather than
// silently passing.
func TestCheckTLSKeyPerms_MissingFile(t *testing.T) {
	err := checkTLSKeyPerms("/nonexistent/path/to/key.pem")
	if err == nil {
		t.Fatal("checkTLSKeyPerms should error on missing file")
	}
	if !strings.Contains(err.Error(), "cannot stat") {
		t.Errorf("error should mention 'cannot stat'; got %v", err)
	}
}

// writeSelfSignedCert generates a throwaway ECDSA self-signed cert
// pair for the perm-check test. P-256 is the modern default; we don't
// need long-term security here (cert lives in t.TempDir).
func writeSelfSignedCert(certPath, keyPath string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
