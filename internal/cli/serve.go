package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
	"github.com/d-mcmillan/rufio/internal/lib/serve"
)

// NewServeCmd returns the `rufio serve` Cobra command — the daemon that
// exposes Rufio's MCP surface over HTTPS for remote agents.
//
// TLS is mandatory unless --insecure --bind=127.0.0.1 is explicitly set
// (with a loud stderr warning). The trust model is "trusted-collaborator"
// — Bearer tokens minted with `rufio admin token mint` carry agent
// identity; the server resolves identity per-request and never trusts a
// client-supplied identity header.
//
// See docs/hosted.md for the operational guide (TLS setup, token
// management, mirror clients) and the v1.0.4 plan in docs/plans/ for the
// architecture rationale.
func NewServeCmd(version string) *cobra.Command {
	var (
		port        int
		bind        string
		tlsCert     string
		tlsKey      string
		insecure    bool
		quietFlag   bool
		noColorFlag bool
		jsonFlag    bool
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the rufio hosted daemon (HTTPS-MCP transport)",
		Long: "Expose the MCP tool surface over HTTPS so remote agents can " +
			"coordinate through this substrate.\n\n" +
			"TLS is mandatory: pass --tls-cert and --tls-key. The only " +
			"exception is local development, where --insecure --bind=127.0.0.1 " +
			"is honoured with a loud stderr warning.\n\n" +
			"Authentication is via Bearer tokens minted with " +
			"`rufio admin token mint --agent=<id>`. Identity is " +
			"server-authoritative: the client cannot override identity by " +
			"header or flag. Privacy is enforced SERVER-SIDE on every " +
			"read path.\n\n" +
			"Health probe (no auth): GET /health.\n" +
			"MCP endpoint: POST /mcp.\n" +
			"Event stream: GET /listen (SSE, requires auth).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runServe(cwd, runServeArgs{
					Port:     port,
					Bind:     bind,
					TLSCert:  tlsCert,
					TLSKey:   tlsKey,
					Insecure: insecure,
					Version:  version,
				}, opts)
			}
			if err != nil {
				HandleError("serve", err)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 8443, "TCP port to listen on")
	cmd.Flags().StringVar(&bind, "bind", "0.0.0.0", "bind address (use 127.0.0.1 for localhost-only)")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "path to PEM-encoded TLS certificate")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "path to PEM-encoded TLS private key")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "start without TLS (requires --bind=127.0.0.1; emits a loud stderr warning)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON status line on startup (no effect on log output)")
	return cmd
}

type runServeArgs struct {
	Port     int
	Bind     string
	TLSCert  string
	TLSKey   string
	Insecure bool
	Version  string
}

func runServe(cwd string, a runServeArgs, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	cfg := serve.Config{
		Root:        root,
		Bind:        a.Bind,
		Port:        a.Port,
		TLSCertFile: a.TLSCert,
		TLSKeyFile:  a.TLSKey,
		Insecure:    a.Insecure,
		Version:     a.Version,
		Logf: func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, "rufio serve: "+format+"\n", args...)
		},
	}
	if err := cfg.Validate(); err != nil {
		// Surface as UsageError so the exit code is 2 (configuration
		// mistake) rather than 1 (runtime failure).
		return &rufioerr.UsageError{Message: err.Error()}
	}
	if a.Insecure {
		fmt.Fprintln(os.Stderr, "rufio serve: WARNING — running without TLS. Localhost dev only. Do NOT expose this to a network.")
	}

	if opts.JSON {
		scheme := "https"
		if a.Insecure {
			scheme = "http"
		}
		_ = output.WriteJSONL(map[string]interface{}{
			"_type":    "serve-start",
			"_version": "1",
			"scheme":   scheme,
			"bind":     a.Bind,
			"port":     a.Port,
			"insecure": a.Insecure,
			"root":     root,
		}, opts)
	} else if !opts.Quiet {
		scheme := "https"
		if a.Insecure {
			scheme = "http"
		}
		fmt.Fprintf(os.Stderr, "rufio serve: starting %s://%s:%d (root=%s)\n", scheme, a.Bind, a.Port, root)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := serve.Run(ctx, cfg); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
	return nil
}
