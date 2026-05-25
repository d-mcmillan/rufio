package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/mirror"
	"github.com/d-mcmillan/rufio/internal/lib/output"
)

// NewMirrorCmd returns the `rufio mirror` parent verb. Two subcommands:
//
//	pull --from=<url> --token=<value> --to=<dir>   # one-shot snapshot
//	sync --from=<url> --token=<value> --to=<dir>   # continuous (default)
//
// The mirror is a READ-ONLY local file-native shadow of a remote rufio
// substrate. Writes always go through the server — the mirror cannot
// drift. This keeps the "GDL-on-disk" manifesto claim true across
// distributed deployments.
func NewMirrorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "Mirror a remote rufio substrate to a local directory",
		Long: "Maintain a file-native local shadow of a remote rufio " +
			"substrate. The mirror is read-only on the client side: " +
			"writes always go through the server (use --server=<url> on " +
			"the cognition verbs). Local files match the canonical GDL " +
			"layout byte-identically.\n\n" +
			"Modes:\n" +
			"  pull   one-shot snapshot (use for cold init or forced refresh)\n" +
			"  sync   continuous (default; opens a long-lived SSE stream)",
	}
	cmd.AddCommand(newMirrorPullCmd())
	cmd.AddCommand(newMirrorSyncCmd())
	return cmd
}

func newMirrorPullCmd() *cobra.Command {
	var from, token, to string
	var insecure, jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "One-shot snapshot of the remote substrate",
		Long: "Fetches every record the bearer-token agent is allowed to " +
			"see and writes them into --to=<dir>, preserving the canonical " +
			"on-disk layout. Atomic per file (.tmp + rename); idempotent — " +
			"re-running yields zero changes when the server hasn't changed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			from = firstNonEmpty(from, remoteServerURL, os.Getenv("RUFIO_SERVER"))
			token = firstNonEmpty(token, remoteToken, os.Getenv("RUFIO_TOKEN"))
			if from == "" {
				HandleError("mirror pull", &rufioerr.UsageError{Message: "missing --from=<url> (or RUFIO_SERVER)"})
			}
			if token == "" {
				HandleError("mirror pull", &rufioerr.UsageError{Message: "missing --token=<value> (or RUFIO_TOKEN)"})
			}
			if to == "" {
				HandleError("mirror pull", &rufioerr.UsageError{Message: "missing --to=<dir>"})
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			st, err := mirror.Pull(ctx, mirror.SnapshotOptions{
				ServerURL: from, Token: token, To: to, InsecureTLS: insecure,
			})
			if err != nil {
				HandleError("mirror pull", err)
			}
			if opts.JSON {
				_ = output.WriteJSONL(map[string]interface{}{
					"_type":           "mirror-pull",
					"_version":        "1",
					"wrote":           st.Wrote,
					"unchanged":       st.Unchanged,
					"skipped_no_path": st.SkippedNoPath,
					"to":              to,
				}, opts)
			} else if !opts.Quiet {
				output.WriteOut(fmt.Sprintf("mirror pull: wrote=%d unchanged=%d skipped=%d to=%s",
					st.Wrote, st.Unchanged, st.SkippedNoPath, to), opts)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "remote rufio server URL")
	cmd.Flags().StringVar(&token, "token", "", "bearer token")
	cmd.Flags().StringVar(&to, "to", "", "local mirror directory (required)")
	cmd.Flags().BoolVar(&insecure, "insecure-tls", false, "skip TLS verification (self-signed dev only)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON summary")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func newMirrorSyncCmd() *cobra.Command {
	var from, token, to, cursorFile string
	var insecure, jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Continuous sync from a remote substrate (default mode)",
		Long: "Opens a long-lived stream to /listen and writes incoming " +
			"events to the local mirror. On startup, does a snapshot pull " +
			"to catch up state before tailing live. Reconnects with " +
			"exponential backoff (1s, 2s, 4s, max 30s) and resumes via the " +
			"cursor file under .rufio/.mirror-cursor. Atomic writes — duplicate " +
			"events from network blips are idempotent.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			from = firstNonEmpty(from, remoteServerURL, os.Getenv("RUFIO_SERVER"))
			token = firstNonEmpty(token, remoteToken, os.Getenv("RUFIO_TOKEN"))
			if from == "" {
				HandleError("mirror sync", &rufioerr.UsageError{Message: "missing --from=<url> (or RUFIO_SERVER)"})
			}
			if token == "" {
				HandleError("mirror sync", &rufioerr.UsageError{Message: "missing --token=<value> (or RUFIO_TOKEN)"})
			}
			if to == "" {
				HandleError("mirror sync", &rufioerr.UsageError{Message: "missing --to=<dir>"})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sig
				cancel()
			}()
			if !opts.Quiet {
				output.WriteOut(fmt.Sprintf("mirror sync: %s -> %s", from, to), opts)
			}
			err := mirror.Sync(ctx, mirror.SyncOptions{
				ServerURL: from, Token: token, To: to, InsecureTLS: insecure,
				CursorFile: cursorFile,
				Logf: func(format string, args ...interface{}) {
					if !opts.Quiet {
						fmt.Fprintf(os.Stderr, "mirror sync: "+format+"\n", args...)
					}
				},
			})
			if err != nil {
				HandleError("mirror sync", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "remote rufio server URL")
	cmd.Flags().StringVar(&token, "token", "", "bearer token")
	cmd.Flags().StringVar(&to, "to", "", "local mirror directory (required)")
	cmd.Flags().StringVar(&cursorFile, "cursor-file", "", "override cursor persistence path (default: <to>/.rufio/.mirror-cursor)")
	cmd.Flags().BoolVar(&insecure, "insecure-tls", false, "skip TLS verification (self-signed dev only)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON summary on shutdown")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

// firstNonEmpty returns the first non-empty trimmed argument. Used to
// resolve --flag → --flag (global) → env-var precedence.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
