package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SSEEvent is one decoded SSE message — `id:` + `event:` + `data:` lines
// terminated by a blank line. Comment-only lines (`: heartbeat...`) are
// surfaced via the IsComment marker so the caller can route them
// separately from real events.
type SSEEvent struct {
	ID        string
	Event     string
	Data      string
	IsComment bool
	// Comment captures the raw text of `: ` lines so a caller that
	// wants to display heartbeats can. Empty for non-comment events.
	Comment string
}

// SSEOptions configure ConsumeSSE. Endpoint MUST be the full URL of the
// server-sent-events stream (e.g. https://rufio.example.com/listen);
// query parameters are passed through. Token is the bearer plaintext;
// InsecureTLS gates the TLS-verification floor exactly like client.Dial.
type SSEOptions struct {
	Endpoint    string
	Token       string
	InsecureTLS bool
	// HTTPClient is exposed for tests. Nil = a sane default with the
	// same TLS-1.3 floor the MCP client uses.
	HTTPClient *http.Client
}

// ConsumeSSE opens an SSE connection and invokes onEvent for each
// decoded message until the underlying reader returns an error or ctx
// is canceled. Comment lines surface as SSEEvent{IsComment: true} so
// the caller can route heartbeats without re-parsing.
//
// Security posture mirrors client.Dial:
//   - https:// required unless InsecureTLS=true AND host is loopback
//   - TLS 1.3 minimum (modernTLS floor)
//   - Bearer attached via Authorization header on the initial request
//
// On non-2xx response the function returns a typed error capturing the
// HTTP status so callers can distinguish auth failures (401) from
// transient server issues. Cancellation via ctx returns ctx.Err().
//
// onEvent returning a non-nil error halts the loop and surfaces that
// error to the caller — the SSE response body is closed cleanly via the
// deferred Close even on early return.
func ConsumeSSE(ctx context.Context, opts SSEOptions, onEvent func(SSEEvent) error) error {
	if opts.Endpoint == "" {
		return errors.New("ConsumeSSE: Endpoint required")
	}
	if opts.Token == "" {
		return errors.New("ConsumeSSE: Token required")
	}
	if err := validateEndpointScheme(opts.Endpoint, opts.InsecureTLS); err != nil {
		return err
	}

	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS13,
					InsecureSkipVerify: opts.InsecureTLS,
				},
			},
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.Endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &SSEStatusError{Status: resp.StatusCode}
	}

	return readSSEStream(ctx, resp.Body, onEvent)
}

// SSEStatusError carries a non-200 status from an SSE connect so the
// caller can branch on auth vs transient failures.
type SSEStatusError struct {
	Status int
}

func (e *SSEStatusError) Error() string {
	return fmt.Sprintf("SSE connect: HTTP status %d", e.Status)
}

// readSSEStream is the pure parser — exposed for tests that drive the
// loop against an in-memory reader without an HTTP server. Public-facing
// callers use ConsumeSSE.
func readSSEStream(ctx context.Context, body io.Reader, onEvent func(SSEEvent) error) error {
	reader := bufio.NewReader(body)
	var ev SSEEvent
	for {
		// Respect cancellation between lines — the bufio reader doesn't
		// honor context on its own.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && line == "" {
				return nil
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// Event terminator. Emit if anything accumulated.
			if ev.Data != "" || ev.ID != "" || ev.Event != "" {
				if cbErr := onEvent(ev); cbErr != nil {
					return cbErr
				}
			}
			ev = SSEEvent{}
			continue
		}
		switch {
		case strings.HasPrefix(line, ": "):
			// Comment / heartbeat. Surface as IsComment so the caller
			// can choose to ignore or display.
			if cbErr := onEvent(SSEEvent{IsComment: true, Comment: strings.TrimPrefix(line, ": ")}); cbErr != nil {
				return cbErr
			}
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		default:
			// Unknown field per the SSE spec — ignore. retry: lands
			// here and we don't honor it (reconnect policy is the
			// caller's responsibility).
		}
	}
}

// BuildListenURL composes the /listen endpoint URL from a server base
// URL (which may already point at /mcp). Mirror sync and listen --server
// both route through this so the rewrite logic stays in one place.
func BuildListenURL(serverURL string) string {
	base := strings.TrimRight(serverURL, "/")
	base = strings.TrimSuffix(base, "/mcp")
	return strings.TrimRight(base, "/") + "/listen"
}
