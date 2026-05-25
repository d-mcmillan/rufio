package mirror

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SyncOptions configure a continuous Sync.
type SyncOptions struct {
	ServerURL   string
	Token       string
	InsecureTLS bool
	To          string
	// CursorFile is where the last-applied canonical TS is persisted.
	// Empty means default: <To>/.rufio/.mirror-cursor.
	CursorFile string
	// Logf receives one-line status messages. Nil = silent.
	Logf func(format string, args ...interface{})
	// HTTPClient is exposed for tests that want to inject a transport.
	// Nil means a sane default with TLS-1.2+ and the InsecureTLS gate.
	HTTPClient *http.Client
}

// ErrSyncNotWired is retained for back-compat with the Task 7 stub; not
// returned by the live Sync.
var ErrSyncNotWired = errors.New("mirror sync: not implemented")

// ErrSyncUnauthorized is returned by Sync after maxConsecutiveUnauthorized
// consecutive 401 responses from the server. Gate 5 UX follow-up:
// previously sync retried indefinitely with exponential backoff after
// a revocation, consuming network + compute without surfacing the
// actual cause. After the threshold, Sync now returns this sentinel
// + logs a clear "token appears revoked; aborting" message.
var ErrSyncUnauthorized = errors.New("mirror sync: token appears revoked (persistent 401 responses); aborting")

// errSyncUnauthorizedTick is the per-connection signal that runSyncLoop
// uses to tell the outer loop "this attempt got a 401, count it." Not
// exported — callers only see ErrSyncUnauthorized after the threshold.
var errSyncUnauthorizedTick = errors.New("mirror sync: 401 from server")

// maxConsecutiveUnauthorized is the cap on consecutive 401 responses
// before Sync aborts. 5 retries with the 1+2+4+8+16s backoff schedule
// = ~31s end-to-end — short enough to surface revocation promptly,
// long enough to tolerate a transient auth-server hiccup.
const maxConsecutiveUnauthorized = 5

