package serve

import (
	"net/http"
)

// maxBytesMiddleware caps the request body size at limit bytes via
// http.MaxBytesReader. Defends against memory-exhaustion DoS on the
// /mcp endpoint — see internal/lib/serve/http.go::mcpMaxBodyBytes
// for the rationale + audit reference (M2, v1.0.5).
//
// http.MaxBytesReader returns *http.MaxBytesError via the normal Read
// error channel — it does NOT panic. When the inner handler attempts
// to read past the cap, its body decoder receives the error and
// surfaces a 4xx-ish response (Go's MCP server wraps the read error
// in its own protocol-level error frame). The defense is therefore
// "MaxBytesReader truncates + signals; inner handler reports
// failure" — no middleware-side panic/recover is involved.
//
// Audit L1 (v1.0.5 follow-up): an earlier draft of this middleware
// installed a defer/recover under the mistaken belief that the SDK's
// MaxBytesReader panics on over-cap reads. It does not. The recover
// block was dead code AND a foot-gun (rec.(error) would panic on a
// non-error recover value, masking the original panic). Removed.
func maxBytesMiddleware(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}
