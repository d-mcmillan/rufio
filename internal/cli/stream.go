package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/identity"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/recall"
	"github.com/d-mcmillan/rufio/internal/lib/stream"
	"github.com/d-mcmillan/rufio/internal/lib/thought"
)

// streamLongHelp documents the cursor contract verbatim — `rufio stream`
// is the global firehose and its cursor format is bit-identical to
// `rufio listen` and the MCP poll tool.
const streamLongHelp = `Long-running global event stream (JSONL).

Watches live/outbox/, live/inbox/, learned/, live/promoted/ and emits
every matching record as JSONL. Use --from=<cursor> to RESUME from a
known point (the SDK reconnect contract).

The cursor is opaque: pass back the value emitted as {"_type":"cursor",
"value":"...","ts":"..."} byte-for-byte. Do NOT parse or reformat it; the
on-the-wire shape is the same opaque (ts,path) token rufio listen and
the MCP poll tool emit, so cursors are interchangeable across surfaces.

Periodic cursor emission: every ` + "`50` events or `30s`" + ` (whichever
fires first) a single {"_type":"cursor",...} JSONL line is emitted on
stdout so streaming consumers can checkpoint without parsing each event.

stream has no --catch-up flag (it's the global firehose; bounded replay
is what --from is for). Passing --from="" is equivalent to "start from
the epoch and replay every visible record first".`

// NewStreamCmd returns the `rufio stream` Cobra command. Long-running
// global event stream watching live/outbox/, live/inbox/, learned/,
// and live/promoted/.
//
//	rufio stream [--types=<csv>] [--scope=<...>] [--from=<cursor>]
func NewStreamCmd() *cobra.Command {
	var typesFlag, scopeFlag, fromFlag string
	var noColorFlag bool
	cmd := &cobra.Command{
		Use:   "stream",
		Short: "Long-running global event stream (JSONL)",
		Long:  streamLongHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				// L2: cmd.Flags().Changed("from") distinguishes "flag
				// absent" (legacy live-tail-only) from "flag explicitly
				// set to empty string" (--from="" == --catch-up per
				// streamLongHelp's documented contract).
				err = runStream(cwd, typesFlag, scopeFlag, fromFlag, cmd.Flags().Changed("from"), opts)
			}
			if err != nil {
				HandleError("stream", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typesFlag, "types", "", "comma-separated record types to include")
	cmd.Flags().StringVar(&scopeFlag, "scope", "", "scope filter (agent|deployment|fleet)")
	cmd.Flags().StringVar(&fromFlag, "from", "", "resume from opaque cursor; see --help for the cursor contract")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// buildStreamEmitOpts decides the EmitOpts for `rufio stream` given the
// resolved cursor and whether --from was explicitly set on the CLI
// (cmd.Flags().Changed("from")). The L2 contract:
//
//   - --from NOT passed              → no replay, live tail from now
//   - --from="" (epoch sentinel)     → replay everything, then live tail
//     (parity with `listen --catch-up`)
//   - --from=<non-empty>             → replay strictly after the cursor,
//     then live tail
//
// Pre-L2 (R26 bug), ReplayBeforeWatch was wired off `fromCursor != ""`
// alone, so an explicit `--from=""` silently disabled replay and stream
// docs disagreed with stream behaviour. Extracting the decision into a
// pure helper makes the L2 invariant unit-testable without spinning up
// fsnotify + stdout-capture machinery.
func buildStreamEmitOpts(fromCursor string, fromFlagSet bool) stream.EmitOpts {
	return stream.EmitOpts{
		FromCursor:         fromCursor,
		ReplayBeforeWatch:  fromFlagSet, // L2: replay when --from is set, even if empty
		CursorEveryNEvents: listenCursorEveryN,
		CursorEveryD:       listenCursorEveryD,
	}
}

// runStream wires filter validation onto stream.WatchAndEmitFrom over
// the four global dirs. Identity is best-effort — stream is
// anonymous-ok (no inbox dependency); the resolved agent is only used
// to gate agent-scope visibility inside Match.
//
// fromFlagSet reports whether --from was explicitly passed on the
// command line; see buildStreamEmitOpts for the L2 contract this
// drives.
func runStream(cwd, typesFlag, scopeFlag, fromCursor string, fromFlagSet bool, opts output.RenderOpts) error {
	_ = opts // reserved for future styled output; suppress unused warning.

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

	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	// Identity resolution is best-effort — stream doesn't require an agent.
	currentAgent, _, _ := identity.Resolve(root)

	dirs := []string{"live/outbox", "live/inbox", "learned", "live/promoted"}
	fp := stream.FilterParams{Types: types, Scope: scopeFlag, CurrentAgent: currentAgent}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	go func() {
		<-sigs
		cancel()
	}()

	// L2: ReplayBeforeWatch is gated on fromFlagSet (whether the user
	// explicitly passed --from), not on `fromCursor != ""`. This makes
	// `--from=""` behave identically to `listen --catch-up` per
	// streamLongHelp — the documented "epoch sentinel" contract. See
	// buildStreamEmitOpts for the full decision matrix.
	emitOpts := buildStreamEmitOpts(fromCursor, fromFlagSet)
	return stream.WatchAndEmitFrom(ctx, os.Stdout, root, dirs, fp, emitOpts)
}