// Sync runs the continuous mirror loop until ctx is canceled or an
// unrecoverable error occurs. The loop:
//
//  1. On startup, runs a snapshot Pull to catch up state (avoids gap
//     window between cursor file and live stream).
//  2. Connects to /listen with the persisted cursor (if any).
//  3. Streams events, writes each underlying record to <To>/<canonical-path>
//     atomically (.tmp + rename).
//  4. Persists the cursor after each successful write.
//  5. On disconnect: backs off (1s, 2s, 4s, max 30s) and reconnects with
//     the saved cursor — duplicate events are idempotent (atomic content
//     equality means no spurious rewrites).
//  6. On ctx.Done(): drains the in-flight write, flushes the cursor,
//     and returns nil.
func Sync(ctx context.Context, opts SyncOptions) error {
	if opts.ServerURL == "" {
		return errors.New("mirror sync: --from is required")
	}
	if opts.Token == "" {
		return errors.New("mirror sync: --token is required")
	}
	if opts.To == "" {
		return errors.New("mirror sync: --to is required")
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}

	// Ensure the --to directory exists before any joinUnderRoot call
	// runs against it. Pre-fix, an operator running `rufio mirror sync
	// --to=./fresh-dir` with no preceding mkdir saw a silent zero-event
	// sync: the F4 symlink-defense walked up past the non-existent
	// --to, found the parent dir's macOS-canonical form (e.g. /tmp →
	// /private/tmp) didn't match /tmp/fresh-dir, and rejected every
	// event as a symlink escape. Auto-creating the root removes the
	// foot-gun for the documented user flow.
	if err := os.MkdirAll(opts.To, 0o755); err != nil {
		return fmt.Errorf("mirror sync: cannot create --to directory %q: %w", opts.To, err)
	}

	cursorPath := opts.CursorFile
	if cursorPath == "" {
		cursorPath = filepath.Join(opts.To, ".rufio", ".mirror-cursor")
	}

	// Startup snapshot pull — catches up the substrate state before the
	// live stream begins, so no events are lost in the gap.
	if _, err := Pull(ctx, SnapshotOptions{
		ServerURL: opts.ServerURL, Token: opts.Token, To: opts.To, InsecureTLS: opts.InsecureTLS,
	}); err != nil {
		// Log but don't abort — sync should still start even if the
		// initial pull is degraded.
		logf("startup pull warning: %v", err)
	}

	cursor := readCursor(cursorPath)
	backoff := time.Second

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &http.Transport{
				// Security audit L1: TLS 1.3 minimum (mirrors
				// internal/lib/serve/http.go::modernTLS).
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS13,
					InsecureSkipVerify: opts.InsecureTLS,
				},
			},
		}
	}

	// Gate 5 UX follow-up: count consecutive 401s. A successful
	// connection (one that gets past the auth gate AND streams at
	// least the initial response — i.e. runSyncLoop didn't return
	// errSyncUnauthorizedTick) resets the counter to 0. After
	// maxConsecutiveUnauthorized 401s in a row, abort with
	// ErrSyncUnauthorized so the wrapping process exits non-zero
	// and the operator sees "your token is revoked" rather than
	// silent indefinite retries.
	consecutiveUnauthorized := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		next, runErr := runSyncLoop(ctx, httpClient, opts.ServerURL, opts.Token, opts.To, cursor, cursorPath, logf)
		if next != "" {
			cursor = next
		}
		if ctx.Err() != nil {
			return nil
		}
		if runErr == nil {
			// Loop exited cleanly (rare — usually it errors on disconnect).
			return nil
		}
		if errors.Is(runErr, errSyncUnauthorizedTick) {
			consecutiveUnauthorized++
			if consecutiveUnauthorized >= maxConsecutiveUnauthorized {
				logf("token appears revoked after %d consecutive 401 responses; aborting", consecutiveUnauthorized)
				return ErrSyncUnauthorized
			}
		} else {
			consecutiveUnauthorized = 0
		}
		logf("listen stream ended (%v); reconnecting in %s", runErr, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

// runSyncLoop opens one /listen connection and consumes events until
// the connection drops or ctx is canceled. Returns the latest cursor
// observed + any error that ended the stream.
func runSyncLoop(ctx context.Context, hc *http.Client, serverURL, token, to, cursor, cursorPath string, logf func(string, ...interface{})) (string, error) {
	listenURL := strings.TrimRight(serverURL, "/") + "/listen"
	if !strings.HasSuffix(serverURL, "/listen") {
		// Allow callers to pass either the base URL or the /mcp path
		// — normalise to <base>/listen.
		base := strings.TrimRight(serverURL, "/")
		base = strings.TrimSuffix(base, "/mcp")
		listenURL = strings.TrimRight(base, "/") + "/listen"
	}
	if cursor != "" {
		listenURL = listenURL + "?cursor=" + cursor
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listenURL, nil)
	if err != nil {
		return cursor, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return cursor, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Gate 5 UX follow-up: tag 401 explicitly so the outer
		// retry loop can count consecutive auth failures and abort
		// after the threshold. Other non-200 codes are transient
		// server errors that the indefinite backoff loop handles
		// fine.
		if resp.StatusCode == http.StatusUnauthorized {
			return cursor, fmt.Errorf("connect /listen: %w", errSyncUnauthorizedTick)
		}
		return cursor, fmt.Errorf("connect /listen: status %d", resp.StatusCode)
	}
	logf("connected to %s (cursor=%q)", listenURL, cursor)

	reader := bufio.NewReader(resp.Body)
	currentEvent := struct {
		id   string
		data string
	}{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return cursor, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			// Empty line = end of event. Process if we have data.
			if currentEvent.data != "" {
				newCursor, perr := persistEvent(currentEvent.data, to)
				if perr == nil && newCursor != "" {
					cursor = newCursor
					_ = writeCursor(cursorPath, cursor)
				}
			}
			currentEvent.id = ""
			currentEvent.data = ""
			continue
		}
		switch {
		case strings.HasPrefix(line, "id: "):
			currentEvent.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			currentEvent.data = strings.TrimPrefix(line, "data: ")
		case strings.HasPrefix(line, ": "):
			// Comment / heartbeat — ignore.
		default:
			// Other SSE fields we don't use (event:, retry:) — ignore.
		}
	}
}

