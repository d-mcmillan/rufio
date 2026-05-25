package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// listenHandler streams live substrate events to the caller as Server-
// Sent Events. The caller's identity (resolved by authMiddleware before
// this handler is reached) is the privacy floor — scope=agent records
// authored by other agents never leave the server.
//
// Wire format:
//
//	id: <event-id>
//	event: <record-type>
//	data: {<json record>}
//
// Cursor model: client passes ?cursor=<canonical-ts> to resume from a
// previous stream. Server emits events whose canonical-ts > cursor in
// chronological order, then catches up to live by polling on a 500ms
// tick.
//
// Heartbeat: a comment-only SSE line (`: heartbeat ...`) is emitted
// every 30s during quiet periods so middleboxes don't drop the
// long-lived connection.
//
// The handler honours r.Context().Done() so srv.Shutdown drains
// gracefully and the client's ctx cancellation tears down the stream
// without dangling polls.
//
// Filters:
//   - ?types=<csv>  thought,observation,reason,...  (recall.ValidateTypes)
//   - ?scope=agent|deployment|fleet  (thought.ValidateScope)
func listenHandler(root string, logf func(string, ...interface{})) http.HandlerFunc {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		agent := AgentFromContext(r.Context())
		// Pre-flush headers: SSE clients block on the first response
		// chunk if Content-Type isn't set.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Parse filters.
		q := r.URL.Query()
		cursor := q.Get("cursor")
		typesCSV := q.Get("types")
		scope := q.Get("scope")
		types, err := recall.ValidateTypes(typesCSV)
		if err != nil {
			http.Error(w, "invalid types: "+err.Error(), http.StatusBadRequest)
			return
		}
		if scope != "" {
			if err := thought.ValidateScope(scope); err != nil {
				http.Error(w, "invalid scope: "+err.Error(), http.StatusBadRequest)
				return
			}
		}

		fp := stream.FilterParams{Types: types, Scope: scope, CurrentAgent: agent}

		// dirs is the set of substrate directories that emit events.
		//
		// `live/` carries in-flight coordination (thoughts, confirms,
		// channels, summons, reasoning, retracted, promoted-audit).
		//
		// `learned/` (Gate 4 follow-up) carries the durable knowledge
		// layer — promoted observations the auto-promote engine writes
		// after a thought clears the confirm-quorum threshold. Mirrors
		// must propagate these for the "file-native local shadow"
		// manifesto claim to hold (a user grepping their local mirror
		// for promoted decisions otherwise finds nothing).
		//
		// `given/` is deliberately NOT walked — given/ is the
		// human-authored provenance layer; mirror users opt-in via a
		// separate mechanism (out of scope for v1.0.4).
		//
		// Privacy gate filters per-agent records downstream.
		dirs := []string{
			filepath.ToSlash("live"),
			filepath.ToSlash("learned"),
		}

		// Security audit F3: capture the bearer plaintext at connect
		// time so we can re-verify it on every poll tick. Pre-fix the
		// handler resolved identity ONCE via the auth middleware and
		// then streamed indefinitely — admin's `rufio admin token
		// revoke <id>` had no effect on an existing /listen connection
		// (the attacker kept receiving events until TCP teardown).
		// Now: each tick, ResolveToken is re-called; ErrTokenInvalid
		// closes the stream cleanly.
		//
		// parseBearer is the same strict parser the auth middleware
		// uses (case-sensitive "Bearer " prefix, single-token payload).
		// A request without a valid header could not reach this
		// handler (the middleware would have 401'd), but we guard
		// defensively rather than assume.
		bearer, _ := parseBearer(r.Header.Get("Authorization"))

		// Security audit M3 (v1.0.5): cap concurrent /listen
		// connections per token. A token holder MUST NOT be able
		// to open unlimited streams — each one binds a poll
		// ticker + a goroutine for the duration of the connection.
		// 32 concurrent is generous for a real agent fleet
		// (typically 1-2 listeners per token) and bounds the
		// worst-case footprint a hostile/buggy client can impose.
		//
		// When the cap is hit, respond 429 Too Many Requests with
		// a Retry-After hint so a well-behaved client backs off.
		// Per audit L3 (v1.0.5), connCounter.release now deletes
		// cells at zero-count under the write lock; the map stays
		// bounded by currently active tokens, not by lifetime token
		// count.
		if bearer != "" {
			if !defaultConnCounter.tryAcquire(bearer, listenConnCap) {
				w.Header().Set("Retry-After", "30")
				http.Error(w, "too many concurrent /listen connections for this token", http.StatusTooManyRequests)
				logf("listen: token %s rejected — connection cap hit", agent)
				return
			}
			defer defaultConnCounter.release(bearer)
		}

		// First flush — empty so the client knows the connection is open.
		flusher.Flush()

		nextCursor := cursor
		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()
		poll := time.NewTicker(500 * time.Millisecond)
		defer poll.Stop()

		writeEvent := func(ev stream.Event) error {
			bs, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			id := eventID(ev)
			_, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, ev.Type, bs)
			return err
		}

		emitOnce := func() error {
			evs, c, err := stream.Poll(root, dirs, fp, nextCursor, 200)
			if err != nil {
				return err
			}
			for _, ev := range evs {
				if err := writeEvent(ev); err != nil {
					return err
				}
			}
			if c != "" {
				nextCursor = c
			}
			if len(evs) > 0 {
				flusher.Flush()
			}
			return nil
		}

		// Initial drain (catch-up).
		_ = emitOnce()

		logf("listen: %s connected (cursor=%q)", agent, cursor)

		for {
			select {
			case <-r.Context().Done():
				logf("listen: %s disconnected", agent)
				return
			case <-heartbeat.C:
				_, err := fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339Nano))
				if err != nil {
					return
				}
				flusher.Flush()
			case <-poll.C:
				// Security audit F3: re-verify the bearer token
				// before doing any more work on this tick. A token
				// that admin has revoked since connect-time MUST
				// close the stream, not keep feeding events to the
				// (now-known-compromised) holder. ResolveToken
				// also catches deleted tokens; both are treated
				// uniformly as "no longer authorised".
				//
				// F3 follow-up (cross-machine gate): the
				// fmt.Fprintf + flusher.Flush + return sequence
				// alone was NOT enough on real WAN deployment —
				// HTTP/2 frame buffering held END_STREAM until
				// long after the handler returned. The serve
				// listener now disables HTTP/2 (see modernTLS +
				// http.Server.TLSNextProto), AND we hijack the
				// connection on revoke to force-close the TCP
				// socket. Two layers means a misconfigured deploy
				// can't silently slip back into the buggy state.
				if bearer == "" {
					return
				}
				if _, err := admin.ResolveToken(root, bearer); err != nil {
					if errors.Is(err, admin.ErrTokenInvalid) {
						_, _ = fmt.Fprintf(w, ": token revoked, closing\n\n")
						flusher.Flush()
						logf("listen: %s token revoked mid-stream — closing", agent)
						forceCloseConn(w)
						return
					}
					// Soft error reading the token store (e.g.
					// transient I/O); skip this tick like emitOnce
					// would on its own soft errors.
					continue
				}
				if err := emitOnce(); err != nil {
					// Soft error: skip this tick, retry next.
					continue
				}
			}
		}
	}
}

