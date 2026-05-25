package cli

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/client"
	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// listenCursorEveryN — cadence for periodic next_cursor emission on
// stdout. 50 events is the "checkpoint roughly every page" knob: a
// typical SDK consumer paginating in batches of ~100 records sees a
// cursor twice per page, so a crash mid-page still resumes with at most
// 50 records of overlap. Lowering this hurts noisy streams (more
// JSONL lines); raising it widens the worst-case duplicate-replay
// window on reconnect. Mirrors the heuristic in tools_poll.go.
const listenCursorEveryN = 50

// listenCursorEveryD — wall-clock floor for periodic next_cursor
// emission. Sleepy streams (low event rate) get a cursor at least
// every 30s so the consumer can advance their checkpoint even when
// nothing new arrives. Paired with listenCursorEveryN: the first to
// fire wins, then both reset.
const listenCursorEveryD = 30 * time.Second

// listenLongHelp documents the cursor contract verbatim so SDK
// integrators can wire up `--from=<cursor>` without reading source.
const listenLongHelp = `Long-running inbox event stream (JSONL).

By default emits every new record under this agent's inbox + any project-
wide records (channels, summons, confirms) as they land. Use --catch-up to
flush existing records first; use --from=<cursor> to RESUME from a known
point (the SDK reconnect contract).

The cursor is opaque: pass back the value emitted as {"_type":"cursor",
"value":"...","ts":"..."} byte-for-byte. Do NOT parse or reformat it; the
on-the-wire shape is the same opaque (ts,path) token the MCP poll tool
emits as next_cursor, so cursors are interchangeable across surfaces.

Periodic cursor emission: every ` + "`50` events or `30s`" + ` (whichever
fires first) a single {"_type":"cursor",...} JSONL line is emitted on
stdout so streaming consumers can checkpoint without parsing each event.

--from and --catch-up are mutually exclusive — --from="" already means
"from the epoch", which IS --catch-up.`

// NewListenCmd returns the `rufio listen` Cobra command. Long-running
// foreground stream of inbox events for the current (or --as) agent;
// SIGINT/SIGTERM trigger a clean exit.
//
//	rufio listen [--as=<agent-id>] [--types=<csv>] [--scope=<...>] [--catch-up | --from=<cursor>]
func NewListenCmd() *cobra.Command {
	var asFlag, typesFlag, scopeFlag, fromFlag string
	var catchUpFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "listen",
		Short: "Long-running inbox event stream (JSONL)",
		Long:  listenLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runListen(cwd, asFlag, typesFlag, scopeFlag, catchUpFlag, fromFlag, opts)
			}
			if err != nil {
				HandleError("listen", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&asFlag, "as", "", "agent id to listen as (default: current identity)")
	cmd.Flags().StringVar(&typesFlag, "types", "", "comma-separated record types to include")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "scope filter (agent|deployment|fleet)")
	cmd.Flags().BoolVar(&catchUpFlag, "catch-up", false, "emit existing inbox contents first")
	cmd.Flags().StringVar(&fromFlag, "from", "", "resume from opaque cursor (mutually exclusive with --catch-up); see --help for the cursor contract")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// runListen wires identity resolution + filter validation onto
