package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/d-mcmillan/rufio/internal/lib/admin"
	rufioerr "github.com/d-mcmillan/rufio/internal/lib/errors"
	"github.com/d-mcmillan/rufio/internal/lib/output"
	"github.com/d-mcmillan/rufio/internal/lib/paths"
)

// NewAdminCmd returns the `rufio admin` parent verb that bundles the
// operator-only subcommands (currently `token mint|revoke|list`). Kept
// separate from the cognition verbs so the surface is grep-clean: `rufio
// admin <thing>` is always operator-only, never agent-level.
//
// Token mint/revoke are NOT exposed via MCP. They're privileged operations
// on the server's filesystem — only a local operator with shell access
// should be able to invoke them. Agents can introspect server health via
// the serve_status MCP tool (task 12) but cannot mint or revoke.
func NewAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Operator-only commands (server administration)",
		Long: "Bundles the rufio operator-only subcommands. Token mint and " +
			"revoke are intentionally NOT exposed via the MCP surface — " +
			"only a local operator with shell access should be able to " +
			"invoke them.",
	}
	cmd.AddCommand(newAdminTokenCmd())
	return cmd
}

func newAdminTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage bearer tokens for rufio serve",
		Long: "Mint, revoke, and list the bearer tokens that authenticate " +
			"agents against `rufio serve`. The plaintext token is shown " +
			"EXACTLY ONCE at mint time; lose it and the token must be " +
			"revoked + reissued.",
	}
	cmd.AddCommand(newAdminTokenMintCmd())
	cmd.AddCommand(newAdminTokenRevokeCmd())
	cmd.AddCommand(newAdminTokenListCmd())
	return cmd
}

func newAdminTokenMintCmd() *cobra.Command {
	var agent string
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "mint",
		Short: "Mint a fresh bearer token bound to an agent identity",
		Long: "Mints a new bearer token and prints the plaintext token + " +
			"the public token id. The plaintext is shown EXACTLY ONCE — " +
			"capture it now; the server only retains the SHA-256 hash.\n\n" +
			"Plain output (machine-parseable):\n" +
			"  token_id=tok-abc1234567\n" +
			"  token=rufio_<43-char-base64url>\n\n" +
			"Pipe to `read` / `cut -d= -f2` from shell scripts:\n" +
			"  TOKEN=$(rufio admin token mint --agent=alice | grep ^token= | cut -d= -f2)",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAdminTokenMint(cwd, strings.TrimSpace(agent), opts)
			}
			if err != nil {
				HandleError("admin token mint", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "agent identity to bind the token to (required)")
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON output instead of key=value lines")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter (no effect on token output — that's data)")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runAdminTokenMint(cwd, agent string, opts output.RenderOpts) error {
	if agent == "" {
		return &rufioerr.UsageError{Message: "missing required flag --agent=<id>"}
	}
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	plaintext, tok, err := admin.MintToken(root, agent)
	if err != nil {
		return err
	}
	if opts.JSON {
		return output.WriteJSONL(map[string]string{
			"_type":    "token-mint",
			"_version": "1",
			"token_id": tok.ID,
			"agent":    tok.Agent,
			"token":    plaintext,
		}, opts)
	}
	// Two-line key=value output. Token is data — print regardless of
	// --quiet (same rule as `rufio whoami`).
	output.WriteData("token_id="+tok.ID, opts)
	output.WriteData("token="+plaintext, opts)
	if !opts.Quiet {
		fmt.Fprintln(os.Stderr, "rufio admin token mint: plaintext shown ONCE. Capture it now; the server keeps only the SHA-256 hash.")
	}
	return nil
}

func newAdminTokenRevokeCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Mark a bearer token as revoked (server rejects subsequent calls)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAdminTokenRevoke(cwd, args[0], opts)
			}
			if err != nil {
				HandleError("admin token revoke", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSON output")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runAdminTokenRevoke(cwd, tokenID string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	if err := admin.RevokeToken(root, tokenID); err != nil {
		return err
	}
	if opts.JSON {
		return output.WriteJSONL(map[string]string{
			"_type":    "token-revoke",
			"_version": "1",
			"token_id": tokenID,
		}, opts)
	}
	if !opts.Quiet {
		output.WriteOut("revoked "+tokenID, opts)
	}
	return nil
}

func newAdminTokenListCmd() *cobra.Command {
	var jsonFlag, quietFlag, noColorFlag bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all minted bearer tokens (active + revoked)",
		Long: "Lists every token on disk with its public id, bound agent, " +
			"creation timestamp, and revoked status. The plaintext is " +
			"never re-emittable — list shows hashes only via the public " +
			"projection (which omits them entirely).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := output.RenderOpts{JSON: jsonFlag, Quiet: quietFlag, NoColor: noColorFlag}
			cwd, err := os.Getwd()
			if err == nil {
				err = runAdminTokenList(cwd, opts)
			}
			if err != nil {
				HandleError("admin token list", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "emit JSONL output (one record per line)")
	cmd.Flags().BoolVarP(&quietFlag, "quiet", "q", false, "suppress chatter")
	cmd.Flags().BoolVar(&noColorFlag, "no-color", false, "disable colour output")
	return cmd
}

func runAdminTokenList(cwd string, opts output.RenderOpts) error {
	root, err := paths.FindProjectRoot(cwd)
	if err != nil {
		return err
	}
	toks, err := admin.ListTokens(root)
	if err != nil {
		return err
	}
	if opts.JSON {
		for _, t := range toks {
			revoked := "false"
			if t.Revoked {
				revoked = "true"
			}
			if err := output.WriteJSONL(map[string]string{
				"_type":    "token",
				"_version": "1",
				"id":       t.ID,
				"agent":    t.Agent,
				"created":  t.Created.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"),
				"revoked":  revoked,
			}, opts); err != nil {
				return err
			}
		}
		return nil
	}
	if len(toks) == 0 {
		if !opts.Quiet {
			output.WriteOut("no tokens minted yet", opts)
		}
		return nil
	}
	// Plain-text table: id, agent, created, revoked. We deliberately
	// avoid any column where a hash could surface; the public Token
	// projection enforces that statically (no Hash field).
	for _, t := range toks {
		state := "active"
		if t.Revoked {
			state = "revoked"
		}
		output.WriteData(fmt.Sprintf("%s  %s  %s  %s",
			t.ID,
			t.Agent,
			t.Created.UTC().Format("2006-01-02T15:04:05Z"),
			state,
		), opts)
	}
	return nil
}
