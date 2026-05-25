package serve

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestModernTLS_MinVersionIs13 (security audit L1) pins the protocol
// floor at TLS 1.3. Pre-fix, the server accepted 1.2 — leaving the
// door open to downgrade-attack research the broader ecosystem moved
// past in 2018.
func TestModernTLS_MinVersionIs13(t *testing.T) {
	cfg := modernTLS()
	if cfg.MinVersion != tls.VersionTLS13 {
		t.Errorf("modernTLS MinVersion=%x want %x (TLS 1.3)", cfg.MinVersion, tls.VersionTLS13)
	}
}

// TestServeTLS_RefusesTLS12 stands up a real httptest TLS server using
// the modernTLS config and asserts a client that explicitly insists on
// TLS 1.2 cannot complete the handshake. This is the end-to-end
// downgrade-rejection proof.
func TestServeTLS_RefusesTLS12(t *testing.T) {
	root := initProject(t)
	handler, err := Handler(Config{Root: root, Bind: "127.0.0.1", Port: 8443, Insecure: true})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	ts := httptest.NewUnstartedServer(handler)
	ts.TLS = modernTLS()
	ts.StartTLS()
	defer ts.Close()

	// Force the client to only offer TLS 1.2 so the server's MinVersion
	// floor is the only knob that can refuse.
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			MaxVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true,
		},
	}
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	if _, err := client.Get(ts.URL + "/health"); err == nil {
		t.Fatal("server accepted a TLS 1.2 handshake — the audit L1 floor was breached")
	}
	// Sanity: a TLS 1.3 client must still succeed against the same
	// server (proves we didn't break the floor by accidentally
	// rejecting everything).
	tr13 := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		},
	}
	client13 := &http.Client{Transport: tr13, Timeout: 2 * time.Second}
	if _, err := client13.Get(ts.URL + "/health"); err != nil {
		t.Fatalf("TLS 1.3 client should still connect: %v", err)
	}
}