// stream.EmitCatchUp / stream.WatchAndEmit / stream.WatchAndEmitFrom.
// opts.NoColor is plumbed through for future styled-error paths even
// though the JSONL emit is already plain.
func runListen(cwd, asFlag, typesFlag, scopeFlag string, catchUp bool, fromCursor string, opts output.RenderOpts) error {
	_ = opts // reserved for future styled output; suppress unused warning.

	// Reject the impossible combo before any FS work.
	if catchUp && fromCursor != "" {
		return fmt.Errorf("--catch-up and --from are mutually exclusive — --from=\"\" is equivalent to --catch-up")
	}

	// Validate flags BEFORE any FS work — matches recall's discipline.
	types, err := recall.ValidateTypes(typesFlag)
	if err != nil {
		return err
	}
	if scopeFlag != "" {
		if err := thought.ValidateScope(scopeFlag); err != nil {
			return err
		}
	}
	// Validate cursor shape pre-FS so a malformed --from fails fast.
	if fromCursor != "" {
		if _, _, err := stream.DecodeCursor(fromCursor); err != nil {
			return err
		}
	}

	// v1.0.5: --server streams events from the server's /listen SSE
	// endpoint. The server enforces the privacy floor + filters; the
	// client just renders one JSON line per data: frame to stdout
	// (matching local listen's JSONL contract). The --as flag is
	// ignored in remote mode — identity is the bearer-token agent.
	if remoteEnabled() {
		return runListenRemote(typesFlag, scopeFlag, catchUp, fromCursor)
	}

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}

	// Resolve listen agent. --as overrides identity resolution; otherwise
	// the current identity is required (no anonymous listening — listen
	// is per-agent by design, the inbox dir is per-agent).
	agent := asFlag
	if agent == "" {
		agent, _, err = identity.Resolve(root)
		if err != nil {
			return err
		}
	} else {
		if err := identity.Validate(agent); err != nil {
			return err
		}
	}

	// Walk dirs mirror the writer layout — see the long comment in #139's
	// listen fix for the rationale. Identical between catch-up + live.
	// Single source of truth in stream.ListenDirs so the MCP listen tool
	// stays in lockstep; an inline list here was the v1.0.3 symmetry
	// regression that the PR #188 gate caught.
	dirs := stream.ListenDirs(agent)
	fp := stream.FilterParams{Types: types, Scope: scopeFlag, CurrentAgent: agent}

	// SIGINT/SIGTERM → cancel context → WatchAndEmit* returns cleanly.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		cancel()
	}()

	// Cursor-aware path: --catch-up OR --from. WatchAndEmitFrom runs the
	// bounded catch-up (strictly after --from, OR everything for
	// --catch-up) AND then engages the live watcher AND emits periodic
	// next_cursor lines. One function call replaces the
	// EmitCatchUp+WatchAndEmit pair so cursor accounting stays in sync.
	if catchUp || fromCursor != "" {
		emitOpts := stream.EmitOpts{
			FromCursor:         fromCursor,
			ReplayBeforeWatch:  true,
			CursorEveryNEvents: listenCursorEveryN,
			CursorEveryD:       listenCursorEveryD,
		}
		return stream.WatchAndEmitFrom(ctx, os.Stdout, root, dirs, fp, emitOpts)
	}

	// Plain live-tail path — no --from / --catch-up flag set. Events
	// are still emitted exactly as before; periodic {"_type":"cursor"}
	// lines are additive so a consumer routing on `_type` keeps existing
	// behaviour and can start checkpointing without re-running listen
	// with a flag. The #155 non-negotiable "DO NOT change live-tail
	// default" is read as "don't change the event shape / event set" —
	// adding a sideband cursor line type is additive and required by
	// the same spec section for SDK reconnect.
	emitOpts := stream.EmitOpts{
		CursorEveryNEvents: listenCursorEveryN,
		CursorEveryD:       listenCursorEveryD,
	}
	return stream.WatchAndEmitFrom(ctx, os.Stdout, root, dirs, fp, emitOpts)
}

// runListenRemote consumes the server's /listen SSE stream and writes
// each data: frame to stdout as a JSONL line. The wire shape on /listen
// is `data: <json>` where <json> is the same stream.Event the local
// emitter produces, so a downstream consumer routing on the JSON
// keys (`_type`, `path`, `raw`, `ts`) sees identical events whether
// the source is local or remote.
//
// Filter query parameters (?types, ?scope, ?cursor) mirror the
// /listen handler's surface. --catch-up has no separate query — the
// server's catch-up is implicit (initial drain via Poll); --from passes
// the cursor verbatim. SIGINT/SIGTERM cancels the consumer via context.
func runListenRemote(typesCSV, scope string, catchUp bool, fromCursor string) error {
	if remoteServerURL == "" {
		return fmt.Errorf("listen --server: no server URL configured")
	}
	token := resolvedToken()
	if token == "" {
		return fmt.Errorf("listen --server: no token (set --token or RUFIO_TOKEN)")
	}

	endpoint := client.BuildListenURL(remoteServerURL)
	q := url.Values{}
	if typesCSV != "" {
		q.Set("types", typesCSV)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	if fromCursor != "" {
		q.Set("cursor", fromCursor)
	}
	// --catch-up = empty cursor → server emits the catch-up drain on
	// connect. The local listen treats catch-up and from="" the same
	// way; nothing more to do here.
	_ = catchUp
	if encoded := q.Encode(); encoded != "" {
		endpoint = endpoint + "?" + encoded
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		cancel()
	}()

	return client.ConsumeSSE(ctx, client.SSEOptions{
		Endpoint:    endpoint,
		Token:       token,
		InsecureTLS: remoteInsecure,
	}, func(ev client.SSEEvent) error {
		if ev.IsComment {
			// Heartbeat / token-revoke notice — silently drop. The
			// local listen doesn't echo heartbeats either.
			return nil
		}
		if ev.Data == "" {
			return nil
		}
		// Emit one JSONL line per data: payload. The server emits
		// canonical-JSON already (recall.RenderJSON-shaped events) so
		// passthrough is byte-stable.
		if _, err := fmt.Fprintln(os.Stdout, ev.Data); err != nil {
			return err
		}
		return nil
	})
}
