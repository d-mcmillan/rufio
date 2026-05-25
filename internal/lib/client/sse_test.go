package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConsumeSSE_DecodesIdEventDataTriples drives the consumer against a
// canned SSE stream and asserts each id/event/data block is delivered
// as a single SSEEvent. Heartbeats are surfaced via IsComment so the
// caller can filter without re-parsing.
func TestConsumeSSE_DecodesIdEventDataTriples(t *testing.T) {
	body := strings.Join([]string{
		"id: 1",
		"event: thought",
		`data: {"id":"t-1"}`,
		"",
		"id: 2",
		"event: confirm",
		`data: {"id":"c-1"}`,
		"",
		": heartbeat 2026-05-22T00:00:00Z",
		"",
	}, "\n")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer rufio_test" {
			t.Errorf("expected Bearer token, got %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("expected Accept text/event-stream, got %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
		w.(http.Flusher).Flush()
	}))
	defer ts.Close()

	// httptest server is http://, so we must opt into InsecureTLS and
	// the host must be loopback (httptest binds 127.0.0.1).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got []SSEEvent
	var mu sync.Mutex
	err := ConsumeSSE(ctx, SSEOptions{
		Endpoint:    ts.URL,
		Token:       "rufio_test",
		InsecureTLS: true,
	}, func(ev SSEEvent) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	})
	// io.EOF surfaces as nil from ConsumeSSE; the stream closed cleanly.
	if err != nil {
		t.Fatalf("ConsumeSSE: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events (2 data + 1 comment), got %d: %+v", len(got), got)
	}
	if got[0].ID != "1" || got[0].Event != "thought" || got[0].Data != `{"id":"t-1"}` {
		t.Errorf("event[0]: %+v", got[0])
	}
	if got[1].ID != "2" || got[1].Event != "confirm" || got[1].Data != `{"id":"c-1"}` {
		t.Errorf("event[1]: %+v", got[1])
	}
	if !got[2].IsComment || !strings.Contains(got[2].Comment, "heartbeat") {
		t.Errorf("event[2] (heartbeat): %+v", got[2])
	}
}

// TestConsumeSSE_VerifiesTLSByDefault is the SDK security floor: the
// SSE consumer MUST reject plaintext http:// unless InsecureTLS=true
// AND the host is loopback. Mirrors the Dial scheme gate.
func TestConsumeSSE_VerifiesTLSByDefault(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := ConsumeSSE(ctx, SSEOptions{
		Endpoint: "http://evil.example.com/listen",
		Token:    "rufio_test",
	}, func(SSEEvent) error { return nil })
	if err == nil {
		t.Fatal("expected ConsumeSSE to refuse plaintext http:// without InsecureTLS")
	}
	if !strings.Contains(err.Error(), "https") && !strings.Contains(err.Error(), "plaintext") {
		t.Errorf("error should mention scheme/plaintext; got %v", err)
	}
}

// TestConsumeSSE_RejectsNon200 surfaces server-side status errors via
// SSEStatusError so the caller can branch on auth (401) vs transient
// (5xx) failures.
func TestConsumeSSE_RejectsNon200(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := ConsumeSSE(ctx, SSEOptions{
		Endpoint:    ts.URL,
		Token:       "rufio_test",
		InsecureTLS: true,
	}, func(SSEEvent) error { return nil })
	if err == nil {
		t.Fatal("expected non-200 to surface an error")
	}
	var se *SSEStatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected SSEStatusError, got %T: %v", err, err)
	}
	if se.Status != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", se.Status)
	}
}

// TestBuildListenURL covers the URL rewrite rules — accepts a bare host,
// a host+/mcp suffix (the existing mirror sync pattern), and a trailing
// slash. All three normalise to <base>/listen.
func TestBuildListenURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://r.example.com:8443", "https://r.example.com:8443/listen"},
		{"https://r.example.com:8443/", "https://r.example.com:8443/listen"},
		{"https://r.example.com:8443/mcp", "https://r.example.com:8443/listen"},
		{"https://r.example.com:8443/mcp/", "https://r.example.com:8443/listen"},
	}
	for _, c := range cases {
		got := BuildListenURL(c.in)
		if got != c.want {
			t.Errorf("BuildListenURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