// eventID derives a stable per-event identifier suitable for the SSE
// `id:` field. The substrate guarantees the (Path, TS) pair is unique
// (one record per file, files are addressed by canonical-ts-prefixed
// names), so a path-suffix + ts string is a safe collision-free choice
// without a UUID dependency.
func eventID(ev stream.Event) string {
	base := filepath.Base(ev.Path)
	if base == "" {
		base = ev.Type
	}
	return ev.TS + "-" + strings.TrimSuffix(base, filepath.Ext(base))
}

// forceCloseConn hijacks w's underlying TCP connection and closes it
// immediately. Used on the revoke path so the FIN packet goes out
// regardless of any HTTP-layer buffering — the cross-machine gate
// reproduced this: handler returned promptly, fmt.Fprintf + Flush
// happened, but Go's HTTP/2 server queued END_STREAM and the client
// kept seeing an open stream for minutes.
//
// HTTP/1.1 supports Hijacker; HTTP/2 does not (you can't hijack an
// HTTP/2 stream). The serve listener now disables HTTP/2 explicitly
// (modernTLS + http.Server.TLSNextProto), so this Hijack call
// succeeds in production. Best-effort: a non-hijackable writer (e.g.
// a test recorder) silently falls through — the handler return is
// the floor.
func forceCloseConn(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}
