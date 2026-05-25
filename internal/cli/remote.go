package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/d-mcmillan/rufio/internal/lib/client"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// remoteServerURL / remoteToken are the global --server / --token flags.
// They're wired as persistent flags on the root command (see root.go).
// Each verb that supports remote routing inspects them via remoteEnabled().
var (
	remoteServerURL  string
	remoteToken      string
	remoteInsecure   bool
	remoteTimeoutStr string
)

// identityMismatchOnce gates the one-line stderr warning emitted on the
// first --server-routed call where the server's bound agent differs from
// the client-supplied RUFIO_AGENT_ID. Field feedback (issue #213, Joey/
// joetemachi's v1.0.5 field report) surfaced this as a foot-gun: agents
// set RUFIO_AGENT_ID expecting it to be honoured, then quietly impersonate
// whichever agent the token resolves to on the server side.
//
// We fire ONCE per process so a script looping through many verbs doesn't
// drown the operator's terminal in repeats.
var identityMismatchOnce sync.Once

// emitIdentityMismatchWarning checks the server response for an `agent`
// field (most cognition tools return it) and, if the resolved bound
// agent differs from the client-supplied RUFIO_AGENT_ID env var, emits
// the one-line warning the spec mandates.
//
// Best-effort: tools whose response doesn't carry an `agent` field
// (e.g. recall, listen) leave the once unfired until a verb that does
// run during the same process. That's acceptable — the warning fires
// the first time we can actually resolve the bound agent.
func emitIdentityMismatchWarning(res map[string]interface{}) {
	env := strings.TrimSpace(os.Getenv("RUFIO_AGENT_ID"))
	if env == "" {
		return
	}
	bound, _ := res["agent"].(string)
	bound = strings.TrimSpace(bound)
	if bound == "" || bound == env {
		return
	}
	identityMismatchOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"note: --token is bound to agent=%s; client-supplied RUFIO_AGENT_ID=%s is ignored (server-authoritative identity)\n",
			bound, env)
	})
}

// resetIdentityMismatchOnceForTest re-arms the once gate. Tests that
// exercise the warning path more than once (or compose multiple
// scenarios in one go test process) call this between assertions.
func resetIdentityMismatchOnceForTest() {
	identityMismatchOnce = sync.Once{}
}

// remoteEnabled returns true iff the caller asked for remote dispatch
// (either explicit --server or RUFIO_SERVER env var). When true, the
// verb's RunE delegates to remoteCallAndRender instead of running the
// local libs.
func remoteEnabled() bool {
	if strings.TrimSpace(remoteServerURL) != "" {
		return true
	}
	if v := strings.TrimSpace(os.Getenv("RUFIO_SERVER")); v != "" {
		remoteServerURL = v
		return true
	}
	return false
}

// resolvedToken returns the bearer-token plaintext from --token or the
// RUFIO_TOKEN env var. Empty when neither is set — the client lib will
// return a descriptive error in that case.
func resolvedToken() string {
	if t := strings.TrimSpace(remoteToken); t != "" {
		return t
	}
	return strings.TrimSpace(os.Getenv("RUFIO_TOKEN"))
}

// remoteCallAndRender dials the configured remote server, invokes the
// named MCP tool with args, and re-renders the response through the
// CLI's existing JSON output path. Text-mode rendering is intentionally
// pass-through (raw JSON) — the goal is parity with `rufio mcp`'s
// surface, not duplicating every text renderer. Callers that need rich
// text output should use --json or upgrade the local renderer path.
func remoteCallAndRender(cmdName, toolName string, args map[string]interface{}, opts output.RenderOpts) error {
	timeout := 30 * time.Second
	if remoteTimeoutStr != "" {
		if d, err := time.ParseDuration(remoteTimeoutStr); err == nil && d > 0 {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cfg := client.Config{
		Endpoint:    remoteServerURL,
		Token:       resolvedToken(),
		InsecureTLS: remoteInsecure,
		HTTPTimeout: timeout,
	}
	if cfg.Token == "" {
		return &rufioerr.UsageError{Message: "no token configured (set --token=<value> or RUFIO_TOKEN=<value>)"}
	}
	if remoteInsecure {
		fmt.Fprintf(os.Stderr, "rufio %s: WARNING — TLS verification disabled (--insecure)\n", cmdName)
	}

	c, err := client.Dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()

	res, err := c.CallTool(ctx, toolName, args)
	if err != nil {
		return err
	}

	// Bundle E (Joey #213): warn ONCE if the server-resolved bound agent
	// differs from RUFIO_AGENT_ID. Identity is server-authoritative when
	// --server is in play, so the env var is silently ignored — surface
	// that to the operator instead of letting it remain a foot-gun.
	emitIdentityMismatchWarning(res)

	// JSON path: emit the result directly (matches the symmetry
	// contract — the MCP tool's wire shape == the --json output of
	// the local verb).
	if opts.JSON {
		bs, _ := json.Marshal(res)
		fmt.Println(string(bs))
		return nil
	}
	// Text-mode pass-through: pretty-print so the operator can read it
	// without piping through jq. Not byte-identical to the local
	// renderer — see the package comment for the rationale.
	bs, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(bs))
	return nil
}

// dropEmpty filters out args whose values are zero. Keeps the wire
// payload tidy and stable across re-runs.
func dropEmpty(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			if strings.TrimSpace(t) == "" {
				continue
			}
		case []string:
			if len(t) == 0 {
				continue
			}
		case []interface{}:
			if len(t) == 0 {
				continue
			}
		case int:
			if t == 0 {
				continue
			}
		case bool:
			if !t {
				continue
			}
		}
		out[k] = v
	}
	return out
}

// remoteFlagsRegistered tracks whether the persistent flags have been
// added — Cobra panics on duplicate flag registration if a test reuses
// the root cmd. The sync.Once equivalent in our flat package layout is
// a package-level bool guarded by the fact that NewRootCmd builds a
// fresh root every call.
//
// (No mutex needed — Cobra's persistent-flag wiring is single-threaded
// during command setup.)
var _ = remoteFlagsRegistered

func remoteFlagsRegistered() bool { return false }