// persistEvent decodes one JSON record and writes it to the local mirror.
// Returns the canonical TS of the persisted event (the cursor checkpoint)
// or an empty string when the record was not persisted (unsupported type,
// missing path).
func persistEvent(data, to string) (string, error) {
	var ev map[string]interface{}
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return "", err
	}
	// The stream.Event shape carries `path` directly — much simpler than
	// recall's projectRecordToFile which reconstructs the path from
	// fields.
	//
	// Defense in depth (security audit H1): the server is the trusted-
	// collaborator floor, but a typo'd --server= URL or DNS hijacking
	// could land us talking to an attacker who emits crafted `path`
	// values (e.g. "../../../etc/cron.d/exploit"). joinUnderRoot
	// rejects: absolute paths, ".." traversal segments, NUL/control
	// bytes, and any post-clean form whose Rel() from the root starts
	// with "..". Refusing to write is preferable to silent rejection;
	// the SSE stream stays alive and the cursor advances past the bad
	// event so a recovered substrate doesn't loop on the same record.
	path, _ := ev["path"].(string)
	raw, _ := ev["raw"].(string)
	ts, _ := ev["ts"].(string)
	if path == "" || raw == "" {
		return ts, nil
	}
	// Ensure the line ends with a newline — the substrate's on-disk
	// shape uses one record per line, newline terminated.
	if !strings.HasSuffix(raw, "\n") {
		raw = raw + "\n"
	}
	// Security audit H1 (v1.0.5 follow-up): apply the same
	// cleaned-form top-level allowlist that projectRecordToFile
	// enforces. Pre-fix, a wire path like "live/../.rufio/.mirror-
	// cursor" passed safeRelPath (cleaned form is .rufio/.mirror-
	// cursor — under root) and joinUnderRoot (same check), then
	// writeAtomic clobbered the cursor file inside the mirror
	// root but OUTSIDE {live, learned, given}. The snapshot path
	// had the gate; the live-sync path (this function, the more
	// commonly used long-running mode) was missed in v1.0.5 Phase
	// C. Same helper, same allow-list.
	if err := validateCleanedTopLevel(path); err != nil {
		return ts, err
	}
	dst, err := joinUnderRoot(to, path)
	if err != nil {
		return ts, err
	}
	if _, werr := writeAtomic(dst, raw); werr != nil {
		return ts, werr
	}
	return ts, nil
}

// readCursor returns the persisted cursor or "" if none.
func readCursor(path string) string {
	bs, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(bs))
}

// writeCursor persists the cursor atomically.
//
// Security audit M2 (v1.0.5 follow-up): use os.CreateTemp for the
// tmp filename so two concurrent `rufio mirror sync` processes
// against the same --to don't race on `<dir>/.mirror-cursor.tmp`.
// Pre-fix, the second writer's content stomped the first's tmp;
// one rename lost to a no-such-file error or to the wrong bytes.
// Mirrors the M4 fix in writeAtomic (the snapshot-side helper)
// exactly — same shape, same cleanup posture.
func writeCursor(path, cursor string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, werr := tmp.Write([]byte(cursor + "\n")); werr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return werr
	}
	if cerr := tmp.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return cerr
	}
	if rerr := os.Rename(tmpPath, path); rerr != nil {
		_ = os.Remove(tmpPath)
		return rerr
	}
	return nil
}
